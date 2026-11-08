package mcpintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// testLaunch 建立只使用测试二进制和临时路径的启动意图。
// 不执行该进程，不读取开发者配置或使用任何真实凭证。
func testLaunch(t *testing.T) Launch {
	t.Helper()
	private := t.TempDir()
	t.Setenv("HOME", private)
	t.Setenv("USERPROFILE", private)
	t.Setenv("APPDATA", private)
	t.Setenv("XDG_CONFIG_HOME", private)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Launch{
		Command:    binary,
		ConfigPath: filepath.Join(t.TempDir(), "config with spaces.json"),
		Instance:   "isolated",
		Account:    "alice",
	}
}

// writeTestFile 在临时目录保存独立配置样本。
// 写入失败立即终止当前场景，避免后续断言将不存在文件误判为成功。
func writeTestFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// readTestFile 返回测试文件的完整内容以验证实际磁盘副作用。
// 不忽略读取失败，也不使用生产适配器掩盖错误写入或文件删除。
func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestManagedLifecyclePreservesOtherSettings 保护四格式的安装、更新和仅移除自有项。
// 覆盖注释、尾随逗号、紧凑对象、含 URL 字符串和 TOML 多行内容，检查原设置及私有备份。
func TestManagedLifecyclePreservesOtherSettings(t *testing.T) {
	for _, sample := range []struct {
		client Client
		raw    string
		keep   string
	}{
		{Codex, "# personal settings\nmodel = 'example'\nnote = '''a\n[mcp_servers.fake]\nb'''\n[mcp_servers.other]\ncommand = 'other'\n", "# personal settings\nmodel = 'example'"},
		{ClaudeCode, "{\"theme\":\"dark\",\"mcpServers\":{\"other\":{\"command\":\"other\"}}}\n", "\"theme\":\"dark\""},
		{Cursor, "{\n// user comment\n\"mcpServers\": {\"other\": {\"url\": \"https://example.test/a//b/*c*/\"},},\n}\n", "// user comment"},
		{VSCode, "{\"inputs\":[],\"servers\":{ /* keep this */ }}\n", "/* keep this */"},
	} {
		t.Run(string(sample.client), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.conf")
			writeTestFile(t, path, []byte(sample.raw))
			launch := testLaunch(t)
			for step := 0; step < 3; step++ {
				snapshot, err := Load(context.Background(), sample.client, path)
				if err != nil {
					t.Fatal(err)
				}
				if step == 0 && snapshot.State != "absent" || step > 0 && snapshot.State != "managed" {
					t.Fatalf("unexpected ownership at step %d: %s", step, snapshot.State)
				}
				launch.AllowWrite, launch.AllowRawAPI = step == 1, step == 1
				change, err := snapshot.Prepare(launch, step == 2)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(readTestFile(t, path), snapshot.raw) {
					t.Fatal("preview changed the file")
				}
				backup, err := change.Apply(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(readTestFile(t, backup), snapshot.raw) {
					t.Fatal("backup did not preserve the previous file")
				}
				if runtime.GOOS != "windows" {
					info, err := os.Stat(backup)
					if err != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("backup permissions: %v %v", info, err)
					}
				}
				current, err := Load(context.Background(), sample.client, path)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(current.raw), sample.keep) {
					t.Fatal("unrelated content or comments were rewritten")
				}
				if step < 2 && (current.Launch.AllowWrite != launch.AllowWrite || current.Launch.AllowRawAPI != launch.AllowRawAPI || current.Launch.ConfigPath != launch.ConfigPath) {
					t.Fatalf("launch did not round trip: %+v", current.Launch)
				}
				if step == 2 && current.State != "absent" {
					t.Fatal("remove left an owned entry behind")
				}
			}
		})
	}
}

