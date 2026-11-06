package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// configureProcess 取消时通过系统 taskkill 结束仍存活的进程树。
// 失败时回退到直接终止根进程；Windows 10 的后代清理仍保留实际验收要求。
func configureProcess(command *exec.Cmd, capture bool) {
	command.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tool := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
		kill := exec.CommandContext(ctx, tool, "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F")
		if err := kill.Run(); err == nil {
			return nil
		}
		return command.Process.Kill()
	}
}

// cleanupProcess 不在根进程正常结束后按可能复用的 PID 再次执行 taskkill。
// 已自行脱离进程树的子进程不在此回退策略的强保证范围，后续需实机评估。
func cleanupProcess(command *exec.Cmd) {
}
