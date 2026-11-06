package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExecuteMachineErrors 保护参数错误、取消和帮助的输出边界。
// JSON 请求不得混入普通帮助，显式帮助仍显示可读说明。
func TestExecuteMachineErrors(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"--json", "unknown-command"}, {"--json", "version", "extra"}} {
		var out, diagnostic bytes.Buffer
		code := Execute(context.Background(), args, nil, &out, &diagnostic, "test")
		if code != 2 || !json.Valid(out.Bytes()) || diagnostic.Len() != 0 {
			t.Fatalf("machine error contract failed for %v: code=%d stdout=%q stderr=%q", args, code, out.String(), diagnostic.String())
		}
	}
	var out, diagnostic bytes.Buffer
	if code := Execute(context.Background(), []string{"--json", "--help"}, nil, &out, &diagnostic, "test"); code != 0 || json.Valid(out.Bytes()) {
		t.Fatal("explicit help did not override JSON mode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := Execute(ctx, []string{"version"}, nil, &out, &diagnostic, "test"); code != 130 {
		t.Fatal("cancellation was not mapped to 130")
	}
}

// TestGitPassthroughCLI 验证 CLI 使用工作目录并保留正常 Git 返回状态。
// 临时目录没有仓库时的 Git 诊断保持原始流，不包装成业务 JSON。
func TestGitPassthroughCLI(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "中文 path")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	code := Execute(context.Background(), []string{"--workdir", directory, "git", "--", "rev-parse", "--show-toplevel"}, nil, &out, &diagnostic, "test")
	if code != 128 || out.Len() != 0 || diagnostic.Len() == 0 {
		t.Fatalf("Git status was changed: code=%d stdout=%q stderr=%q", code, out.String(), diagnostic.String())
	}
}
