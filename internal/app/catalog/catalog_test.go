package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	"github.com/Spring-Ocean/gitea-cli-so/internal/config"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/git"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

type catalogFixture struct {
	server         *httptest.Server
	authentication auth.Service
	target         target.Target
}

// newCatalogFixture 创建只接受合成测试 token 的隔离实例服务。
// 请求经真实 HTTP 客户端和身份核验，测试不会读取开发者凭证或联系远端。
func newCatalogFixture(t *testing.T, handler http.HandlerFunc) catalogFixture {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	authentication := auth.Service{
		HTTPOptions: gitea.Options{
			Timeout: 2 * time.Second,
		},
		Getenv: func(name string) string {
			switch name {
			case "SOTEA_TOKEN":
				return "synthetic-catalog-test-token"
			case "SOTEA_TOKEN_INSTANCE":
				return server.URL
			default:
				return ""
			}
		},
	}
	workdir := t.TempDir()
	return catalogFixture{
		server:         server,
		authentication: authentication,
		target: target.Target{
			Name:       "test-instance",
			Instance:   config.Instance{URL: server.URL},
			Repository: "local/checkout",
			Account:    "alice",
			Workdir:    workdir,
			Remote:     "origin",
			Sources: map[string]string{
				"repo":   "remote",
				"remote": "unique_remote",
			},
			Registered:   true,
			Local:        &git.Repository{Root: workdir},
			Credential:   git.Credential{Protocol: "https", Host: "gitea.example.test"},
			LocalMatches: true,
		},
	}
}

// requireTestToken 确认只有认证边界给出的合成 token 到达测试服务。
// 任何缺失或来源错误都立即终止该请求处理，避免掩盖认证回归。
func requireTestToken(t *testing.T, writer http.ResponseWriter, request *http.Request) bool {
	t.Helper()
	if request.Header.Get("Authorization") != "token synthetic-catalog-test-token" {
		t.Errorf("unexpected authorization header: %q", request.Header.Get("Authorization"))
		http.Error(writer, "unauthorized test request", http.StatusUnauthorized)
		return false
	}
	return true
}

