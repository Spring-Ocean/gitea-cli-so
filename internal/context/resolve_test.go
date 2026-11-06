package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
)

// init 仅在明确测试环境下返回旧 Git 版本，不执行真实仓库或凭证操作。
// 该夹具用于验证功能下限不会阻止显式纯 API，也不会误推断本地上下文。
func init() {
	if os.Getenv("SOTEA_CONTEXT_OLD_GIT") == "1" {
		fmt.Fprintln(os.Stdout, "git version 2.42.0")
		os.Exit(0)
	}
}

// TestOldGitRequiresExplicitAPI 保护最低 Git 功能基线及纯 API 的独立入口。
// 旧 Git 不被用于发现仓库，明确实例和仓库仍可继续，透传规则由独立入口保留。
func TestOldGitRequiresExplicitAPI(t *testing.T) {
	runner := git.Runner{Executable: os.Args[0], Environment: []string{"SOTEA_CONTEXT_OLD_GIT=1"}}
	settings := config.Config{Version: 1, Instances: map[string]config.Instance{"team": {URL: "https://git.invalid"}}}
	getenv := func(string) string {
		return ""
	}
	directory := t.TempDir()
	value, err := Resolve(context.Background(), Input{Workdir: directory, Instance: "team", Repo: "owner/repo", RequireRepo: true}, settings, runner, getenv)
	if err != nil || value.Local != nil {
		t.Fatal("old Git prevented an explicit pure API context")
	}
	if _, err := Resolve(context.Background(), Input{Workdir: directory, RequireRepo: true}, settings, runner, getenv); err == nil || app.Failure(err).Code != app.GitUnavailable {
		t.Fatal("old Git did not provide an actionable upgrade boundary")
	}
}

// TestTargetPrecedenceAndRemoteBoundaries 保护多 remote、代理子路径与跨实例显式覆盖。
// 使用隔离 Git 仓库，来源解析不得向 origin、默认实例或其他账号偷偷回退。
func TestTargetPrecedenceAndRemoteBoundaries(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "中文 worktree")
	runner := git.Runner{Environment: []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_COUNT=0"}}
	ctx := context.Background()
	for _, args := range [][]string{{"init", "--initial-branch=main", directory}, {"-C", directory, "remote", "add", "origin", "https://git.example.invalid:8443/prefix/team/fork.git"}, {"-C", directory, "remote", "add", "upstream", "https://git.example.invalid:8443/prefix/team/upstream.git"}} {
		if _, err := runner.Capture(ctx, "", args, nil); err != nil {
			t.Fatal(err)
		}
	}
	settings := config.Config{
		Version:         1,
		DefaultInstance: "other",
		Instances: map[string]config.Instance{
			"team": {
				URL:           "https://git.example.invalid:8443/prefix",
				Accounts:      map[string]config.Account{"a": {}, "b": {}},
				ActiveAccount: "a",
			},
			"other": {URL: "https://other.example.invalid"},
		},
	}
	noEnvironment := func(string) string {
		return ""
	}
	_, err := Resolve(ctx, Input{
		Workdir:     directory,
		RequireRepo: true,
	}, settings, runner, noEnvironment)
	if err == nil || app.Failure(err).Code != app.ContextAmbiguous {
		t.Fatal("multiple remotes silently selected an origin")
	}
	resolved, err := Resolve(ctx, Input{
		Workdir:     directory,
		Remote:      "upstream",
		RequireRepo: true,
	}, settings, runner, noEnvironment)
	if err != nil || resolved.Name != "team" || resolved.Repository != "team/upstream" || !resolved.LocalMatches || resolved.Credential.Path != "prefix/team/upstream.git" || resolved.Account != "a" {
		t.Fatalf("remote mapping or context precedence was lost: %+v %v", resolved, err)
	}
	_, err = Resolve(ctx, Input{
		Workdir:     directory,
		Instance:    "other",
		Remote:      "origin",
		RequireRepo: true,
	}, settings, runner, noEnvironment)
	if err == nil || app.Failure(err).Code != app.ContextAmbiguous {
		t.Fatal("conflicting explicit instance reused the remote repository")
	}
	resolved, err = Resolve(ctx, Input{
		Workdir:     directory,
		Instance:    "other",
		Repo:        "team/explicit",
		RequireRepo: true,
	}, settings, runner, noEnvironment)
	if err != nil || resolved.LocalMatches || resolved.Credential.Host != "" || resolved.Repository != "team/explicit" {
		t.Fatal("explicit cross-repository operation retained unrelated Git credentials")
	}
	instance := settings.Instances["team"]
	instance.ActiveAccount = ""
	instance.LoggedOut = map[string]bool{"a": true}
	settings.Instances["team"] = instance
	settings.Repositories = []config.Binding{{
		Path:       directory,
		Remote:     "upstream",
		Instance:   "team",
		Repository: "team/upstream",
	}}
	resolved, err = Resolve(ctx, Input{
		Workdir:     directory,
		RequireRepo: true,
	}, settings, runner, noEnvironment)
	if err != nil || resolved.Account != "" || resolved.Remote != "upstream" {
		t.Fatal("logout allowed automatic account takeover or ignored the remote binding")
	}
}

// TestRemoteMappingAndNoGitContext 保护协议、端口、SSH 映射和无 Git 的显式入口。
// 未映射 SSH 与非法仓库必须失败；HTTP helper 保留原始用户名和路径。
func TestRemoteMappingAndNoGitContext(t *testing.T) {
	settings := config.Config{
		Version: 1,
		Instances: map[string]config.Instance{"team": {
			URL:         "https://git.invalid:9443/gitea",
			SSHMappings: map[string]string{"alias:2222": "https://git.invalid:9443/gitea", "alias": "https://git.invalid:9443/gitea"},
		}},
	}
	for _, raw := range []string{"ssh://git@alias:2222/team/repo.git", "git@alias:team/repo.git"} {
		root, repo, credential, err := parseRemote(raw, settings)
		if err != nil || root != "https://git.invalid:9443/gitea" || repo != "team/repo" || credential.Host != "" {
			t.Fatalf("explicit SSH mapping failed: %v", err)
		}
	}
	if _, _, _, err := parseRemote("git@unmapped:team/repo.git", settings); err == nil {
		t.Fatal("SSH alias was guessed as an HTTP host")
	}
	root, repo, credential, err := parseRemote("https://oauth2@git.invalid:9443/gitea/team/repo.git", settings)
	if err != nil || root != "https://git.invalid:9443/gitea" || repo != "team/repo" || credential.Username != "oauth2" || credential.Path != "gitea/team/repo.git" {
		t.Fatal("HTTP credential scope was changed")
	}
	resolved, err := Resolve(context.Background(), Input{
		Workdir:     t.TempDir(),
		Instance:    "team",
		Repo:        "team/repo",
		RequireRepo: true,
	}, settings, git.Runner{Executable: filepath.Join(t.TempDir(), "missing")}, func(string) string {
		return ""
	})
	if err != nil || resolved.Name != "team" || resolved.Local != nil {
		t.Fatalf("explicit API target required Git: %v", err)
	}
	for _, repo := range []string{"../repo", "team/..", "team/repo/extra", "https://git.invalid/team/repo", "team/%2f"} {
		if ValidRepository(repo) {
			t.Fatalf("unsafe repository was accepted: %q", repo)
		}
	}
}
