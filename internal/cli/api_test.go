package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRawAPIUploadsDownloadsAndConfirmation 保护任意方法、二进制、multipart 与明确确认。
// 所有请求都访问本机合成服务，断连写入只允许一次，文件目标不被覆盖。
func TestRawAPIUploadsDownloadsAndConfirmation(t *testing.T) {
	var writes atomic.Int32
	payload := []byte{0, 1, 2, 255, 'x'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write(payload)
			return
		}
		writes.Add(1)
		if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("multipart request was invalid")
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("attachment")
			if err != nil {
				t.Error("multipart attachment was missing")
				w.WriteHeader(400)
				return
			}
			value, err := io.ReadAll(file)
			file.Close()
			if err != nil || !bytes.Equal(value, payload) || r.FormValue("name") != "sample" {
				t.Error("multipart bytes or fields changed")
			}
			w.Write([]byte("uploaded"))
			return
		}
		value, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("unable to read synthetic request")
		}
		if r.URL.Query().Get("drop") == "true" {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		w.Write(value)
	}))
	defer server.Close()
	t.Setenv("SOTEA_TOKEN", "")
	t.Setenv("SOTEA_INSTANCE", "")
	t.Setenv("SOTEA_ACCOUNT", "")
	directory := t.TempDir()
	input := filepath.Join(directory, "source.bin")
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, []byte, string) {
		t.Helper()
		var out, diagnostic bytes.Buffer
		base := []string{"--config", filepath.Join(directory, "config.json"), "--workdir", directory, "--instance", server.URL, "api", "/raw"}
		code := Execute(context.Background(), append(base, args...), nil, &out, &diagnostic, "test")
		return code, out.Bytes(), diagnostic.String()
	}
	if code, _, _ := run("--method", "POST", "--input", input); code != 2 || writes.Load() != 0 {
		t.Fatal("unconfirmed raw write reached the server")
	}
	if code, out, _ := run("--method", "PATCH", "--data", `{"unchanged":true}`, "--yes", "--json"); code != 0 || string(out) != `{"unchanged":true}` {
		t.Fatal("raw JSON request was enveloped or modified")
	}
	if code, out, _ := run("--method", "PUT", "--input", input, "--yes"); code != 0 || !bytes.Equal(out, payload) {
		t.Fatal("raw binary input was changed")
	}
	if code, out, _ := run("--method", "POST", "--upload", "attachment="+input, "--form", "name=sample", "--yes"); code != 0 || string(out) != "uploaded" {
		t.Fatal("multipart upload did not preserve the request")
	}
	target := filepath.Join(directory, "download.bin")
	if code, out, _ := run("--output", target); code != 0 || len(out) != 0 {
		t.Fatal("explicit download did not commit a file")
	}
	contents, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(contents, payload) {
		t.Fatal("downloaded bytes were changed")
	}
	if code, _, _ := run("--output", target); code != 1 {
		t.Fatal("existing download target was accepted")
	}
	limited := filepath.Join(directory, "limited.bin")
	if code, _, _ := run("--output", limited, "--max-response-bytes", "4"); code != 1 {
		t.Fatal("response cap was ignored for file output")
	}
	if _, err := os.Stat(limited); !os.IsNotExist(err) {
		t.Fatal("truncated response became a completed artifact")
	}
	before := writes.Load()
	if code, out, diagnostic := run("--method", "POST", "--data", "accepted", "--max-response-bytes", "4", "--yes"); code != 3 || string(out) != "acce" || writes.Load() != before+1 || !strings.Contains(diagnostic, "PARTIAL_FAILURE") {
		t.Fatal("accepted raw write lost its outcome after response truncation")
	}
	before = writes.Load()
	if code, _, diagnostic := run("--method", "POST", "--data", "synthetic", "--query", "drop=true", "--yes"); code != 4 || writes.Load() != before+1 || !strings.Contains(diagnostic, "RESULT_UNKNOWN") {
		t.Fatal("raw write was replayed or its unknown outcome was lost")
	}
}
