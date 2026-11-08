package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc 将固定官方请求交给隔离响应，不改变生产地址或使用真实网络。
// 仅用于验证认证边界、冷却及取消，不加载开发者配置或凭证。
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 实现测试 transport，并保留请求 context 的所有权。
// 返回体由产品服务关闭，夹具不启动后台网络服务。
func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// fixtureResponse 构造带确定内容长度的 HTTP 响应。
// 二进制数据不经文本解析，供真实下载和摘要逻辑使用。
func fixtureResponse(contents []byte, status int) *http.Response {
	return &http.Response{
		StatusCode:    status,
		ContentLength: int64(len(contents)),
		Body:          io.NopCloser(strings.NewReader(string(contents))),
		Header:        make(http.Header),
	}
}

// fixtureService 创建共享临时缓存和可控时钟的更新服务。
// transport 只接受固定 GitHub API 来源，任何凭证字段都导致用例失败。
func fixtureService(t *testing.T, clock *time.Time, handler roundTripFunc) *Service {
	t.Helper()
	return &Service{
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Scheme != "https" || (request.URL.Host != "api.github.com" && request.URL.Host != "github.com") || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Error("the updater sent a request outside the fixed credential-free source")
			}
			return handler(request)
		})},
		cachePath: filepath.Join(t.TempDir(), "updates.json"),
		now: func() time.Time {
			return *clock
		},
		goos:   "darwin",
		goarch: "arm64",
	}
}

// TestSemanticChannelsAndAssetBoundaries 保护 RC 数字排序、构建元数据和稳定通道。
// 同时拒绝草稿、非法版本、重复附件和第三方下载地址，避免安装错误发布。
func TestSemanticChannelsAndAssetBoundaries(t *testing.T) {
	s := &Service{goos: "darwin", goarch: "arm64"}
	releases := []Release{
		{Tag: "v0.1.0-rc.2", Prerelease: true},
		{Tag: "v0.1.0-rc.10", Prerelease: true},
		{Tag: "v0.0.9"},
		{Tag: "v99.0.0", Draft: true},
		{Tag: "v1.0.0-rc.01", Prerelease: true},
	}
	for _, sample := range []struct {
		current string
		pre     bool
		latest  string
		update  bool
	}{
		{"v0.1.0-rc.1", false, "v0.1.0-rc.10", true},
		{"v0.0.8", false, "v0.0.9", true},
		{"v0.1.0", false, "v0.0.9", false},
		{"v0.0.8", true, "v0.1.0-rc.10", true},
		{"v0.1.0-rc.10+ci.123", false, "v0.1.0-rc.10", false},
	} {
		result, err := s.selectRelease(sample.current, sample.pre, releases)
		if err != nil || result.Latest != sample.latest || result.Available != sample.update {
			t.Fatalf("wrong update channel for %+v: %+v %v", sample, result, err)
		}
	}
	for _, value := range []string{"dev", "v01.0.0", "v1.0", "v1.0.0+", "v1.0.0/evil", "v1.0.0-"} {
		if _, valid := parseVersion(value); valid {
			t.Fatalf("invalid version accepted: %q", value)
		}
	}
	name := "sotea-v1.0.0-darwin-arm64.tar.gz"
	archive := Asset{Name: name, URL: "https://github.com/" + Repository + "/releases/download/v1.0.0/" + name, Size: 10}
	sums := Asset{Name: "SHA256SUMS", URL: "https://github.com/" + Repository + "/releases/download/v1.0.0/SHA256SUMS", Size: 100}
	valid := Release{Tag: "v1.0.0", Assets: []Asset{archive, sums}}
	if _, _, err := s.assets(valid); err != nil {
		t.Fatal(err)
	}
	for _, assets := range [][]Asset{{archive}, {archive, archive, sums}, {{Name: name, URL: "https://example.invalid/evil", Size: 10}, sums}} {
		if _, _, err := s.assets(Release{Tag: "v1.0.0", Assets: assets}); err == nil {
			t.Fatal("unsafe or ambiguous release assets accepted")
		}
	}
}

