package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// workspaceURI 将测试目录编码成标准 file URI，保留空格和非 ASCII 字符。
// 不把文件路径直接拼接进 URI，避免测试绕过生产边界的转义约束。
func workspaceURI(path string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

// workspaceRepository 创建与开发者 Git 配置隔离的真实仓库。
// 固定 Git 查询需要有效 HEAD，提交只发生于临时目录且不使用 hooks 或签名。
func workspaceRepository(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", directory},
		{"-C", directory, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--quiet", "--allow-empty", "-m", "fixture"},
	} {
		command := exec.Command("git", args...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture Git: %v %s", err, output)
		}
	}
	actual, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

// connectWorkspaceClient 在同一服务上创建第二个独立客户端会话。
// 客户端根仅通过 MCP 协议提供，便于检查不同会话不会共享本地授权或写计划。
func connectWorkspaceClient(t *testing.T, host *Host, root string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "workspace-client", Version: "1"}, nil)
	client.AddRoots(&sdk.Root{URI: workspaceURI(root)})
	a, b := sdk.NewInMemoryTransports()
	serverSession, err := host.Server.Connect(context.Background(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if err := serverSession.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}

// TestWorkspaceSessionsAndPlansAreIsolated 保护两项目会话隔离和跨根写计划禁止复用。
// 真实 Git 查询验证当前根；写计划检查不得因工具参数、其他会话或切回旧目录而恢复。
func TestWorkspaceSessionsAndPlansAreIsolated(t *testing.T) {
	rootA, rootB := workspaceRepository(t), workspaceRepository(t)
	fixture, host, clientA := newProtocolFixture(t, Options{FollowWorkspace: true, AllowWrite: true}, &sdk.Root{URI: workspaceURI(rootA)})
	clientB := connectWorkspaceClient(t, host, rootB)
	for _, sample := range []struct {
		client *sdk.ClientSession
		root   string
	}{{clientA, rootA}, {clientB, rootB}} {
		result := callProtocolTool(t, sample.client, "git_context", map[string]any{})
		if result.ExitCode() != 0 || len(result.Items) != 1 {
			t.Fatalf("workspace Git failed: %+v", result)
		}
		encoded, err := json.Marshal(result.Items[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Root string `json:"root"`
		}
		if err := json.Unmarshal(encoded, &data); err != nil || data.Root != sample.root {
			t.Fatalf("workspace crossed sessions: %s %v", data.Root, err)
		}
	}
	outside := callProtocolTool(t, clientA, "git_context", map[string]any{"workdir": rootB})
	if outside.Error == nil || outside.Error.Code != app.PathOutsideRoot {
		t.Fatalf("tool argument expanded the root: %+v", outside)
	}
	prepared := prepareClose(t, clientA, "workspace-plan", []int64{1})
	if prepared.Meta == nil || prepared.Meta.Plan == nil || prepared.Meta.Plan.PlanID == "" {
		t.Fatalf("could not prepare workspace-bound plan: %+v", prepared)
	}
	commit := map[string]any{"plan_id": prepared.Meta.Plan.PlanID, "confirmation_token": prepared.Meta.Plan.Token}
	denied := callProtocolTool(t, clientB, "write_commit", commit)
	if denied.Error == nil || fixture.writes != 0 {
		t.Fatal("another session executed the original plan")
	}
	fixture.client.RemoveRoots(workspaceURI(rootA))
	fixture.client.AddRoots(&sdk.Root{URI: workspaceURI(rootB)})
	denied = callProtocolTool(t, clientA, "write_commit", commit)
	if denied.Error == nil || fixture.writes != 0 {
		t.Fatal("changed workspace executed the old plan")
	}
	fixture.client.RemoveRoots(workspaceURI(rootB))
	fixture.client.AddRoots(&sdk.Root{URI: workspaceURI(rootA)})
	denied = callProtocolTool(t, clientA, "write_commit", commit)
	if denied.Error == nil || fixture.writes != 0 {
		t.Fatal("restoring a root revived the old plan")
	}
}

// TestMissingAndAmbiguousWorkspaceKeepRemoteTools 保护缺失根和多根时的远端可用性。
// 不猜 cwd、不合并父目录；同一远端写计划仍可提交，避免失败根反复换绑阻断远端业务。
func TestMissingAndAmbiguousWorkspaceKeepRemoteTools(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		roots := []*sdk.Root{}
		if multiple {
			roots = append(roots, &sdk.Root{URI: workspaceURI(t.TempDir())}, &sdk.Root{URI: workspaceURI(t.TempDir())})
		}
		fixture, _, client := newProtocolFixture(t, Options{FollowWorkspace: true, AllowWrite: true}, roots...)
		local := callProtocolTool(t, client, "git_context", map[string]any{})
		if local.Error == nil || local.Error.Code != app.PathOutsideRoot {
			t.Fatalf("missing or ambiguous roots granted local access: %+v", local)
		}
		remote := callProtocolTool(t, client, "context_get", map[string]any{})
		if remote.ExitCode() != 0 {
			t.Fatalf("workspace failure blocked remote context: %+v", remote)
		}
		prepared := prepareClose(t, client, "remote-without-root", []int64{1})
		if prepared.Meta == nil || prepared.Meta.Plan == nil || prepared.Meta.Plan.PlanID == "" {
			t.Fatalf("workspace failure blocked remote preparation: %+v", prepared)
		}
		committed := callProtocolTool(t, client, "write_commit", map[string]any{
			"plan_id":            prepared.Meta.Plan.PlanID,
			"confirmation_token": prepared.Meta.Plan.Token,
		})
		if committed.ExitCode() != 0 || fixture.writes != 1 {
			t.Fatalf("workspace failure blocked remote commit: %+v", committed)
		}
	}
}

// TestWorkspaceURIRejectsUnclearBoundaries 保护客户端根 URI 解析和过宽授权限制。
// 普通非 Git 项目目录允许授权文件；主目录、卷根及带远程来源的 URI 不自动授权。
func TestWorkspaceURIRejectsUnclearBoundaries(t *testing.T) {
	root := t.TempDir()
	withSpaces := filepath.Join(root, "project with 空格")
	if err := os.Mkdir(withSpaces, 0o700); err != nil {
		t.Fatal(err)
	}
	actual, err := workspacePath(workspaceURI(withSpaces))
	if err != nil || !strings.HasSuffix(actual, "project with 空格") {
		t.Fatalf("ordinary project URI: %q %v", actual, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{"https://example.invalid/project", "file://remote-server/share", "file:relative", "file:///missing/project", workspaceURI(home), workspaceURI(filepath.VolumeName(root) + string(filepath.Separator)), workspaceURI(root) + "?token=x", workspaceURI(root) + "#fragment"} {
		if _, err := workspacePath(uri); err == nil {
			t.Fatalf("unsafe workspace accepted: %q", uri)
		}
	}
}

// TestExplicitClaudeAdapterCannotRestoreRevokedRoots 保护客户端适配器只补充初始缺失根。
// 一旦会话采用 MCP roots，撤销后不得用旧启动环境重新授权；环境后续变化也不能迁移原会话。
func TestExplicitClaudeAdapterCannotRestoreRevokedRoots(t *testing.T) {
	rootA, rootB := workspaceRepository(t), workspaceRepository(t)
	t.Setenv("CLAUDE_PROJECT_DIR", rootA)
	fixture, host, client := newProtocolFixture(t, Options{FollowWorkspace: true, WorkspaceSource: "claude"})
	initial := callProtocolTool(t, client, "git_context", map[string]any{})
	if initial.ExitCode() != 0 {
		t.Fatalf("explicit client project environment was unavailable: %+v", initial)
	}
	if host.launchWorkspace != rootA {
		t.Fatal("the client adapter did not retain its startup project")
	}
	t.Setenv("CLAUDE_PROJECT_DIR", rootB)
	if host.launchWorkspace != rootA {
		t.Fatal("changing an environment variable migrated the active client project")
	}
	fixture.client.AddRoots(&sdk.Root{URI: workspaceURI(rootB)})
	changed := callProtocolTool(t, client, "git_context", map[string]any{})
	if changed.ExitCode() != 0 {
		t.Fatalf("explicit protocol root could not replace the launch adapter: %+v", changed)
	}
	fixture.client.RemoveRoots(workspaceURI(rootB))
	denied := callProtocolTool(t, client, "git_context", map[string]any{})
	if denied.Error == nil || denied.Error.Code != app.PathOutsideRoot {
		t.Fatalf("revoked protocol roots fell back to the old project environment: %+v", denied)
	}
}
