package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
)

type memoryVault struct {
	mu         sync.Mutex
	values     map[string]string
	failDelete bool
	failSet    bool
}

// Get 读取隔离内存引用，不触碰真实用户密钥库。
// 缺失使用与系统适配器相同的认证错误类别。
func (v *memoryVault) Get(ctx context.Context, ref string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	value := v.values[ref]
	if value == "" {
		return "", app.Wrap(app.AuthRequired, "Missing synthetic credential.", nil)
	}
	return value, nil
}

// Set 保存合成密钥或注入明确存储故障。
// 测试通过配置文件内容检查密钥未进入持久化引用。
func (v *memoryVault) Set(ctx context.Context, ref, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failSet {
		return app.Wrap(app.IOError, "Synthetic credential storage failed.", nil)
	}
	v.values[ref] = value
	return nil
}

// Delete 模拟只删除一个独立引用及其故障边界。
// 缺失引用视为已删除，符合幂等清理契约。
func (v *memoryVault) Delete(ctx context.Context, ref string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failDelete {
		return app.Wrap(app.IOError, "Synthetic credential removal failed.", nil)
	}
	delete(v.values, ref)
	return nil
}

// TestIdentityLifecycleAndFailureIsolation 保护身份核对、刷新保留、401／403 和跨进程退出。
// 真实 HTTP 使用合成 token，系统存储通过最小内存适配器隔离。
func TestIdentityLifecycleAndFailureIsolation(t *testing.T) {
	var repositoryCalls atomic.Int32
	var invalid atomic.Bool
	var forbidden atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			if forbidden.Load() {
				w.WriteHeader(403)
				return
			}
			if invalid.Load() || r.Header.Get("Authorization") == "token bad" {
				w.WriteHeader(401)
				return
			}
			login := "alice"
			if r.Header.Get("Authorization") == "token synthetic-bob" {
				login = "bob"
			}
			json.NewEncoder(w).Encode(map[string]string{"login": login})
			return
		}
		repositoryCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"full_name": "team/repo"})
	}))
	defer server.Close()
	ctx := context.Background()
	vault := &memoryVault{values: map[string]string{}}
	service := Service{
		Store: config.Store{Path: filepath.Join(t.TempDir(), "config.json")},
		Vault: vault,
		Getenv: func(string) string {
			return ""
		},
	}
	snapshot, err := service.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetValue := target.Target{
		Name: "team",
		Instance: config.Instance{
			URL:   server.URL,
			Proxy: "direct",
		},
		Repository: "team/repo",
		Registered: true,
	}
	if _, err := service.Login(ctx, targetValue, snapshot, LoginOptions{
		Source: "token",
		Secret: "synthetic-alice",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetValue.Account = "alice"
	targetValue.Instance = snapshot.Config.Instances["team"]
	oldRef := targetValue.Instance.Accounts["alice"].CredentialRef
	contents, err := os.ReadFile(service.Store.Path)
	if err != nil || strings.Contains(string(contents), "synthetic-alice") {
		t.Fatal("token reached configuration")
	}
	if _, err := service.Login(ctx, targetValue, snapshot, LoginOptions{
		Source: "token",
		Secret: "bad",
	}); err == nil {
		t.Fatal("invalid refresh succeeded")
	}
	current, err := service.Store.Load(ctx)
	if err != nil || current.Config.Instances["team"].Accounts["alice"].CredentialRef != oldRef ||
		current.Config.Instances["team"].Accounts["alice"].Invalid || vault.values[oldRef] != "synthetic-alice" {
		t.Fatal("failed refresh replaced a valid credential reference")
	}
	targetValue.Instance = current.Config.Instances["team"]
	before := repositoryCalls.Load()
	if _, err := service.Login(ctx, targetValue, current, LoginOptions{
		Source: "token",
		Secret: "synthetic-bob",
	}); err == nil || app.Failure(err).Code != app.IdentityMismatch || repositoryCalls.Load() != before {
		t.Fatal("identity mismatch allowed repository access")
	}
	forbidden.Store(true)
	material, err := service.Credentials(ctx, targetValue, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(ctx, targetValue, material); err == nil || app.Failure(err).Code != app.PermissionDenied {
		t.Fatal("403 was not preserved")
	}
	current, _ = service.Store.Load(ctx)
	if current.Config.Instances["team"].Accounts["alice"].Invalid != targetValue.Instance.Accounts["alice"].Invalid {
		t.Fatal("403 changed credential validity")
	}
	forbidden.Store(false)
	invalid.Store(true)
	if _, err := service.Verify(ctx, targetValue, material); err == nil || app.Failure(err).Code != app.AuthRequired {
		t.Fatal("401 was not classified as authentication failure")
	}
	current, err = service.Store.Load(ctx)
	if err != nil || !current.Config.Instances["team"].Accounts["alice"].Invalid {
		t.Fatal("401 did not persist the invalid state")
	}
	invalid.Store(false)
	targetValue.Instance = current.Config.Instances["team"]
	if _, err := service.Login(ctx, targetValue, current, LoginOptions{
		Source: "token",
		Secret: "synthetic-alice-new",
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := vault.values[oldRef]; exists {
		t.Fatal("successful refresh left the old credential")
	}
	current, err = service.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetValue.Instance = current.Config.Instances["team"]
	vault.failDelete = true
	if _, err := service.Logout(ctx, targetValue, current); err == nil || app.Failure(err).Code != app.PartialFailure {
		t.Fatal("failed deletion did not report partial cleanup")
	}
	current, err = service.Store.Load(ctx)
	if err != nil || !current.Config.Instances["team"].LoggedOut["alice"] || current.Config.Instances["team"].ActiveAccount != "" {
		t.Fatal("logout cleanup failure did not persist suppression")
	}
	targetValue.Instance = current.Config.Instances["team"]
	targetValue.Account = ""
	targetValue.Credential = git.Credential{
		Protocol: "https",
		Host:     "unreachable.invalid",
	}
	if material, err := service.Credentials(ctx, targetValue, true); err != nil || material != nil {
		t.Fatal("new-process anonymous read tried to sign in after logout")
	}
	targetValue.Account = "alice"
	vault.failDelete = false
	if _, err := service.Logout(ctx, targetValue, current); err != nil {
		t.Fatal(err)
	}
}

// TestCredentialSourceScopeAndStorageFailure 保护环境实例限定和持久化失败的完整性。
// 来源未确认时不会读密钥，存储失败不能留下成功配置或泄露输入。
func TestCredentialSourceScopeAndStorageFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"login": "alice"})
	}))
	defer server.Close()
	ctx := context.Background()
	vault := &memoryVault{
		values:  map[string]string{},
		failSet: true,
	}
	environment := map[string]string{"SOTEA_TOKEN": "synthetic-environment", "SOTEA_TOKEN_INSTANCE": "other"}
	service := Service{
		Store: config.Store{Path: filepath.Join(t.TempDir(), "config.json")},
		Vault: vault,
		Getenv: func(name string) string {
			return environment[name]
		},
	}
	targetValue := target.Target{
		Name: "team",
		Instance: config.Instance{
			URL:   server.URL,
			Proxy: "direct",
		},
		Registered: true,
	}
	if _, err := service.Credentials(ctx, targetValue, false); err == nil || app.Failure(err).Code != app.IdentityMismatch || calls.Load() != 0 {
		t.Fatal("environment token crossed an instance scope")
	}
	targetValue.Registered = false
	if _, err := service.Credentials(ctx, targetValue, false); err == nil || calls.Load() != 0 {
		t.Fatal("unknown instance accessed credentials")
	}
	targetValue.Registered = true
	environment["SOTEA_TOKEN_INSTANCE"] = "team"
	targetValue.Instance.LoggedOut = map[string]bool{"alice": true}
	if material, err := service.Credentials(ctx, targetValue, false); err != nil || material.Source != "environment" {
		t.Fatal("externally supplied token was silently removed by logout")
	}
	snapshot, err := service.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, targetValue, snapshot, LoginOptions{
		Source: "token",
		Secret: "synthetic-independent",
	}); err == nil {
		t.Fatal("system storage failure was ignored")
	}
	if after, err := service.Store.Load(ctx); err != nil || after.Exists {
		t.Fatal("storage failure left a successful configuration")
	}
}
