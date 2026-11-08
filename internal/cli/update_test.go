package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestUpdateInstallationRequiresExplicitNoninteractiveConsent 保护脚本不能隐式替换程序。
// 在任何网络和缓存查询前拒绝缺少 --yes 的安装，JSON 仍保留既有错误契约。
func TestUpdateInstallationRequiresExplicitNoninteractiveConsent(t *testing.T) {
	for _, machine := range []bool{false, true} {
		var out, diagnostic bytes.Buffer
		command := NewRootCommand("v0.1.0-rc.1")
		command.SetOut(&out)
		command.SetErr(&diagnostic)
		args := []string{"update", "install", "--no-interactive"}
		if machine {
			args = append(args, "--json")
		}
		command.SetArgs(args)
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("non-interactive update did not require explicit consent: %v", err)
		}
	}
}
