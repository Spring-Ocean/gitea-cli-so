package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/catalog"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	"github.com/spf13/cobra"
)

// TestBrowseRepositoryRestoresContextOnWorkspaceFailure 保护远端工作台的会话上下文归还。
// 目标验证成功但无法进入交互时，原仓库、remote、账号、结果与本地发现开关均必须恢复。
func TestBrowseRepositoryRestoresContextOnWorkspaceFailure(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reads.Add(1)
		if request.Header.Get("Authorization") != "token synthetic-browse-token" {
			http.Error(writer, "unexpected identity", http.StatusUnauthorized)
			return
		}
		var response any
		switch request.URL.Path {
		case "/api/v1/user":
			response = map[string]any{"login": "alice"}
		case "/api/v1/repos/team/selected":
			response = map[string]any{
				"id":        71,
				"name":      "selected",
				"full_name": "team/selected",
				"owner":     map[string]string{"login": "team"},
			}
		default:
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer server.Close()
	command, runtime, store, snapshot := browseTestRuntime(t, server.URL)
	previous := &app.Result{}
	runtime.options.lastResult = previous
	original := *runtime.options
	scope := browseScope{instance: server.URL, account: "alice", revision: snapshot.RevisionKey()}
	err := runtime.browseRepository(command, "test", scope, catalog.Item{ID: 71, FullName: "team/selected"})
	if app.Failure(err).Code != app.InvalidArgument || reads.Load() == 0 {
		t.Fatalf("expected a verified target followed by a noninteractive exit: %v", err)
	}
	current := runtime.options
	if current.instance != original.instance || current.repo != original.repo ||
		current.remote != original.remote || current.account != original.account ||
		current.noLocal != original.noLocal || current.browseScope != original.browseScope ||
		current.lastResult != previous {
		t.Fatal("a failed remote workspace retained its context in the parent session")
	}
	after, err := store.Load(context.Background())
	if err != nil || after.RevisionKey() != snapshot.RevisionKey() {
		t.Fatalf("browsing changed persisted configuration: %v", err)
	}
}

// TestBrowseActionRejectsExternalConfigurationChange 保护子命令继承账户浏览的身份边界。
// 外部配置修订后执行真实菜单动作必须在 HTTP 或凭证查询前失败，不能用新配置续用旧选择。
func TestBrowseActionRejectsExternalConfigurationChange(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "request should have been blocked", http.StatusInternalServerError)
	}))
	defer server.Close()
	command, runtime, store, snapshot := browseTestRuntime(t, server.URL)
	runtime.options.browseScope = &browseScope{
		instance: server.URL,
		account:  "alice",
		revision: snapshot.RevisionKey(),
	}
	next := snapshot.Config
	next.DefaultInstance = "isolated"
	if _, err := store.Save(context.Background(), snapshot, next); err != nil {
		t.Fatal(err)
	}
	err := runtime.menuAction(command, "test", "repo view", &menuState{})
	if app.Failure(err).Code != app.Conflict || requests.Load() != 0 {
		t.Fatalf("stale action was not blocked before HTTP: requests=%d error=%v", requests.Load(), err)
	}
}

// browseTestRuntime 建立使用临时配置和合成 token 的菜单运行边界。
// 普通内存流故意不提供交互能力，测试只读本机服务，不触碰开发者凭证或 Git 配置。
func browseTestRuntime(t *testing.T, serverURL string) (*cobra.Command, *runtime, config.Store, config.Snapshot) {
	t.Helper()
	for _, name := range []string{"SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO", "SOTEA_CONFIG"} {
		t.Setenv(name, "")
	}
	t.Setenv("SOTEA_TOKEN", "synthetic-browse-token")
	t.Setenv("SOTEA_TOKEN_INSTANCE", "isolated")
	directory := t.TempDir()
	store := config.Store{Path: filepath.Join(directory, "config.json")}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Config
	next.Instances["isolated"] = config.Instance{
		URL:           serverURL,
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{
			"alice": {Source: "environment", InstanceURL: serverURL},
		},
	}
	snapshot, err = store.Save(context.Background(), snapshot, next)
	if err != nil {
		t.Fatal(err)
	}
	command, runtime := newRootCommand("test")
	command.SetContext(context.Background())
	command.SetIn(bytes.NewBuffer(nil))
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	runtime.options.workdir = directory
	runtime.options.configPath = store.Path
	runtime.options.instance = "isolated"
	runtime.options.repo = "team/original"
	runtime.options.account = "alice"
	return command, runtime, store, snapshot
}
