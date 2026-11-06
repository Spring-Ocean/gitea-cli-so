//go:build !windows

package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"golang.org/x/sys/unix"
)

// TestSpecialInputRejectedWithoutWaiting 保护无人写入的 FIFO 不阻塞正文或 API 输入。
// 不启动无法回收的阻塞读取；非阻塞平台句柄边界也必须拒绝特殊文件。
func TestSpecialInputRejectedWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := openRegular(path); err == nil || app.Failure(err).Code != app.InvalidArgument {
		t.Fatal("raw or multipart input accepted a FIFO")
	}
	if _, err := readBodyFile(path); err == nil || app.Failure(err).Code != app.InvalidArgument {
		t.Fatal("body file accepted a FIFO")
	}
	file, err := openInputFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("special file checks unexpectedly waited for a writer")
	}
}