// TestConfigBoundariesRejectUnsafeWrites 保护同名冲突、外部编辑、锁和取消的写入边界。
// 每种失败都比较实际原文件，避免以错误返回掩盖已经覆盖的用户设置。
func TestConfigBoundariesRejectUnsafeWrites(t *testing.T) {
	launch := testLaunch(t)
	for _, scenario := range []string{"unmanaged", "external-change", "ownership-change", "locked", "cancelled", "edited-entry"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			original := []byte("{\"mcpServers\":{}}")
			if scenario == "unmanaged" {
				original = []byte("{\"mcpServers\":{\"sotea\":{\"command\":\"user-owned\"}}}")
			}
			writeTestFile(t, path, original)
			snapshot, err := Load(context.Background(), Cursor, path)
			if err != nil {
				t.Fatal(err)
			}
			change, err := snapshot.Prepare(launch, false)
			if scenario == "unmanaged" {
				if err == nil || !bytes.Equal(readTestFile(t, path), original) {
					t.Fatal("unmanaged entry could be overwritten")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch scenario {
			case "external-change":
				original = []byte("{\"outside\":true}")
				writeTestFile(t, path, original)
			case "ownership-change":
				writeTestFile(t, path+".sotea-mcp.json", []byte("{}"))
			case "locked":
				writeTestFile(t, path+".sotea-mcp.lock", nil)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "edited-entry":
				if _, err := change.Apply(ctx); err != nil {
					t.Fatal(err)
				}
				original = bytes.ReplaceAll(readTestFile(t, path), []byte("alice"), []byte("mallory"))
				writeTestFile(t, path, original)
				edited, err := Load(ctx, Cursor, path)
				if err != nil || edited.State != "changed" {
					t.Fatalf("external entry edit: %s %v", edited.State, err)
				}
				if _, err := edited.Prepare(launch, true); err == nil {
					t.Fatal("externally changed entry could be removed")
				}
				return
			}
			if _, err := change.Apply(ctx); err == nil {
				t.Fatal("unsafe write accepted")
			}
			if !bytes.Equal(readTestFile(t, path), original) {
				t.Fatal("rejected change modified client configuration")
			}
		})
	}
}

// TestJSONCRemovePositionAndValidation 保护只有一个条目或处于首尾时的精确删除。
// 安装后添加其他服务器属于合法外部编辑；重复目标键、坏语法和非对象根仍必须拒绝。
func TestJSONCRemovePositionAndValidation(t *testing.T) {
	for _, contents := range []string{
		"{\"mcpServers\":{\"sotea\":ENTRY,}}",
		"{\"mcpServers\":{\"sotea\":ENTRY,\"other\":{}}}",
		"{\"mcpServers\":{\"other\":{},\"sotea\":ENTRY,}}",
		"{\"mcpServers\":{\"other\":{},\"sotea\":ENTRY}}",
	} {
		path := filepath.Join(t.TempDir(), "mcp.json")
		launch := testLaunch(t)
		snapshot, err := Load(context.Background(), Cursor, path)
		if err != nil {
			t.Fatal(err)
		}
		change, err := snapshot.Prepare(launch, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := change.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		installed, err := Load(context.Background(), Cursor, path)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path, []byte(strings.Replace(contents, "ENTRY", string(installed.doc.entryBytes()), 1)))
		current, err := Load(context.Background(), Cursor, path)
		if err != nil || current.State != "managed" {
			t.Fatalf("moving an unchanged entry lost ownership: %s %v", current.State, err)
		}
		remove, err := current.Prepare(Launch{}, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := remove.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(context.Background(), Cursor, path); err != nil {
			t.Fatal(err)
		}
	}
	for _, contents := range []string{"{", "[]", "null", "", "{\"mcpServers\":null}", "{\"mcpServers\":{},\"mcpServers\":{}}", "{\"mcpServers\":{\"sotea\":{},\"sotea\":{}}}", "{/*unterminated"} {
		path := filepath.Join(t.TempDir(), "mcp.json")
		writeTestFile(t, path, []byte(contents))
		if _, err := Load(context.Background(), VSCode, path); err == nil {
			// VS Code 的目标容器是 servers，因此另以 Cursor 验证 mcpServers 约束。
			if _, err := Load(context.Background(), Cursor, path); err == nil {
				t.Fatalf("invalid document accepted: %q", contents)
			}
		}
	}
}

// TestInterruptedOwnershipJournalAllowsRecovery 保护日志先于配置替换的崩溃恢复语义。
// 无论磁盘仍是旧条目还是已经是新条目，都只能识别日志中明确记录的两份自有内容。
func TestInterruptedOwnershipJournalAllowsRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	launch := testLaunch(t)
	snapshot, err := Load(context.Background(), Codex, path)
	if err != nil {
		t.Fatal(err)
	}
	change, err := snapshot.Prepare(launch, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, err := Load(context.Background(), Codex, path)
	if err != nil {
		t.Fatal(err)
	}
	launch.AllowWrite = true
	change, err = old.Prepare(launch, false)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(ownership{Version: 1, Client: Codex, Path: path, Previous: digest(old.doc.entryBytes()), Next: change.entryHash})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path+".sotea-mcp.json", journal)
	for _, raw := range [][]byte{old.raw, change.next} {
		writeTestFile(t, path, raw)
		current, err := Load(context.Background(), Codex, path)
		if err != nil || current.State != "managed" {
			t.Fatalf("journal recovery: %s %v", current.State, err)
		}
	}
	// 标记外的键也属于最后的 TOML 表，不能借字节块未变绕过条目所有权。
	writeTestFile(t, path, append(bytes.Clone(change.next), []byte("enabled = false\n")...))
	current, err := Load(context.Background(), Codex, path)
	if err != nil || current.State != "changed" {
		t.Fatalf("out-of-block TOML edit bypassed ownership: %s %v", current.State, err)
	}
}