// writeTestJSON 返回指定 JSON 和状态码，避免测试夹具绕过真实响应解码。
// 调用者仍可设置 Link 等响应头，以验证分页契约。
func writeTestJSON(t *testing.T, writer http.ResponseWriter, status int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

// TestListReadsBoundedRepositoryPages 验证分页参数、Link 证据和导航字段映射。
// 账号资料先由 /user 核验，随后目录请求必须使用同一实例的认证客户端。
func TestListReadsBoundedRepositoryPages(t *testing.T) {
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if !requireTestToken(t, writer, request) {
			return
		}
		switch request.URL.Path {
		case "/api/v1/user":
			writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
		case "/api/v1/user/repos":
			if request.URL.Query().Get("page") != "2" || request.URL.Query().Get("limit") != "2" {
				t.Errorf("unexpected pagination query: %s", request.URL.RawQuery)
			}
			writer.Header().Set("Link", "<https://example.test/api/v1/user/repos?page=3>; rel=\"next\"")
			writeTestJSON(t, writer, http.StatusOK, []map[string]any{
				{
					"id": 101, "name": "web", "full_name": "alice/web", "description": "web app",
					"visibility": "private", "private": true, "owner": map[string]string{"login": "alice"},
				},
				{
					"id": 102, "name": "cli", "full_name": "alice/cli", "owner": map[string]string{"login": "alice"},
				},
			})
		default:
			t.Errorf("unexpected request path: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	})

	page, verified, err := List(context.Background(), fixture.authentication, fixture.target, Query{
		Kind:  Repositories,
		Page:  2,
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if verified.Account != "alice" || page.Page != 2 || page.Limit != 2 || !page.HasMore {
		t.Fatalf("unexpected page or verified identity: page=%+v account=%q", page, verified.Account)
	}
	if len(page.Items) != 2 || page.Items[0].ID != 101 || page.Items[0].FullName != "alice/web" ||
		!page.Items[0].Private || page.Items[0].OwnerLogin != "alice" {
		t.Fatalf("unexpected repository mapping: %+v", page.Items)
	}
}

// TestListVerifiesOrganizationBeforeReadingRepositories prevents stale organization navigation.
// The stable organization ID and name are checked before the organization repository endpoint is called.
func TestListVerifiesOrganizationBeforeReadingRepositories(t *testing.T) {
	var repositoryListRequested bool
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if !requireTestToken(t, writer, request) {
			return
		}
		switch request.URL.Path {
		case "/api/v1/user":
			writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
		case "/api/v1/orgs/team":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{"id": 42, "username": "team", "full_name": "Team"})
		case "/api/v1/orgs/team/repos":
			repositoryListRequested = true
			if request.URL.Query().Get("page") != "1" || request.URL.Query().Get("limit") != "30" {
				t.Errorf("unexpected pagination query: %s", request.URL.RawQuery)
			}
			writeTestJSON(t, writer, http.StatusOK, []map[string]any{{
				"id": 201, "name": "api", "full_name": "team/api", "owner": map[string]string{"login": "team"},
			}})
		default:
			t.Errorf("unexpected request path: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	})

	page, _, err := List(context.Background(), fixture.authentication, fixture.target, Query{
		Kind:           OrganizationRepositories,
		Organization:   "team",
		OrganizationID: 42,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !repositoryListRequested || len(page.Items) != 1 || page.Items[0].FullName != "team/api" {
		t.Fatalf("organization repository page was not loaded correctly: %+v", page)
	}
}

// TestListRejectsDuplicateIDsAndOversizedPages protects stable interactive selection keys.
// Both directory kinds must reject server pages that could collapse or exceed the requested selection set.
func TestListRejectsDuplicateIDsAndOversizedPages(t *testing.T) {
	tests := []struct {
		name     string
		kind     Kind
		path     string
		body     string
		limit    int
		wantCode app.Code
	}{
		{
			name:     "repository duplicate IDs",
			kind:     Repositories,
			path:     "/api/v1/user/repos",
			body:     `[{"id":1,"name":"one","full_name":"alice/one"},{"id":1,"name":"two","full_name":"alice/two"}]`,
			limit:    2,
			wantCode: app.ServerError,
		},
		{
			name:     "organization duplicate IDs",
			kind:     Organizations,
			path:     "/api/v1/user/orgs",
			body:     `[{"id":2,"username":"one"},{"id":2,"username":"two"}]`,
			limit:    2,
			wantCode: app.ServerError,
		},
		{
			name:     "repository page exceeds limit",
			kind:     Repositories,
			path:     "/api/v1/user/repos",
			body:     `[{"id":1,"full_name":"alice/one"},{"id":2,"full_name":"alice/two"}]`,
			limit:    1,
			wantCode: app.ResponseTooLarge,
		},
		{
			name:     "organization page exceeds limit",
			kind:     Organizations,
			path:     "/api/v1/user/orgs",
			body:     `[{"id":1,"username":"one"},{"id":2,"username":"two"}]`,
			limit:    1,
			wantCode: app.ResponseTooLarge,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if !requireTestToken(t, writer, request) {
					return
				}
				if request.URL.Path == "/api/v1/user" {
					writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
					return
				}
				if request.URL.Path != test.path {
					t.Errorf("unexpected request path: %s", request.URL.Path)
					http.NotFound(writer, request)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				if _, err := writer.Write([]byte(test.body)); err != nil {
					t.Errorf("write test response: %v", err)
				}
			})
			_, _, err := List(context.Background(), fixture.authentication, fixture.target, Query{
				Kind:  test.kind,
				Limit: test.limit,
			})
			if err == nil || app.Failure(err).Code != test.wantCode {
				t.Fatalf("List() error = %v, want code %s", err, test.wantCode)
			}
		})
	}
}

// TestListStopsBeforeDirectoryRequestOnIdentityOrPermissionFailure verifies the authentication boundary.
// A mismatched /user identity blocks catalog access, and HTTP 403 remains an explicit permission error.
func TestListStopsBeforeDirectoryRequestOnIdentityOrPermissionFailure(t *testing.T) {
	t.Run("identity mismatch", func(t *testing.T) {
		var directoryRequested bool
		fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
			if !requireTestToken(t, writer, request) {
				return
			}
			if request.URL.Path == "/api/v1/user" {
				writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "mallory"})
				return
			}
			directoryRequested = true
			http.NotFound(writer, request)
		})
		_, _, err := List(context.Background(), fixture.authentication, fixture.target, Query{Kind: Repositories})
		if err == nil || app.Failure(err).Code != app.IdentityMismatch || directoryRequested {
			t.Fatalf("List() error = %v, directoryRequested = %t", err, directoryRequested)
		}
	})

	t.Run("permission denied", func(t *testing.T) {
		fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
			if !requireTestToken(t, writer, request) {
				return
			}
			if request.URL.Path == "/api/v1/user" {
				writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
				return
			}
			http.Error(writer, "denied", http.StatusForbidden)
		})
		_, _, err := List(context.Background(), fixture.authentication, fixture.target, Query{Kind: Repositories})
		failure := app.Failure(err)
		if failure.Code != app.PermissionDenied || failure.HTTPStatus != http.StatusForbidden {
			t.Fatalf("List() error = %+v, want permission denied with 403", failure)
		}
	})
}

