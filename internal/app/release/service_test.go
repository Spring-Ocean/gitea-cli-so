package release

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/write"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/compat"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
)

type releaseFixture struct {
	mu           sync.Mutex
	value        *Release
	assets       []Asset
	writes       int
	tagExists    bool
	mode         string
	mutate       string
	cancelUpload chan struct{}
}

// newReleaseFixture 构造隔离认证、配置和 HTTP 服务，避免查询开发者凭证。
// 默认无 Release 或标签，上传故障在接收字节后注入以验证未知及部分完成。
func newReleaseFixture(t *testing.T) (*releaseFixture, Service) {
	t.Helper()
	fixture := &releaseFixture{assets: []Asset{}}
	server := httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(server.Close)
	store := config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	instance := config.Instance{
		URL:           server.URL,
		Proxy:         "direct",
		ActiveAccount: "alice",
		Accounts: map[string]config.Account{"alice": {
			Source:      "environment",
			InstanceURL: server.URL,
		}},
	}
	snapshot.Config.Instances["team"] = instance
	snapshot, err = store.Save(context.Background(), snapshot, snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Service: write.Service{
		Auth: auth.Service{
			Store: store,
			Getenv: func(name string) string {
				if name == "SOTEA_TOKEN" {
					return "synthetic-release-token"
				}
				return ""
			},
		},
		Target: target.Target{
			Name:       "team",
			Instance:   instance,
			Repository: "team/repo",
			Account:    "alice",
			Registered: true,
		},
		Version:  compat.Parse("1.22.6"),
		Revision: snapshot.RevisionKey(),
		Locks:    &write.Locks{},
	}}
	return fixture, service
}

// handle 提供草稿、标签副作用、附件流和已接受后断连的最小服务端行为。
// 同名保护依赖实际附件集合；每次成功写入都可观察，不模拟客户端内部方法。
func (fixture *releaseFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/user" {
		json.NewEncoder(w).Encode(map[string]string{"login": "alice"})
		return
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/team/repo")
	if r.Method == http.MethodGet {
		switch path {
		case "/git/commits/main":
			json.NewEncoder(w).Encode(map[string]string{"sha": strings.Repeat("a", 40)})
		case "/tags/v1":
			if fixture.tagExists {
				json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": strings.Repeat("a", 40)}})
			} else {
				w.WriteHeader(404)
			}
		case "/releases/tags/v1":
			if fixture.value == nil {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(fixture.value)
			}
		case "/releases/10/assets":
			json.NewEncoder(w).Encode(fixture.assets)
		case "/releases/10/assets/1":
			if len(fixture.assets) > 0 {
				json.NewEncoder(w).Encode(fixture.assets[0])
			} else {
				w.WriteHeader(404)
			}
		default:
			w.WriteHeader(404)
		}
		return
	}
	fixture.writes++
	if path == "/releases" {
		var value Release
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			w.WriteHeader(400)
			return
		}
		if fixture.mode == "tag_then_failure" {
			fixture.tagExists = true
			w.WriteHeader(500)
			return
		}
		value.ID = 10
		fixture.value = &value
		if !value.Draft {
			fixture.tagExists = true
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(value)
		return
	}
	if path != "/releases/10/assets" {
		w.WriteHeader(404)
		return
	}
	if fixture.cancelUpload != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			w.WriteHeader(400)
			return
		}
		close(fixture.cancelUpload)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
		return
	}
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		w.WriteHeader(400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("attachment")
	if err != nil {
		w.WriteHeader(400)
		return
	}
	count, readErr := io.Copy(io.Discard, file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		w.WriteHeader(400)
		return
	}
	asset := Asset{
		ID:   1,
		Name: header.Filename,
		Size: count,
	}
	fixture.assets = append(fixture.assets, asset)
	if fixture.mutate != "" {
		if err := os.WriteFile(fixture.mutate, []byte("changed after receipt"), 0o600); err != nil {
			w.WriteHeader(500)
			return
		}
	}
	if fixture.mode == "disconnect" {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			connection.Close()
		}
		return
	}
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(asset)
}

