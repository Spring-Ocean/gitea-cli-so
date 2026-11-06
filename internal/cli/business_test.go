package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
)

// TestReadOnlyCLIAndRawBoundaries 保护无 Git 的真实只读链路及原始 API 输出。
// 版本入口被拦截时仍可显式读 API，凭证账号不匹配时业务请求数保持不变。
func TestReadOnlyCLIAndRawBoundaries(t *testing.T) {
	var businessCalls atomic.Int32
	var identity atomic.Value
	identity.Store("alice")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prefix/api/v1/user":
			json.NewEncoder(w).Encode(map[string]string{"login": identity.Load().(string)})
		case "/prefix/api/v1/version":
			w.WriteHeader(404)
			w.Write([]byte("version blocked"))
		case "/prefix/api/v1/raw":
			if len(r.URL.Query()["key"]) != 2 {
				t.Error("raw duplicate query values were lost")
			}
			w.WriteHeader(403)
			w.Write([]byte("original body"))
		default:
			businessCalls.Add(1)
			if strings.HasSuffix(r.URL.Path, "/branches") || strings.HasSuffix(r.URL.Path, "/tags") || strings.HasSuffix(r.URL.Path, "/commits") || strings.HasSuffix(r.URL.Path, "/labels") || strings.HasSuffix(r.URL.Path, "/milestones") {
				w.Write([]byte(`[{"id":9007199254740993,"name":"sample"}]`))
			} else {
				w.Write([]byte(`{"id":9007199254740993,"full_name":"team/repo"}`))
			}
		}
	}))
	defer server.Close()
	t.Setenv("SOTEA_TOKEN", "synthetic-cli-token")
	t.Setenv("SOTEA_TOKEN_INSTANCE", "team")
	t.Setenv("SOTEA_INSTANCE", "")
	t.Setenv("SOTEA_ACCOUNT", "")
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	store := config.Store{Path: path}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Instances["team"] = config.Instance{
		URL:           server.URL + "/prefix",
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{"alice": {
			InstanceURL: server.URL + "/prefix",
			Source:      "environment",
		}},
	}
	if _, err := store.Save(context.Background(), snapshot, snapshot.Config); err != nil {
		t.Fatal(err)
	}
	run := func(args []string) (int, string, string) {
		t.Helper()
		var out, diagnostic bytes.Buffer
		base := []string{"--config", path, "--workdir", directory, "--instance", "team", "--repo", "team/repo"}
		code := Execute(context.Background(), append(base, args...), nil, &out, &diagnostic, "test")
		return code, out.String(), diagnostic.String()
	}
	for _, args := range [][]string{{"repo", "view"}, {"branch", "list"}, {"tag", "list"}, {"commit", "list"}, {"commit", "status", "main"}, {"label", "list"}, {"milestone", "list"}} {
		code, text, diagnostic := run(append(args, "--json"))
		var result app.Result
		if err := json.Unmarshal([]byte(text), &result); err != nil || code != 0 || result.Status != app.Success || diagnostic != "" || !strings.Contains(text, "9007199254740993") {
			t.Fatalf("read-only command failed for %v: code=%d", args, code)
		}
	}
	code, raw, diagnostic := run([]string{"api", "/raw", "--query", "key=first", "--query", "key=second", "--json"})
	if code != 1 || raw != "original body" || !strings.Contains(diagnostic, "PERMISSION_DENIED") {
		t.Fatal("raw API body was enveloped or non-success status was lost")
	}
	before := businessCalls.Load()
	identity.Store("bob")
	code, text, _ := run([]string{"repo", "view", "--json"})
	if code != 1 || !strings.Contains(text, "AUTH_IDENTITY_MISMATCH") || businessCalls.Load() != before {
		t.Fatal("identity mismatch reached the repository API")
	}
	identity.Store("alice")
	code, text, _ = run([]string{"instance", "info", "--json"})
	if code != 1 || !json.Valid([]byte(text)) || !strings.Contains(text, "not_tested") {
		t.Fatal("instance failure lost version evidence")
	}
	code, text, _ = run([]string{"commit", "status", "--json"})
	if code != 2 || !json.Valid([]byte(text)) {
		t.Fatal("missing local HEAD silently chose a remote ref")
	}
	contents, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(contents), "synthetic-cli-token") {
		t.Fatal("environment token was persisted")
	}
	t.Setenv("SOTEA_TOKEN", "")
	t.Setenv("SOTEA_TOKEN_INSTANCE", "")
	publicPath := filepath.Join(directory, "public.json")
	var out, publicDiagnostic bytes.Buffer
	code = Execute(context.Background(), []string{"--config", publicPath, "--workdir", directory, "--instance", server.URL + "/prefix", "--repo", "team/repo", "--confirm-instance", "--json", "repo", "view"}, nil, &out, &publicDiagnostic, "test")
	if code != 0 || !json.Valid(out.Bytes()) {
		t.Fatal("confirmed public read forced account login")
	}
	publicSnapshot, err := (config.Store{Path: publicPath}).Load(context.Background())
	if err != nil || !publicSnapshot.Exists || len(publicSnapshot.Config.Instances[server.URL+"/prefix"].Accounts) != 0 {
		t.Fatal("public read did not save only its confirmed non-sensitive mapping")
	}
}
