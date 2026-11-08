package fileinput

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// TestAuthorizedRootAndFrozenContent 保护根内文件、symlink 逃逸和固定快照内容。
// 原文件原地修改不应改变已冻结的上传字节；测试只使用临时目录。
func TestAuthorizedRootAndFrozenContent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sourcePath := filepath.Join(root, "input.txt")
	if err := os.WriteFile(sourcePath, []byte("confirmed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, name := range []string{"../secret.txt", filepath.Join(outside, "secret.txt")} {
		file, err := scope.Open(name)
		if err == nil {
			file.Close()
			t.Fatalf("root escape opened: %s", name)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		file, err := scope.Open("link/secret.txt")
		if err == nil {
			file.Close()
			t.Fatal("root-external symlink opened")
		}
	}
	source, err := scope.Open("input.txt")
	if err != nil {
		t.Fatal(err)
	}
	frozen, cleanup, err := scope.Freeze(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("modified!"), 0600); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(frozen)
	if err != nil || string(contents) != "confirmed" {
		t.Fatalf("snapshot followed original modifications: %q, %v", contents, err)
	}
	if err := frozen.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

// TestFileAndSnapshotBudgets 保护实际文件与并发快照配额，不只检查请求参数。
// 配额不足必须在复制前拒绝，释放快照后相同预算可再次使用。
func TestFileAndSnapshotBudgets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("12345678"), 0600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Close(); err != nil {
			t.Error(err)
		}
	}()
	file, err := scope.Open("file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, first, err := scope.Freeze(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = scope.Freeze(context.Background(), file)
	if err == nil || app.Failure(err).Code != app.ResourceLimit {
		t.Fatal("snapshot budget exceeded")
	}
	if err := first(); err != nil {
		t.Fatal(err)
	}
	_, second, err := scope.Freeze(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	if err := second(); err != nil {
		t.Fatal(err)
	}
}