// TestAutomaticCheckPersistsFiveHourAttempt 保护多实例缓存共享、精确到期和失败冷却。
// 成功后升级当前版本会重算提示；显式检查允许绕过时间，但不泄露环境 token。
func TestAutomaticCheckPersistsFiveHourAttempt(t *testing.T) {
	t.Setenv("SOTEA_TOKEN", "synthetic-never-send")
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	var fail atomic.Bool
	s := fixtureService(t, &now, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if fail.Load() {
			return fixtureResponse([]byte("unavailable"), 503), nil
		}
		contents, err := json.Marshal([]Release{{Tag: "v1.0.1"}})
		if err != nil {
			return nil, err
		}
		return fixtureResponse(contents, 200), nil
	})
	first, err := s.Check(context.Background(), "v1.0.0", false, false)
	if err != nil || !first.Available || first.Cached {
		t.Fatalf("first check failed: %+v %v", first, err)
	}
	other := *s
	now = now.Add(CheckInterval - time.Second)
	second, err := other.Check(context.Background(), "v1.0.1", false, false)
	if err != nil || !second.Cached || second.Available || calls.Load() != 1 {
		t.Fatalf("cache did not preserve the five-hour boundary: %+v %v", second, err)
	}
	now = now.Add(time.Second)
	fail.Store(true)
	if _, err := other.Check(context.Background(), "v1.0.0", false, false); err == nil {
		t.Fatal("HTTP failure was reported as an up-to-date result")
	}
	if _, err := s.Check(context.Background(), "v1.0.0", false, false); err == nil || calls.Load() != 2 {
		t.Fatal("a failed automatic attempt did not retain its cooldown")
	}
	fail.Store(false)
	if _, err := s.Check(context.Background(), "v1.0.0", false, true); err != nil || calls.Load() != 3 {
		t.Fatalf("explicit check did not bypass cooldown: %v", err)
	}
}

// TestConcurrentAndCancelledChecksNeverDuplicateAutomaticRequest 保护并发进程检查及取消冷却。
// 首个请求受控等待，第二服务只读缓存；取消后不会立即重发请求。
func TestConcurrentAndCancelledChecksNeverDuplicateAutomaticRequest(t *testing.T) {
	now := time.Now()
	started := make(chan struct{})
	s := fixtureService(t, &now, func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Check(ctx, "v1.0.0", false, false)
		done <- err
	}()
	<-started
	other := *s
	result, err := other.Check(context.Background(), "v1.0.0", false, false)
	if err != nil || !result.Cached {
		t.Fatalf("a concurrent automatic check did not reuse the reserved attempt: %v", err)
	}
	if _, err := other.Check(context.Background(), "v1.0.0", false, true); err == nil {
		t.Fatal("an explicit check ignored the in-progress lock")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation was lost: %v", err)
	}
	if _, err := other.Check(context.Background(), "v1.0.0", false, false); err == nil {
		t.Fatal("a cancelled check did not preserve its failure cooldown")
	}
}

// TestReleaseRedirectsStayOnTrustedHTTPSHosts 保护下载不能跨协议或流向任意主机。
// 允许官方附件存储，但跳转后不保留认证与 Cookie。
func TestReleaseRedirectsStayOnTrustedHTTPSHosts(t *testing.T) {
	for _, sample := range []struct {
		address string
		allowed bool
	}{
		{"https://release-assets.githubusercontent.com/asset?signature=fixture", true},
		{"http://github.com/asset", false},
		{"https://github.com.evil.invalid/asset", false},
		{"https://user:password@github.com/asset", false},
		{"https://github.com:444/asset", false},
	} {
		address, err := url.Parse(sample.address)
		if err != nil {
			t.Fatal(err)
		}
		request := &http.Request{URL: address, Header: http.Header{"Authorization": {"secret"}, "Cookie": {"secret"}}}
		err = checkRedirect(request, nil)
		if (err == nil) != sample.allowed {
			t.Fatalf("unexpected redirect policy: %s %v", sample.address, err)
		}
		if err == nil && (request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "") {
			t.Fatal("redirect retained credentials")
		}
	}
}
