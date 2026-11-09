//go:build !windows

package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitHubHelperScope 验证真实子进程只查询固定主机且忽略仓库环境注入。
// 使用隔离gh／git替身，错误返回的主机必须拒绝，不调用任何真实凭证程序。
func TestGitHubHelperScope(t *testing.T) {
	directory := t.TempDir()
	for _, key := range []string{"SOTEA_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("PATH", directory)
	t.Setenv("GIT_DIR", "injected-repository")
	t.Setenv("GIT_CONFIG_COUNT", "9")
	t.Setenv("HOME", t.TempDir())
	gh := filepath.Join(directory, "gh")
	git := filepath.Join(directory, "git")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\n[ \"$*\" = 'auth token --hostname github.com' ] || exit 2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ -z \"${GIT_DIR:-}\" ] && [ -z \"${GIT_CONFIG_COUNT:-}\" ] || exit 2\n[ \"$GIT_TERMINAL_PROMPT\" = 0 ] && [ \"$GCM_INTERACTIVE\" = Never ] || exit 2\nIFS= read -r protocol\nIFS= read -r host\nIFS= read -r path\n[ \"$protocol\" = protocol=https ] && [ \"$host\" = host=github.com ] && [ \"$path\" = path=Spring-Ocean/gitea-cli-so ] || exit 3\nprintf 'protocol=https\\nhost=%s\\npassword=helper-fixture\\n' \"${SOTEA_FIXTURE_HOST:-github.com}\"\n"
	if err := os.WriteFile(git, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if githubCredential(context.Background()) != "helper-fixture" {
		t.Fatal("fixed helper query failed")
	}
	t.Setenv("SOTEA_FIXTURE_HOST", "attacker.invalid")
	if githubCredential(context.Background()) != "" {
		t.Fatal("mismatched credential scope accepted")
	}
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf 'gh-fixture\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if githubCredential(context.Background()) != "gh-fixture" {
		t.Fatal("gh did not take priority over Git")
	}
	// 超量输出不能通过io.Copy的ReadFrom优化绕过Write上限。
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf '%s' '"+strings.Repeat("a", 16385)+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if githubCredential(context.Background()) != "" {
		t.Fatal("oversized credential output accepted")
	}

}
