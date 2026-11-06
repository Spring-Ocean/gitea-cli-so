package gitea

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// TestPathAndRedirectBoundaries 保护代理子路径、重复参数与认证来源边界。
// 跳转目标使用独立服务计数，证明认证请求不会被发送到另一个来源。
func TestPathAndRedirectBoundaries(t *testing.T) {
	var escapedCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedCalls.Add(1)
	}))
	defer outside.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/prefix/api/v1/redirect" {
			http.Redirect(w, r, outside.URL+"/target", http.StatusFound)
			return
		}
		if r.URL.EscapedPath() != "/prefix/api/v1/repos/team/name%2Fpart" || len(r.URL.Query()["label"]) != 2 || r.Header.Get("Authorization") != "token synthetic" {
			t.Error("path, repeated query, or authentication was changed")
		}
		w.Write([]byte("original"))
	}))
	defer server.Close()
	client, err := New(Options{
		BaseURL:  server.URL + "/prefix",
		ProxyURL: "direct",
		Authenticate: func(r *http.Request) error {
			r.Header.Set("Authorization", "token synthetic")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Do(context.Background(), Request{Path: "/repos/team/name%2Fpart", Query: url.Values{"label": {"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(contents) != "original" {
		t.Fatal("response was not preserved")
	}
	response, err = client.Do(context.Background(), Request{Path: "/redirect"})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || escapedCalls.Load() != 0 {
		t.Fatal("cross-origin redirect was followed")
	}
	for _, path := range []string{"../escape", "/%2e%2e/escape", "/%252e%252e/escape", "//other.invalid/escape", "https://other.invalid", "/path?token=x", "/path\\escape"} {
		if _, err := client.Do(context.Background(), Request{Path: path}); err == nil {
			t.Fatalf("unsafe path was accepted: %q", path)
		}
	}
}

// TestReadRetryAndUnknownWrite 保护有界读取重试与禁止写入重放的契约。
// 即使提供幂等头，写连接中断也仅记录一次请求并返回未知结果。
func TestReadRetryAndUnknownWrite(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if reads.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Write([]byte("ok"))
			return
		}
		writes.Add(1)
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/api/v1/write-partial" {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	client, err := New(Options{BaseURL: server.URL, ProxyURL: "direct", ReadRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Do(context.Background(), Request{Path: "/read"})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if reads.Load() != 2 {
		t.Fatal("read retries did not obey the configured bound")
	}
	_, err = client.Do(context.Background(), Request{
		Method:  http.MethodPost,
		Path:    "/write",
		Body:    strings.NewReader("synthetic"),
		Headers: http.Header{"Idempotency-Key": {"synthetic-id"}},
	})
	if err == nil || app.Failure(err).Code != app.ResultUnknown || writes.Load() != 1 {
		t.Fatalf("write was replayed or unknown outcome lost: %v", err)
	}
	response, err = client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/write-partial"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err == nil || app.Failure(err).Code != app.ResultUnknown || writes.Load() != 2 {
		t.Fatalf("interrupted write response was replayed or misclassified: %v", err)
	}
}

// TestTLSResponseLimitAndCancellation 保护默认 TLS 校验和有限响应。
// 私有 CA 必须显式提供；超限和取消不能输出成完整成功。
func TestTLSResponseLimitAndCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/slow" {
			<-r.Context().Done()
			return
		}
		w.Write([]byte("12345"))
	}))
	defer server.Close()
	untrusted, err := New(Options{BaseURL: server.URL, ProxyURL: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = untrusted.Do(context.Background(), Request{Path: "/ok"})
	untrusted.Close()
	if err == nil || app.Failure(err).Code != app.TLSError {
		t.Fatalf("unknown CA was accepted or misclassified: %v", err)
	}
	ca := filepath.Join(t.TempDir(), "private-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := New(Options{BaseURL: server.URL, ProxyURL: "direct", CAFile: ca, MaxResponseBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer trusted.Close()
	response, err := trusted.Do(context.Background(), Request{Path: "/ok"})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err == nil || app.Failure(err).Code != app.ResponseTooLarge || string(contents) != "1234" {
		t.Fatal("response cap was not reported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := trusted.Do(ctx, Request{Path: "/slow"}); err == nil || app.Failure(err).Code != app.NetworkError {
		t.Fatal("deadline was not respected")
	}
}