// TestDraftFileChangesAndDuplicateProtection 保护默认草稿、创建目标、文件变化与同名附件边界。
// 明确缺参或已变化输入不得发写请求，上传成功后不会隐式发布或覆盖已有附件。
func TestDraftFileChangesAndDuplicateProtection(t *testing.T) {
	fixture, service := newReleaseFixture(t)
	if _, err := service.Prepare(context.Background(), Request{
		Operation: "release.create",
		Tag:       "v1",
	}); err == nil {
		t.Fatal("new tag without target was accepted")
	}
	plan, err := service.Prepare(context.Background(), Request{
		Operation: "release.create",
		Tag:       "v1",
		Target:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := plan.Preview()
	if err != nil || preview.Request.Draft == nil || !*preview.Request.Draft {
		t.Fatal("release preview did not default to draft")
	}
	*preview.Request.Draft = false
	result, err := service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Success || !result.Items[0].Data.(write.Execution).Resource.(Release).Draft {
		t.Fatal("default draft was lost or preview changed intent")
	}
	path := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(path, []byte("initial"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Operation: "release.asset.upload",
		Tag:       "v1",
		File:      path,
	}
	plan, err = service.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("altered"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = service.Commit(context.Background(), plan)
	if err != nil || result.Items[0].Error.Code != app.PreconditionChanged {
		t.Fatal("modified attachment was sent")
	}
	plan, err = service.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Success {
		t.Fatal("valid attachment failed")
	}
	if _, err := service.Prepare(context.Background(), request); err == nil || app.Failure(err).Code != app.Conflict {
		t.Fatal("same-name attachment was accepted")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.writes != 2 || len(fixture.assets) != 1 || !fixture.value.Draft {
		t.Fatal("rejected file wrote, duplicate replaced an asset, or upload published the draft")
	}
}

// TestUploadUnknownAndPostReceiptChange 保护已接收附件的断连和本地文件变化结果。
// 断连不重放上传，已返回附件但文件变化报告 partial，并保留服务端资源证据。
func TestUploadUnknownAndPostReceiptChange(t *testing.T) {
	for _, mode := range []string{"disconnect", "changed"} {
		t.Run(mode, func(t *testing.T) {
			fixture, service := newReleaseFixture(t)
			fixture.value = &Release{
				ID:    10,
				Tag:   "v1",
				Draft: true,
			}
			path := filepath.Join(t.TempDir(), "asset.bin")
			if err := os.WriteFile(path, []byte("initial payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture.mode = mode
			if mode == "changed" {
				fixture.mutate = path
			}
			plan, err := service.Prepare(context.Background(), Request{
				Operation: "release.asset.upload",
				Tag:       "v1",
				File:      path,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Commit(context.Background(), plan)
			expected := app.Unknown
			if mode == "changed" {
				expected = app.Partial
			}
			if err != nil || result.Status != expected {
				t.Fatalf("accepted upload lost outcome: %s, %v", result.Status, err)
			}
			fixture.mu.Lock()
			count := fixture.writes
			fixture.mu.Unlock()
			if count != 1 {
				t.Fatal("upload was replayed")
			}
		})
	}
}

// TestCancelAfterUploadHeadersRetainsUnknown 保护发送附件请求后取消的明确未知状态。
// 取消必须退出 130，保留未确定上传步骤且不能以重发或回滚伪装取消成功。
func TestCancelAfterUploadHeadersRetainsUnknown(t *testing.T) {
	fixture, service := newReleaseFixture(t)
	fixture.value = &Release{
		ID:    10,
		Tag:   "v1",
		Draft: true,
	}
	fixture.cancelUpload = make(chan struct{})
	path := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	plan, err := service.Prepare(ctx, Request{
		Operation: "release.asset.upload",
		Tag:       "v1",
		File:      path,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan app.Result, 1)
	failures := make(chan error, 1)
	go func() {
		result, err := service.Commit(ctx, plan)
		failures <- err
		done <- result
	}()
	select {
	case <-fixture.cancelUpload:
	case <-ctx.Done():
		t.Fatal("upload did not reach the server")
	}
	cancel()
	result := <-done
	if err := <-failures; err != nil || result.ExitCode() != 130 || result.Items[0].Status != app.Unknown {
		t.Fatal("cancelled upload lost its unknown result or cancellation exit code")
	}
}

// TestTagSideEffectSurvivesReleaseFailure 保护发布过程中先创建标签后失败的实际副作用。
// 结果必须保留标签成功及 Release 失败，不声称完全无写入或自动删除标签。
func TestTagSideEffectSurvivesReleaseFailure(t *testing.T) {
	fixture, service := newReleaseFixture(t)
	fixture.mode = "tag_then_failure"
	draft := false
	plan, err := service.Prepare(context.Background(), Request{
		Operation: "release.create",
		Tag:       "v1",
		Target:    "main",
		Draft:     &draft,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Partial || result.ExitCode() != 3 {
		t.Fatal("tag side effect was hidden by release failure")
	}
	execution := result.Items[0].Data.(write.Execution)
	if len(execution.Steps) != 2 || execution.Steps[0].Target != "tag_created" || execution.Steps[0].Status != app.Success {
		t.Fatal("actual created tag was lost")
	}
}

// TestPublishedReleaseCreateOverHTTP2TLS 保护 HTTPS HTTP/2 实例上的真实准备／创建／读回链路。
// 使用合成账号、中文发布正文和既有标签，发布后只写一次，重复消费计划不能重建 Release。
func TestPublishedReleaseCreateOverHTTP2TLS(t *testing.T) {
	fixture, service := newReleaseFixture(t)
	fixture.tagExists = true
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.handle))
	server.EnableHTTP2 = true
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "https-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Auth.Store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	instance := snapshot.Config.Instances["team"]
	instance.URL = server.URL
	instance.CAPath = ca
	account := instance.Accounts["alice"]
	account.InstanceURL = server.URL
	instance.Accounts["alice"] = account
	snapshot.Config.Instances["team"] = instance
	snapshot, err = service.Auth.Store.Save(context.Background(), snapshot, snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	service.Target.Instance = instance
	service.Revision = snapshot.RevisionKey()
	service.Version = compat.Parse("28.3.0")
	title := "v1"
	body := "# 预发布\n验证 HTTPS 下的 Release 创建。"
	draft, prerelease := false, true
	plan, err := service.Prepare(context.Background(), Request{
		Operation:  "release.create",
		Tag:        "v1",
		Title:      &title,
		Body:       &body,
		Draft:      &draft,
		Prerelease: &prerelease,
		Target:     "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), plan)
	if err != nil || result.Status != app.Success {
		t.Fatalf("HTTPS Release creation failed: result=%+v err=%v", result, err)
	}
	value := result.Items[0].Data.(write.Execution).Resource.(Release)
	if value.ID != 10 || value.Tag != "v1" || value.Body != body || value.Draft || !value.Prerelease {
		t.Fatalf("published Release fields changed: %+v", value)
	}
	if _, err := service.Commit(context.Background(), plan); err == nil {
		t.Fatal("the same Release plan was executed twice")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.writes != 1 {
		t.Fatalf("expected one Release create request, got %d", fixture.writes)
	}
}
