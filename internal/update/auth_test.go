package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestUpdateAuthenticationBoundary 保护认证范围、匿名模式和401单次回退。
// 限流必须保留恢复信息且不重试，附件与不相干API不得获取凭证。
func TestUpdateAuthenticationBoundary(t *testing.T) {
	for _, sample := range []struct {
		name, address, mode string
		status, attempts    int
		auth                bool
	}{
		{"api", "https://api.github.com/repos/" + Repository + "/releases", "auto", 200, 1, true},
		{"anonymous", "https://api.github.com/repos/" + Repository + "/releases", "anonymous", 200, 1, false},
		{"rejected", "https://api.github.com/repos/" + Repository + "/releases", "auto", 401, 2, true},
		{"rate", "https://api.github.com/repos/" + Repository + "/releases", "auto", 403, 1, true},
		{"asset", "https://github.com/" + Repository + "/releases/download/v1.0.0/a", "auto", 200, 1, false},
		{"other", "https://api.github.com/user", "auto", 200, 1, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			count, credentials := 0, 0
			service := &Service{
				authMode:           sample.mode,
				credentialRejected: &atomic.Bool{},
				credential: func(context.Context) string {
					credentials++
					return "fixture-token"
				},
			}
			service.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				count++
				expected := sample.auth && count == 1
				if (r.Header.Get("Authorization") == "Bearer fixture-token") != expected {
					t.Fatal("incorrect credential scope")
				}
				status := 200
				if count == 1 {
					status = sample.status
				}
				response := fixtureResponse([]byte("{}"), status)
				response.Header.Set("X-RateLimit-Remaining", "0")
				response.Header.Set("X-RateLimit-Reset", "1234567890")
				return response, nil
			})}
			response, err := service.get(context.Background(), sample.address, 1024)
			if sample.status == 403 {
				if err == nil || !strings.Contains(err.Error(), "1234567890") {
					t.Fatal("missing rate reset diagnostic")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = io.Copy(io.Discard, response.Body); err != nil {
					t.Fatal(err)
				}
				if err = response.Body.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if service.credentialRejected.Load() != (sample.status == 401 && sample.auth) {
				t.Fatal("credential rejection was not tracked")
			}
			if count != sample.attempts || (credentials > 0) != sample.auth {
				t.Fatal("unexpected authentication lookup or retry")
			}
		})
	}
}

// TestGitHubCredentialEnvironmentPriority 验证显式来源优先且从不需要访问真实凭证。
// 隔离PATH及HOME，空来源、恶意多行输入都不能调用开发者helper。
func TestGitHubCredentialEnvironmentPriority(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	for _, key := range []string{"SOTEA_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("GITHUB_TOKEN", "third")
	t.Setenv("GH_TOKEN", "second")
	t.Setenv("SOTEA_GITHUB_TOKEN", "first")
	if githubCredential(context.Background()) != "first" {
		t.Fatal("dedicated token did not win")
	}
	t.Setenv("SOTEA_GITHUB_TOKEN", "bad\nheader")
	if githubCredential(context.Background()) != "second" {
		t.Fatal("invalid dedicated value did not fall through")
	}
	t.Setenv("GH_TOKEN", "")
	if githubCredential(context.Background()) != "third" {
		t.Fatal("generic token unavailable")
	}
	t.Setenv("GITHUB_TOKEN", "")
	if githubCredential(context.Background()) != "" {
		t.Fatal("missing credentials did not remain anonymous")
	}
	if _, err := NewWithAuth("invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