// TestListRejectsUnconfirmedInstanceBeforeSendingCredentials keeps source confirmation mandatory.
// The request counter proves a registered account is required before token material reaches HTTP.
func TestListRejectsUnconfirmedInstanceBeforeSendingCredentials(t *testing.T) {
	var requestCount int
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		http.NotFound(writer, request)
	})
	fixture.target.Registered = false
	_, _, err := List(context.Background(), fixture.authentication, fixture.target, Query{Kind: Repositories})
	if err == nil || app.Failure(err).Code != app.AuthRequired || requestCount != 0 {
		t.Fatalf("List() error = %v, requestCount = %d", err, requestCount)
	}
}

// TestGetRepositoryChecksIdentityAndClearsLocalContext validates account navigation into a remote repository.
// Successful entry clears checkout-specific state on a copied target while preserving the caller's source map.
func TestGetRepositoryChecksIdentityAndClearsLocalContext(t *testing.T) {
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if !requireTestToken(t, writer, request) {
			return
		}
		switch request.URL.Path {
		case "/api/v1/user":
			writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
		case "/api/v1/repos/team/api":
			writeTestJSON(t, writer, http.StatusOK, map[string]any{
				"id": 501, "name": "api", "full_name": "team/api", "description": "service",
				"visibility": "public", "owner": map[string]string{"login": "team"},
			})
		default:
			t.Errorf("unexpected request path: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	})
	originalSources := fixture.target.Sources

	repository, entered, err := GetRepository(context.Background(), fixture.authentication, fixture.target, 501, "team/api")
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if repository.ID != 501 || repository.FullName != "team/api" || entered.Repository != "team/api" {
		t.Fatalf("unexpected repository entry: repository=%+v target=%+v", repository, entered)
	}
	if entered.Local != nil || entered.LocalMatches || entered.Remote != "" || entered.Credential.Protocol != "" {
		t.Fatalf("repository entry retained local Git context: %+v", entered)
	}
	if entered.Sources["repo"] != "account_catalog" || entered.Sources["remote"] != "unselected" {
		t.Fatalf("unexpected entry sources: %+v", entered.Sources)
	}
	if originalSources["repo"] != "remote" || originalSources["remote"] != "unique_remote" {
		t.Fatalf("GetRepository mutated the caller's source map: %+v", originalSources)
	}
}

// TestGetRepositoryRejectsChangedStableID prevents stale menu choices from opening a replacement resource.
// The response must match both the selected ID and owner/repository path before entry is allowed.
func TestGetRepositoryRejectsChangedStableID(t *testing.T) {
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if !requireTestToken(t, writer, request) {
			return
		}
		if request.URL.Path == "/api/v1/user" {
			writeTestJSON(t, writer, http.StatusOK, map[string]string{"login": "alice"})
			return
		}
		writeTestJSON(t, writer, http.StatusOK, map[string]any{
			"id": 502, "name": "api", "full_name": "team/api", "owner": map[string]string{"login": "team"},
		})
	})
	_, _, err := GetRepository(context.Background(), fixture.authentication, fixture.target, 501, "team/api")
	if err == nil || app.Failure(err).Code != app.PreconditionChanged {
		t.Fatalf("GetRepository() error = %v, want changed precondition", err)
	}
}

// TestListCancellationStopsHTTP verifies caller cancellation reaches the in-flight authenticated request.
// The handler blocks until the request context is cancelled, then the service returns CANCELLED.
func TestListCancellationStopsHTTP(t *testing.T) {
	requestStarted := make(chan struct{})
	fixture := newCatalogFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if !requireTestToken(t, writer, request) {
			return
		}
		if request.URL.Path == "/api/v1/user" {
			close(requestStarted)
			<-request.Context().Done()
			return
		}
		http.NotFound(writer, request)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := List(ctx, fixture.authentication, fixture.target, Query{Kind: Repositories})
		done <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("identity request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || (!errors.Is(err, context.Canceled) && app.Failure(err).Code != app.Cancelled) {
			t.Fatalf("List() error = %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("List() did not stop after cancellation")
	}
}
