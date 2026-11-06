package compat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// TestVersionEvidenceAndDetectionFailures 保护精确版本与定义／运行证据分离。
// 七版、后缀、权限和未知入口均不自动变成全部兼容或不兼容。
func TestVersionEvidenceAndDetectionFailures(t *testing.T) {
	for _, raw := range []string{"1.22.6", "1.23.8", "1.24.7", "1.25.5", "1.26.4", "1.27.3", "28.3.0"} {
		info := Parse(raw)
		if !info.ExactMatch || info.Detected != "identified" || info.InstanceEvidence != "not_tested" || info.DefinitionEvidence != "analyzed" {
			t.Fatalf("version evidence was conflated: %+v", info)
		}
	}
	for _, raw := range []string{"1.27.3-dev", "28.3.0+custom", "1.27.4", "", "not-gitea"} {
		if info := Parse(raw); info.ExactMatch || info.Raw != raw {
			t.Fatal("suffix or unknown version impersonated a tested formal release")
		}
	}
	for _, status := range []int{200, 401, 403, 404} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/prefix/api/v1/version" {
					t.Error("version request lost the instance prefix")
				}
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"version": ""})
			}))
			defer server.Close()
			client, err := gitea.New(gitea.Options{
				BaseURL:  server.URL + "/prefix",
				ProxyURL: "direct",
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			info, err := Detect(context.Background(), client, server.URL+"/prefix")
			if err == nil || info.Detected != "unknown" || info.ExactMatch || info.Error == nil {
				t.Fatal("failed detection was converted to compatibility evidence")
			}
			if status != 200 && info.Error.HTTPStatus != status {
				t.Fatal("HTTP detection failure category was lost")
			}
		})
	}
}

// TestCacheCoalescingIsolationAndExpiry 保护合并、TTL、刷新、身份隔离和取消等待。
// 使用可控时钟与同步屏障，避免依赖脆弱的实际过期等待。
func TestCacheCoalescingIsolationAndExpiry(t *testing.T) {
	now := time.Now()
	cache := Cache{Now: func() time.Time {
		return now
	}}
	var calls atomic.Int32
	fetch := func(context.Context) (Info, error) {
		calls.Add(1)
		return Parse("28.3.0"), nil
	}
	ctx := context.Background()
	if _, err := cache.Get(ctx, "instance/account/revision", false, fetch); err != nil {
		t.Fatal(err)
	}
	if info, err := cache.Get(ctx, "instance/account/revision", false, fetch); err != nil || !info.Cached || calls.Load() != 1 {
		t.Fatal("cache did not reuse an unexpired result")
	}
	now = now.Add(6 * time.Minute)
	for _, request := range []struct {
		key     string
		refresh bool
	}{{"instance/account/revision", false}, {"instance/account/revision", true}, {"instance/other/revision", false}} {
		if _, err := cache.Get(ctx, request.key, request.refresh, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatal("expiry, refresh or account isolation was ignored")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		cache.Get(ctx, "inflight", false, func(context.Context) (Info, error) {
			close(started)
			<-release
			return Parse("1.22.6"), nil
		})
	}()
	<-started
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := cache.Get(canceled, "inflight", false, fetch); err == nil || app.Failure(err).Code != app.Cancelled {
		t.Fatal("cancelled waiter remained blocked")
	}
	close(release)
	wait.Wait()
	if info, err := cache.Get(ctx, "inflight", false, fetch); err != nil || !info.Cached || info.Raw != "1.22.6" {
		t.Fatal("inflight result was lost")
	}
	bad := func(context.Context) (Info, error) {
		calls.Add(1)
		return Parse(""), app.Wrap(app.PermissionDenied, "Synthetic permission failure.", nil)
	}
	if _, err := cache.Get(ctx, "failure", false, bad); err == nil {
		t.Fatal("failure was hidden")
	}
	now = now.Add(31 * time.Second)
	before := calls.Load()
	if _, err := cache.Get(ctx, "failure", false, bad); err == nil || calls.Load() != before+1 {
		t.Fatal("failure TTL exceeded 30 seconds")
	}
}
