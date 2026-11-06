//go:build !windows

package git

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcess 为捕获查询创建独立组，取消时终止整个组。
// 交互透传留在前台终端组，保持 Git 自身的终端输入和信号语义。
func configureProcess(command *exec.Cmd, capture bool) {
	if capture {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error {
			err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
	}
}

// cleanupProcess 清理专属捕获组中的剩余后代。
// 透传不杀当前前台组，避免影响 sotea 或调用者所在的终端进程。
func cleanupProcess(command *exec.Cmd) {
	if command.Process != nil && command.SysProcAttr != nil && command.SysProcAttr.Setpgid {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