// TestSymlinkAndPermissionIntent 保护最终文件链接及独立权限开关的边界。
// 权限模型不生成 env 或自动批准选项；Windows 链接创建受权限限制时单列运行缺口。
func TestSymlinkAndPermissionIntent(t *testing.T) {
	launch := testLaunch(t)
	entry, err := launch.Entry(Codex)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("--allow-")) || entry["env"] != nil {
		t.Fatal("default integration grants extra permissions or stores credentials")
	}
	launch.AllowRawAPI = true
	entry, err = launch.Entry(Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(entry["args"].([]string), " "), "--allow-write") {
		t.Fatal("raw API implied business writes")
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	target := filepath.Join(t.TempDir(), "original.json")
	writeTestFile(t, target, []byte("{}"))
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows symlink privileges require real platform validation")
		}
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), Cursor, path); err == nil || app.Failure(err).Code != app.ConfigError {
		t.Fatalf("symbolic link accepted: %v", err)
	}
}

// TestFollowingConfigurationHasNoFixedProject 保护四客户端跟随配置的持久化和更新语义。
// 只由具体客户端选择已验证启动适配器，不保存 cwd／repo／workdir，也不改变写或 raw 权限。
func TestFollowingConfigurationHasNoFixedProject(t *testing.T) {
	launch := testLaunch(t)
	launch.FollowWorkspace = true
	for _, client := range []Client{Codex, ClaudeCode, Cursor, VSCode} {
		path := filepath.Join(t.TempDir(), "client.conf")
		snapshot, err := Load(context.Background(), client, path)
		if err != nil {
			t.Fatal(err)
		}
		change, err := snapshot.Prepare(launch, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := change.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		installed, err := Load(context.Background(), client, path)
		if err != nil || installed.State != "managed" || !installed.Launch.FollowWorkspace {
			t.Fatalf("follow mode did not round trip: %+v %v", installed.Launch, err)
		}
		entry, err := installed.Launch.Entry(client)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(entry["args"].([]string), " ")
		if entry["cwd"] != nil || strings.Contains(args, "--workdir") || strings.Contains(args, "--repo") || strings.Contains(args, "--allow-") {
			t.Fatalf("following configuration retained fixed paths or extra permissions: %s", args)
		}
		adapter := strings.Contains(args, "--workspace-source")
		if adapter != (client == Codex || client == ClaudeCode) {
			t.Fatalf("client launch adapter mismatch: %s %s", client, args)
		}
	}
	launch.Workdir = t.TempDir()
	if _, err := launch.Entry(Codex); err == nil {
		t.Fatal("following mode accepted a pinned authorization root")
	}
	launch.Workdir, launch.Repository = "", "team/repo"
	if _, err := launch.Entry(Codex); err == nil {
		t.Fatal("following mode accepted a stale default repository")
	}
}
