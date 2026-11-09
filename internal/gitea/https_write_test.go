package gitea

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHTTPSReadThenSingleWrite 保护支持 HTTP/2 的 HTTPS 实例上的一次 JSON 写入。
// 读取与写入使用同一实际客户端，服务端只接受合成数据并检查协议切换及零重放。
func TestHTTPSReadThenSingleWrite(t *testing.T) {
	var writes atomic.Int32
	var readProtocol, writeProtocol atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			readProtocol.Store(int32(r.ProtoMajor))
			if _, err := w.Write([]byte("ready")); err != nil {
				t.Error(err)
			}
			return
		}
		writeProtocol.Store(int32(r.ProtoMajor))
		writes.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "synthetic" {
			t.Error("request body was not preserved")
		}
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("created")); err != nil {
			t.Error(err)
		}
	}))
	server.EnableHTTP2 = true
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Options{
		BaseURL:  server.URL,
		ProxyURL: "direct",
		CAFile:   ca,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Do(context.Background(), Request{Path: "/version"})
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read response lost: read=%v close=%v", readErr, closeErr)
	}
	response, err = client.Do(context.Background(), Request{
		Method: http.MethodPost,
		Path:   "/releases",
		Body:   strings.NewReader("synthetic"),
	})
	if err != nil {
		t.Fatalf("HTTPS write failed: %v (cause: %s); read protocol=%d, write handler calls=%d, read ALPN=%v, write ALPN=%v",
			err, errors.Unwrap(err), readProtocol.Load(), writes.Load(),
			client.read.Transport.(*http.Transport).TLSClientConfig.NextProtos,
			client.write.Transport.(*http.Transport).TLSClientConfig.NextProtos)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr = response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusCreated || string(body) != "created" {
		t.Fatalf("write response lost: read=%v close=%v", readErr, closeErr)
	}
	if writes.Load() != 1 || writeProtocol.Load() != 1 || readProtocol.Load() != 2 {
		t.Fatalf("unexpected protocols or replay: read=%d write=%d writes=%d", readProtocol.Load(), writeProtocol.Load(), writes.Load())
	}
}
