package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// protocolFixture 保存真实 HTTP 写入及故障注入，不依赖开发者凭证。
// HTTP 服务只接受隔离 token；编号类型和业务效果可由测试直接核验。
type protocolFixture struct {
	mu           sync.Mutex
	store        config.Store
	writes       int
	state        string
	disconnected bool
	client       *sdk.Client
}

// newProtocolFixture 建立独立配置、认证和可控 HTTP 服务。
// SDK 连接走实际内存协议消息，关闭顺序先取消连接再释放 Host 资源。
func newProtocolFixture(t *testing.T, options Options, roots ...*sdk.Root) (*protocolFixture, *Host, *sdk.ClientSession) {
	t.Helper()
	fixture := &protocolFixture{state: "open"}
	remote := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(remote.Close)
	fixture.store = config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	snapshot, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Instances["team"] = config.Instance{
		URL:           remote.URL,
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{"alice": {
			InstanceURL: remote.URL,
			Source:      "environment",
		}},
	}
	if _, err := fixture.store.Save(context.Background(), snapshot, snapshot.Config); err != nil {
		t.Fatal(err)
	}
	getenv := func(name string) string {
		if name == "SOTEA_TOKEN" {
			return "synthetic-mcp-token"
		}
		return ""
	}
	environment := &session.Environment{
		Authentication: auth.Service{
			Store:  fixture.store,
			Getenv: getenv,
		},
		Cache: &compat.Cache{},
		Locks: &write.Locks{},
	}
	options.Defaults = target.Input{
		Instance: "team",
		Account:  "alice",
		Repo:     "team/repo",
		Workdir:  options.Defaults.Workdir,
	}
	host, err := NewHost("test", environment, options, output.NewRedactor("synthetic-mcp-token"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := host.Server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{
		Name:    "sotea-contract-tests",
		Version: "1",
	}, nil)
	client.AddRoots(roots...)
	fixture.client = client
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := clientSession.Close(); err != nil {
			t.Error(err)
		}
		if err := serverSession.Close(); err != nil {
			t.Error(err)
		}
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture, host, clientSession
}

// serveHTTP 实现身份、版本、仓库和 Issue 状态链路的最小远端。
// 写请求在业务应用后可主动断连，保护 unknown 与禁止自动重放的契约。
func (f *protocolFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "token synthetic-mcp-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var value any
	switch r.URL.Path {
	case "/api/v1/user":
		value = map[string]any{"login": "alice"}
	case "/api/v1/version":
		value = map[string]any{"version": "28.3.0"}
	case "/api/v1/repos/team/repo":
		value = map[string]any{"id": 9, "full_name": "team/repo", "name": "repo"}
	case "/api/v1/repos/team/repo/labels", "/api/v1/repos/team/repo/assignees", "/api/v1/repos/team/repo/milestones":
		value = []any{}
	case "/api/v1/repos/team/repo/issues/1":
		if r.Method == http.MethodPatch {
			var changes map[string]any
			if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.state = changes["state"].(string)
			f.writes++
			if f.disconnected {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					connection.Close()
				}
				return
			}
		}
		value = map[string]any{"id": 11, "number": 1, "title": "first", "state": f.state, "body": "synthetic-mcp-token", "updated_at": "initial", "labels": []any{}, "assignees": []any{}}
	case "/api/v1/repos/team/repo/issues/2":
		value = map[string]any{"number": 2, "title": "a PR", "state": "open", "pull_request": map[string]any{"merged": false}}
	case "/api/v1/repos/team/repo/issues":
		value = []any{}
	case "/api/v1/binary":
		w.Write([]byte("\xffsynthetic-mcp-token\x00"))
		return
	case "/api/v1/custom":
		if r.Method == http.MethodPost {
			f.writes++
		}
		value = map[string]any{"token": "synthetic-mcp-token", "safe": "visible"}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

// callProtocolTool 调用 SDK 的真实工具消息并核对结构化与文本 JSON 一致。
// 成功和业务失败均须保留完整 envelope，不允许 token 泄漏或纯摘要回退。
func callProtocolTool(t *testing.T, client *sdk.ClientSession, name string, arguments any) response {
	t.Helper()
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "sotea_" + name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s protocol failure: %v", name, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil || bytes.Contains(encoded, []byte("synthetic-mcp-token")) {
		t.Fatalf("%s lost safe structured content: %v", name, err)
	}
	var value response
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	if value.SchemaVersion != 1 || result.IsError != (value.ExitCode() != 0) {
		t.Fatalf("%s error/envelope mismatch: %+v", name, value)
	}
	matched := false
	for _, content := range result.Content {
		text, ok := content.(*sdk.TextContent)
		if !ok || !json.Valid([]byte(text.Text)) {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
			t.Fatal(err)
		}
		compact, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		matched = bytes.Equal(compact, encoded)
	}
	if !matched {
		t.Fatalf("%s lacks an identical JSON text fallback", name)
	}
	return value
}

// prepareClose 为批量状态操作生成真实业务计划，返回可提交元数据。
// 此帮助函数仅用于已授权测试，准备后写请求数应保持零。
func prepareClose(t *testing.T, client *sdk.ClientSession, id string, indices []int64) response {
	t.Helper()
	return callProtocolTool(t, client, "write_prepare", map[string]any{
		"request_id": id, "action": "issue.close",
		"context": map[string]any{}, "params": map[string]any{"indices": indices},
	})
}

// TestCompleteModeDiscoveryAndStrictBranches 保护工具模式隔离与 action 参数校验。
// 未知字段或错配 params 必须在领域执行前拒绝，不能只从工具列表隐藏。
func TestCompleteModeDiscoveryAndStrictBranches(t *testing.T) {
	for _, test := range []struct {
		write, raw, local bool
		count             int
	}{
		{false, false, false, 27}, {true, false, false, 30}, {false, true, false, 28}, {true, true, true, 32},
	} {
		t.Run(fmt.Sprintf("%t-%t-%t", test.write, test.raw, test.local), func(t *testing.T) {
			options := Options{
				AllowWrite:  test.write,
				AllowRawAPI: test.raw,
			}
			if test.local {
				options.Defaults.Workdir = t.TempDir()
			}
			fixture, _, client := newProtocolFixture(t, options)
			listed, err := client.ListTools(context.Background(), nil)
			if err != nil || len(listed.Tools) != test.count {
				t.Fatalf("mode tools: %v, count=%d", err, len(listed.Tools))
			}
			result, err := client.CallTool(context.Background(), &sdk.CallToolParams{
				Name:      "sotea_issue_list",
				Arguments: map[string]any{"all": true},
			})
			if err == nil && !result.IsError {
				t.Fatal("unknown argument accepted")
			}
			if test.write {
				result, err = client.CallTool(context.Background(), &sdk.CallToolParams{
					Name: "sotea_write_prepare",
					Arguments: map[string]any{
						"request_id": "bad", "action": "issue.close", "context": map[string]any{}, "params": map[string]any{"title": "wrong"},
					},
				})
				if err == nil && !result.IsError {
					t.Fatal("action/params mismatch reached application")
				}
				result, err = client.CallTool(context.Background(), &sdk.CallToolParams{
					Name:      "sotea_write_status",
					Arguments: map[string]any{"request_id": "a", "plan_id": "b"},
				})
				if err == nil && !result.IsError {
					t.Fatal("status accepted two identifiers")
				}
			} else {
				result, err = client.CallTool(context.Background(), &sdk.CallToolParams{
					Name:      "sotea_write_prepare",
					Arguments: map[string]any{},
				})
				if err == nil && !result.IsError {
					t.Fatal("hidden write tool callable")
				}
			}
			fixture.mu.Lock()
			writes := fixture.writes
			fixture.mu.Unlock()
			if writes != 0 {
				t.Fatal("invalid calls wrote remotely")
			}
		})
	}
}

// TestConcurrentCommitRetainsOneOutcome 保护并发提交最多一次与完成后重复查询。
// token 错误不消费，批量预检失败保留原目标，已完成结果不因准备期限失效。
func TestConcurrentCommitRetainsOneOutcome(t *testing.T) {
	fixture, host, client := newProtocolFixture(t, Options{AllowWrite: true})
	prepared := prepareClose(t, client, "close-once", []int64{1, 2})
	if prepared.Status != app.Partial || prepared.Meta.Plan == nil || !prepared.Meta.Plan.Committable {
		t.Fatalf("partial preparation missing: %+v", prepared)
	}
	plan := prepared.Meta.Plan
	repeat := prepareClose(t, client, "close-once", []int64{1, 2})
	if repeat.Meta.Plan.PlanID != plan.PlanID || repeat.Meta.Plan.Token != plan.Token {
		t.Fatal("prepare generated another plan")
	}
	rejected := callProtocolTool(t, client, "write_commit", map[string]any{"plan_id": plan.PlanID, "confirmation_token": "wrong"})
	if rejected.Error == nil || rejected.Error.Code != app.PermissionDenied {
		t.Fatal("wrong token consumed or executed plan")
	}
	var wait sync.WaitGroup
	results := make(chan response, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- callProtocolTool(t, client, "write_commit", map[string]any{"plan_id": plan.PlanID, "confirmation_token": plan.Token})
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.Meta == nil || result.Meta.Plan == nil {
			t.Fatal("repeat lost execution state")
		}
	}
	final := callProtocolTool(t, client, "write_status", map[string]any{"request_id": "close-once"})
	if final.Meta.Plan.State != "completed" || final.Meta.Plan.Token != "" || final.Status != app.Partial || len(final.Items) != 2 {
		t.Fatalf("outcome missing target failures: %+v", final)
	}
	host.plans.mu.Lock()
	host.plans.now = func() time.Time {
		return time.Now().Add(time.Hour)
	}
	host.plans.mu.Unlock()
	repeated := callProtocolTool(t, client, "write_commit", map[string]any{"plan_id": plan.PlanID, "confirmation_token": plan.Token})
	if repeated.Meta.Plan.State != "completed" {
		t.Fatal("completed result incorrectly expired")
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 1 {
		t.Fatalf("write replayed %d times", writes)
	}
}

// TestPlanExpiryIdentityAndIntentConflict 保护过期、身份修订与 request_id 绑定。
// 状态失败不泄露原预览／token，任何失效路径都不能产生远端写入。
func TestPlanExpiryIdentityAndIntentConflict(t *testing.T) {
	fixture, host, client := newProtocolFixture(t, Options{AllowWrite: true})
	prepared := prepareClose(t, client, "expires", []int64{1})
	plan := prepared.Meta.Plan
	conflict := prepareClose(t, client, "expires", []int64{2})
	if conflict.Error == nil || conflict.Error.Code != app.Conflict {
		t.Fatal("different intent reused request ID")
	}
	host.plans.mu.Lock()
	host.plans.now = func() time.Time {
		return time.Now().Add(time.Hour)
	}
	host.plans.mu.Unlock()
	expired := callProtocolTool(t, client, "write_commit", map[string]any{"plan_id": plan.PlanID, "confirmation_token": plan.Token})
	if expired.Error == nil || expired.Error.Code != app.PlanExpired {
		t.Fatalf("expiry failed: %+v", expired)
	}
	host.plans.mu.Lock()
	host.plans.now = time.Now
	host.plans.mu.Unlock()
	other := prepareClose(t, client, "identity", []int64{1})
	snapshot, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.DefaultInstance = "team"
	if _, err := fixture.store.Save(context.Background(), snapshot, snapshot.Config); err != nil {
		t.Fatal(err)
	}
	restricted := callProtocolTool(t, client, "write_status", map[string]any{"plan_id": other.Meta.Plan.PlanID})
	if restricted.Error == nil || restricted.Error.Code != app.PreconditionChanged || len(restricted.Items) != 0 || restricted.Meta.Plan.Token != "" {
		t.Fatalf("identity change exposed old data: %+v", restricted)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 0 {
		t.Fatal("expired/invalidated operation wrote remotely")
	}
}

// TestUnknownWriteAndRawRequestRemainDeduplicated 保护发送后断连与原始 API 边界。
// 服务端已应用但无响应时保留 unknown，重复提交不得再次发送。
func TestUnknownWriteAndRawRequestRemainDeduplicated(t *testing.T) {
	fixture, _, client := newProtocolFixture(t, Options{
		AllowWrite:  true,
		AllowRawAPI: true,
	})
	prepared := prepareClose(t, client, "unknown", []int64{1})
	fixture.mu.Lock()
	fixture.disconnected = true
	fixture.mu.Unlock()
	args := map[string]any{"plan_id": prepared.Meta.Plan.PlanID, "confirmation_token": prepared.Meta.Plan.Token}
	first := callProtocolTool(t, client, "write_commit", args)
	if first.Status != app.Unknown {
		t.Fatalf("sent disconnect not unknown: %+v", first)
	}
	second := callProtocolTool(t, client, "write_commit", args)
	if second.Status != app.Unknown {
		t.Fatal("repeat discarded unknown result")
	}
	raw := callProtocolTool(t, client, "write_prepare", map[string]any{"request_id": "raw", "action": "api.request", "context": map[string]any{}, "params": map[string]any{"method": "POST", "path": "/custom", "body_text": "{}"}})
	if raw.Meta == nil || raw.Meta.Plan == nil || !raw.Meta.Plan.Committable {
		t.Fatalf("raw prepare failed: %+v", raw)
	}
	sent := callProtocolTool(t, client, "write_commit", map[string]any{"plan_id": raw.Meta.Plan.PlanID, "confirmation_token": raw.Meta.Plan.Token})
	if sent.Status != app.Success {
		t.Fatalf("raw write failed: %+v", sent)
	}
	for _, headers := range []map[string][]string{{"Authorization": {"another"}}, {"sudo": {"other"}}, {"Content-Length": {"99"}}} {
		denied := callProtocolTool(t, client, "write_prepare", map[string]any{"request_id": fmt.Sprint(headers), "action": "api.request", "context": map[string]any{}, "params": map[string]any{"method": "POST", "path": "/custom", "headers": headers}})
		if denied.Error == nil || denied.Error.Code != app.InvalidArgument {
			t.Fatal("raw identity/framing override allowed")
		}
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 2 {
		t.Fatalf("unknown request replayed: %d writes", writes)
	}
}

// TestPreviewBudgetAndRegistryLimitRejectBeforeWrite 保护确认预览与终态配额。
// 超限不得发放可提交 token，也不能忘记 request_id 以允许重复写入。
func TestPreviewBudgetAndRegistryLimitRejectBeforeWrite(t *testing.T) {
	fixture, host, client := newProtocolFixture(t, Options{
		AllowWrite:  true,
		AllowRawAPI: true,
		MaxOutput:   4096,
	})
	tooLarge := callProtocolTool(t, client, "write_prepare", map[string]any{"request_id": "large", "action": "api.request", "context": map[string]any{}, "params": map[string]any{"method": "POST", "path": "/custom", "body_text": strings.Repeat("x", 10000)}})
	if tooLarge.Error == nil || tooLarge.Error.Code != app.ResponseTooLarge || (tooLarge.Meta != nil && tooLarge.Meta.Plan.Token != "") {
		t.Fatal("oversized preview issued commit authority")
	}
	host.plans.mu.Lock()
	host.plans.maxRecords = 1
	host.plans.mu.Unlock()
	full := prepareClose(t, client, "full", []int64{1})
	if full.Error == nil || full.Error.Code != app.ResourceLimit {
		t.Fatal("registry full accepted new intent")
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 0 {
		t.Fatal("resource failure executed request")
	}
}

// TestBinaryPreviewRedactsBeforeBase64 保护非 UTF8 原始响应不会通过编码绕过已知凭证脱敏。
// 不要求丢弃二进制前缀，但文本和结构化结果中的 base64 解码后均不能包含密钥。
func TestBinaryPreviewRedactsBeforeBase64(t *testing.T) {
	_, _, client := newProtocolFixture(t, Options{AllowRawAPI: true})
	result := callProtocolTool(t, client, "api_read", map[string]any{"method": "GET", "path": "/binary"})
	value := result.Items[0].Data.(map[string]any)
	if value["body_encoding"] != "base64" {
		t.Fatal("binary preview lost its encoding")
	}
	decoded, err := base64.StdEncoding.DecodeString(value["body"].(string))
	if err != nil || bytes.Contains(decoded, []byte("synthetic-mcp-token")) {
		t.Fatal("binary preview exposed an encoded credential")
	}
}

// TestRawFileSealAndScopeFailBeforeSending 保护原始 API 文件变化与根外路径的拒绝边界。
// 准备后替换或原地改写文件不能被 commit 当作新正文，失败不能触发业务请求。
func TestRawFileSealAndScopeFailBeforeSending(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "body.json")
	if err := os.WriteFile(path, []byte(`{"value":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, _, client := newProtocolFixture(t, Options{
		AllowWrite:  true,
		AllowRawAPI: true,
		Defaults:    target.Input{Workdir: root},
	})
	prepared := callProtocolTool(t, client, "write_prepare", map[string]any{
		"request_id": "file", "action": "api.request", "context": map[string]any{},
		"params": map[string]any{"method": "POST", "path": "/custom", "body_file": "body.json"},
	})
	if err := os.WriteFile(path, []byte(`{"value":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	result := callProtocolTool(t, client, "write_commit", map[string]any{
		"plan_id": prepared.Meta.Plan.PlanID, "confirmation_token": prepared.Meta.Plan.Token,
	})
	if result.Error == nil || result.Error.Code != app.PreconditionChanged || result.Meta.Plan.State != "invalidated" {
		t.Fatalf("changed API file was accepted: %+v", result)
	}
	escaped := callProtocolTool(t, client, "write_prepare", map[string]any{
		"request_id": "outside", "action": "api.request", "context": map[string]any{},
		"params": map[string]any{"method": "POST", "path": "/custom", "body_file": "../outside.json"},
	})
	if escaped.Error == nil || escaped.Error.Code != app.PathOutsideRoot {
		t.Fatal("API file escaped the startup scope")
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 0 {
		t.Fatal("file precondition failure sent a write")
	}
}

// assertSameDirectory 按目录实体核对路径，不把平台分隔符差异视为不同仓库。
// 两条路径都必须可访问且为目录；真实跨根结果仍失败，并报告原始路径便于诊断。
func assertSameDirectory(t *testing.T, actual, expected string) {
	t.Helper()
	actualInfo, err := os.Stat(actual)
	if err != nil {
		t.Fatalf("read returned directory %q: %v", actual, err)
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("read expected directory %q: %v", expected, err)
	}
	if !actualInfo.IsDir() || !expectedInfo.IsDir() || !os.SameFile(actualInfo, expectedInfo) {
		t.Fatalf("returned directory %q does not identify expected directory %q", actual, expected)
	}
}

// TestGitContextUsesFixedReadQueries 保护授权本地仓库读取和禁用 fsmonitor 的契约。
// 使用真实临时目录实体核对根，避免将平台路径分隔符差异误判为越界。
func TestGitContextUsesFixedReadQueries(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "-C", root, "init", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated Git repository: %v %s", err, output)
	}
	marker := filepath.Join(root, "unexpected-monitor")
	command = exec.Command("git", "-C", root, "config", "core.fsmonitor", "touch "+marker)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("configure isolated Git monitor: %v %s", err, output)
	}
	_, _, client := newProtocolFixture(t, Options{Defaults: target.Input{Workdir: root}})
	result := callProtocolTool(t, client, "git_context", nil)
	if result.ExitCode() != 0 || len(result.Items) != 1 {
		t.Fatalf("fixed Git query failed: %+v", result)
	}
	data := result.Items[0].Data.(map[string]any)
	actual, ok := data["root"].(string)
	if !ok {
		t.Fatalf("fixed Git query did not return a root path: %+v", data)
	}
	assertSameDirectory(t, actual, root)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git context executed a configured fsmonitor")
	}
}

// TestShutdownCancelsWaitersAndKeepsStatusAvailable 保护停止时的请求取消与独立状态通道。
// 人工占满普通槽不会阻断 status，Close 会取消等待而不是等待默认三十秒超时。
func TestShutdownCancelsWaitersAndKeepsStatusAvailable(t *testing.T) {
	_, host, client := newProtocolFixture(t, Options{AllowWrite: true})
	for i := 0; i < cap(host.reads); i++ {
		host.reads <- struct{}{}
	}
	status := callProtocolTool(t, client, "write_status", map[string]any{"request_id": "missing"})
	if status.Error == nil || status.Error.Code != app.NotFound {
		t.Fatal("status was blocked by saturated read slots")
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "sotea_issue_list",
			Arguments: map[string]any{},
		})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(host.queued) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(host.queued) == 0 {
		t.Fatal("the read did not enter its bounded queue")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel a waiting request")
	}
}
