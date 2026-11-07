package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
)

// TestMenuDetectionDoesNotSendCredentialsToUnknownOrigin 保护启动探测的来源边界。
// 匿名版本成功也不授权自动登录；404／401／无效 JSON 保留不可验证状态和独立错误。
func TestMenuDetectionDoesNotSendCredentialsToUnknownOrigin(t *testing.T) {
	for _, sample := range []struct {
		name    string
		status  int
		payload string
		ready   bool
	}{
		{
			name:    "repository",
			status:  200,
			payload: "{\"version\":\"28.3.0\"}",
			ready:   true,
		},
		{
			name:    "missing",
			status:  404,
			payload: "{}",
		},
		{
			name:    "protected",
			status:  401,
			payload: "{}",
		},
		{
			name:    "other-platform",
			status:  200,
			payload: "<html>Other Git server</html>",
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, name := range []string{"SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO", "SOTEA_TOKEN_INSTANCE"} {
				t.Setenv(name, "")
			}
			t.Setenv("SOTEA_TOKEN", "synthetic-untrusted-token")
			var lock sync.Mutex
			credentialsSent := false
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				lock.Lock()
				credentialsSent = credentialsSent || request.Header.Get("Authorization") != ""
				lock.Unlock()
				if request.URL.Path == "/api/v1/version" {
					writer.WriteHeader(sample.status)
					_, _ = fmt.Fprint(writer, sample.payload)
					return
				}
				if request.URL.Path == "/api/v1/repos/team/repo" {
					_, _ = fmt.Fprint(writer, "{\"full_name\":\"team/repo\"}")
					return
				}
				http.Error(writer, "unexpected request", 500)
			}))
			defer server.Close()
			command, runtime := newRootCommand("test")
			command.SetContext(context.Background())
			runtime.options.instance = server.URL
			runtime.options.repo = "team/repo"
			runtime.options.account = "unverified-name"
			runtime.options.proxy = "direct"
			runtime.options.noLocal = true
			runtime.options.configPath = filepath.Join(t.TempDir(), "config.json")
			state := &menuState{}
			runtime.checkMenuTarget(command, command.Context(), state)
			lock.Lock()
			sent := credentialsSent
			lock.Unlock()
			if sent || state.ready != sample.ready {
				t.Fatalf("unexpected detection: credentials=%v ready=%v status=%q", sent, state.ready, state.availability)
			}
			if strings.Contains(state.availability, "Signed in as") {
				t.Fatal("an unverified explicit account was shown as a signed-in identity")
			}
			if !sample.ready && !strings.Contains(state.availability, "Unable to verify Gitea") {
				t.Fatalf("a failed probe was reported as another platform: %q", state.availability)
			}
		})
	}
}

// TestMenuDetectionVerifiesRegisteredIdentity 保护首页真实身份和私有实例的启动恢复。
// 匿名版本被拒绝时仅已登记根地址可带凭证重试，身份与仓库均通过后才开放业务菜单。
func TestMenuDetectionVerifiesRegisteredIdentity(t *testing.T) {
	for _, name := range []string{"SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO"} {
		t.Setenv(name, "")
	}
	t.Setenv("SOTEA_TOKEN", "synthetic-menu-token")
	t.Setenv("SOTEA_TOKEN_INSTANCE", "isolated")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/version" && request.Header.Get("Authorization") == "" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Header.Get("Authorization") != "token synthetic-menu-token" {
			http.Error(writer, "wrong synthetic identity", 401)
			return
		}
		switch request.URL.Path {
		case "/api/v1/version":
			_, _ = fmt.Fprint(writer, "{\"version\":\"1.22.6\"}")
		case "/api/v1/user":
			_, _ = fmt.Fprint(writer, "{\"login\":\"alice\",\"full_name\":\"Alice Ocean\",\"email\":\"alice@example.invalid\"}")
		case "/api/v1/repos/team/repo":
			_, _ = fmt.Fprint(writer, "{\"full_name\":\"team/repo\"}")
		default:
			http.Error(writer, "unexpected request", 500)
		}
	}))
	defer server.Close()
	store := config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Config
	next.Instances["isolated"] = config.Instance{
		URL:           server.URL,
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{
			"alice": {
				Source:      "environment",
				InstanceURL: server.URL,
			},
		},
	}
	if _, err := store.Save(context.Background(), snapshot, next); err != nil {
		t.Fatal(err)
	}
	command, runtime := newRootCommand("test")
	command.SetContext(context.Background())
	runtime.options.instance = "isolated"
	runtime.options.repo = "team/repo"
	runtime.options.noLocal = true
	runtime.options.configPath = store.Path
	state := &menuState{}
	runtime.checkMenuTarget(command, command.Context(), state)
	if !state.ready || !strings.Contains(state.availability, "Signed in as alice") {
		t.Fatalf("the registered API identity was not verified: %q", state.availability)
	}
	if state.home.Login != "alice" || state.home.Name != "Alice Ocean" || state.home.Email != "alice@example.invalid" {
		t.Fatal("the home screen did not receive the verified API profile")
	}
	runtime.options.repo = ""
	runtime.checkMenuTarget(command, command.Context(), state)
	if state.ready || state.home.Login != "alice" || !strings.Contains(state.availability, "No repository selected") {
		t.Fatal("missing repository prevented displaying a valid account profile")
	}
	runtime.options.account = "different-account"
	runtime.checkMenuTarget(command, command.Context(), state)
	if state.home.Login != "" || state.home.Name != "" || state.home.Email != "" || state.ready {
		t.Fatal("failed identity verification retained the previous user's profile")
	}
}
