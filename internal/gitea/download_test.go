package gitea

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

type failingDownload struct {
	target string
	raced  bool
}

// Read 在传输中创建竞争目标，下一次读取明确中断。
// 夹具只操作临时目录，保护不覆盖及中断不留伪完整文件的边界。
func (reader *failingDownload) Read(buffer []byte) (int, error) {
	if !reader.raced {
		reader.raced = true
		if reader.target != "" {
			if err := os.WriteFile(reader.target, []byte("other-writer"), 0o600); err != nil {
				return 0, err
			}
		}
		return copy(buffer, []byte("partial")), nil
	}
	if reader.target != "" {
		return 0, io.EOF
	}
	return 0, errors.New("synthetic interruption")
}

// TestDownloadNoReplacementAndCleanup 保护落盘前竞争、原文件、取消和传输中断。
// 对目标内容与临时文件实际检查，不以错误类别代替磁盘状态验证。
func TestDownloadNoReplacementAndCleanup(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "中文 artifact.bin")
	if err := Download(context.Background(), strings.NewReader("complete"), target); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), strings.NewReader("overwrite"), target); err == nil || app.Failure(err).Code != app.Conflict {
		t.Fatal("existing artifact was overwritten")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "complete" {
		t.Fatal("existing artifact contents changed")
	}
	racing := filepath.Join(directory, "race.bin")
	if err := Download(context.Background(), &failingDownload{target: racing}, racing); err == nil || app.Failure(err).Code != app.Conflict {
		t.Fatal("concurrent target creation was not protected")
	}
	contents, err = os.ReadFile(racing)
	if err != nil || string(contents) != "other-writer" {
		t.Fatal("concurrently created target was replaced")
	}
	broken := filepath.Join(directory, "broken.bin")
	if err := Download(context.Background(), &failingDownload{}, broken); err == nil {
		t.Fatal("interrupted transfer was accepted")
	}
	if _, err := os.Stat(broken); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial transfer appeared as a completed target")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Download(ctx, strings.NewReader("cancelled"), filepath.Join(directory, "cancelled.bin")); !errors.Is(err, context.Canceled) {
		t.Fatal("download cancellation was lost")
	}
	files, err := filepath.Glob(filepath.Join(directory, ".sotea-download-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary download files remained")
	}
}
