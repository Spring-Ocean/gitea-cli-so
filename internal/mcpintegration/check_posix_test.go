//go:build !windows

package mcpintegration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCheckCancellationReapsChild 保护连接检查超时后的真实子进程回收及凭证环境隔离。
// 可控脚本只写临时进程号，不输出凭证；Windows 进程边界仍保留真实平台验收。
func TestCheckCancellationReapsChild(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "slow-sotea")
	pidPath := filepath.Join(directory, "pid")
	// 路径由临时目录生成并作为固定脚本内的单引号参数转义，凭证值不写入文本。
	quotedPID := "'" + strings.ReplaceAll(pidPath, "'", "'\"'\"'") + "'"
	script := "#!/bin/sh\nif [ -n \"$SOTEA_TOKEN\" ]; then exit 9; fi\nprintf '%s' \"$$\" > " + quotedPID + "\nexec sleep 30\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOTEA_TOKEN", "synthetic-do-not-inherit")
	launch := testLaunch(t)
	launch.Command = binary
	path := filepath.Join(directory, "mcp.json")
	snapshot, err := Load(context.Background(), Cursor, path)
	if err != nil {
		t.Fatal(err)
	}
	change, err := snapshot.Prepare(launch, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err = Load(context.Background(), Cursor, path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := snapshot.Check(ctx)
		finished <- err
	}()
	var raw []byte
	for len(raw) == 0 {
		var err error
		raw, err = os.ReadFile(pidPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if len(raw) > 0 {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("process did not reach the fixture handshake: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	start := time.Now()
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled initialization, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("connection check did not stop within its cleanup budget")
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		killErr := syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("connection check left its process alive: %v; cleanup: %v", err, killErr)
	}
}
