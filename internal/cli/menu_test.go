package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	"github.com/spf13/cobra"
)

// TestNoninteractiveEntryContract 防止根入口在管道、机器模式或帮助路径等待交互。
// --help 先于工作目录验证且不访问配置，--yes 不能补出缺失的业务目标。
func TestNoninteractiveEntryContract(t *testing.T) {
	for _, sample := range []struct {
		args []string
		code int
		want string
	}{
		{
			args: nil,
			code: 0,
			want: "Usage:",
		},
		{
			args: []string{"--json"},
			code: 2,
			want: "INVALID_ARGUMENT",
		},
		{
			args: []string{"--yes"},
			code: 0,
			want: "Usage:",
		},
		{
			args: []string{"--json", "--help", "--workdir", "/missing/sotea-help-test"},
			code: 0,
			want: "Usage:",
		},
		{
			args: []string{"--color", "invalid", "version"},
			code: 2,
			want: "INVALID_ARGUMENT",
		},
	} {
		var out, diagnostic bytes.Buffer
		code := Execute(context.Background(), sample.args, strings.NewReader(""), &out, &diagnostic, "test")
		if code != sample.code || !strings.Contains(out.String()+diagnostic.String(), sample.want) {
			t.Fatalf("entry %v: code=%d output=%q", sample.args, code, out.String()+diagnostic.String())
		}
	}
}

// TestMenuInventoryUsesRegisteredCommands 保护菜单覆盖真实首版叶子入口。
// MCP 协议进程单独启动；其余业务树均能从稳定路径导航，不复制一份易漂移的清单。
func TestMenuInventoryUsesRegisteredCommands(t *testing.T) {
	root := NewRootCommand("test")
	var inspect func(*cobra.Command)
	inspect = func(parent *cobra.Command) {
		choices := menuChoices(parent)
		ids := map[string]bool{}
		for _, choice := range choices {
			ids[choice.ID] = true
		}
		for _, command := range parent.Commands() {
			if command.Name() == "mcp" {
				continue
			}
			path := strings.TrimPrefix(command.CommandPath(), root.Name()+" ")
			if !ids[path] {
				t.Fatalf("missing menu action %q", path)
			}
			if command.RunE == nil {
				inspect(command)
			}
		}
	}
	inspect(root)
}

// TestSessionReloadsExternalAccountChange 保护持续菜单不缓存活动身份。
// 外部配置变更后重新显示账号来源，不能继续展示先前账号或无声覆盖外部内容。
func TestSessionReloadsExternalAccountChange(t *testing.T) {
	for _, name := range []string{"SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO", "SOTEA_TOKEN"} {
		t.Setenv(name, "")
	}
	directory := t.TempDir()
	store := config.Store{Path: filepath.Join(directory, "config.json")}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Config
	next.DefaultInstance = "isolated"
	next.Instances["isolated"] = config.Instance{
		URL:           "https://gitea.invalid",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{
			"alice": {Source: "environment"},
			"bob":   {Source: "environment"},
		},
	}
	snapshot, err = store.Save(context.Background(), snapshot, next)
	if err != nil {
		t.Fatal(err)
	}
	command, runtime := newRootCommand("test")
	command.SetContext(context.Background())
	var diagnostic bytes.Buffer
	command.SetErr(&diagnostic)
	runtime.options.workdir = directory
	runtime.options.configPath = store.Path
	state := &menuState{}
	header, err := runtime.menuContext(command, state)
	if err != nil || !strings.Contains(header, "alice") {
		t.Fatalf("initial context: %q %v", header, err)
	}
	next = snapshot.Config
	instance := next.Instances["isolated"]
	instance.ActiveAccount = "bob"
	delete(instance.Accounts, "alice")
	instance.LoggedOut = map[string]bool{"alice": true}
	next.Instances["isolated"] = instance
	if _, err := store.Save(context.Background(), snapshot, next); err != nil {
		t.Fatal(err)
	}
	header, err = runtime.menuContext(command, state)
	if err != nil || strings.Contains(header, "alice") || !strings.Contains(header, "bob") ||
		!strings.Contains(diagnostic.String(), "Configuration changed") {
		t.Fatalf("stale session context: %q %v", header, err)
	}
	if runtime.options.lastResult != nil {
		t.Fatal("context inspection fabricated a business result")
	}
}
