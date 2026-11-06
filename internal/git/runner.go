// Package git 通过参数数组调用系统 Git，并隔离查询与透传的输出边界。
// 本包不决定 API 身份，helper 的认证复用由上层服务处理。
package git

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Runner 保存系统 Git 的执行配置。
// Environment 追加覆盖当前环境；捕获默认 30 秒、每条输出流最多 1 MiB。
type Runner struct {
	Executable  string
	Environment []string
	Timeout     time.Duration
	OutputLimit int
}

// Captured 是内部查询的有界结果。
// helper 可能在 Stdout 中返回密钥，不得直接序列化、记录或回传该对象。
type Captured struct {
	Stdout []byte
	Stderr []byte
}

// ProcessError 保存正常启动后的 Git 非零退出状态。
// 不包含参数或原始 stderr，透传调用使用 ExitCode 保留 Git 的原状态。
type ProcessError struct {
	Code int
}

// Error 返回不包含命令行参数的摘要。
// Git 已经把实际诊断写入原始 stderr 时，上层不应重复输出此摘要。
func (e *ProcessError) Error() string {
	return "Git exited unsuccessfully."
}

// ExitCode 返回 Git 进程报告的正常退出码。
// 信号终止及主动取消由执行器单独转换，不存为负数。
func (e *ProcessError) ExitCode() int {
	return e.Code
}

// Capture 在指定目录执行禁止提示的 Git 查询，捕获有界输出。
// 非零退出返回 ProcessError；超时／取消／启动失败返回带原因的应用错误。
func (r Runner) Capture(ctx context.Context, directory string, args []string, input io.Reader) (Captured, error) {
	return r.capture(ctx, directory, args, input, false)
}

// Passthrough 原样连接 Git 的输入输出，不设查询超时或重排参数。
// 进程随 ctx 取消结束；正常非零退出保留 ProcessError 供 CLI 原码退出。
func (r Runner) Passthrough(ctx context.Context, directory string, args []string, in io.Reader, out, diagnostic io.Writer) error {
	command, err := r.command(ctx, directory, args)
	if err != nil {
		return err
	}
	command.Stdin = in
	command.Stdout = out
	command.Stderr = diagnostic
	configureProcess(command, false)
	return execute(ctx, command)
}

// capture 为查询或明确允许解锁的登录流程捕获输出。
// 仅 interactive=true 保留用户自己的提示配置；所有捕获仍受超时及输出上限约束。
func (r Runner) capture(ctx context.Context, directory string, args []string, input io.Reader, interactive bool) (Captured, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !interactive {
		args = append([]string{"-c", "core.askPass=", "-c", "credential.interactive=false"}, args...)
	}
	command, err := r.command(ctx, directory, args)
	if err != nil {
		return Captured{}, err
	}
	if !interactive {
		command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=Never")
	}
	limit := r.OutputLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	stdout, stderr := limitedBuffer{limit: limit}, limitedBuffer{limit: limit}
	command.Stdin = input
	command.Stdout = &stdout
	command.Stderr = &stderr
	configureProcess(command, true)
	err = execute(ctx, command)
	result := Captured{
		Stdout: stdout.contents,
		Stderr: stderr.contents,
	}
	if stdout.truncated || stderr.truncated {
		return result, app.Wrap(app.ResponseTooLarge, "Git output exceeded the capture limit.", nil)
	}
	return result, err
}

// command 定位 Git 并按参数数组构造命令。
// 环境不经过 shell 展开；传入的目录和参数只交给 Git 自身处理。
func (r Runner) command(ctx context.Context, directory string, args []string) (*exec.Cmd, error) {
	name := r.Executable
	if name == "" {
		name = "git"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, app.Wrap(app.GitUnavailable, "Git is unavailable. Install Git or use an explicit API context.", err)
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), r.Environment...)
	command.WaitDelay = 2 * time.Second
	return command, nil
}

// execute 等待进程结束并区分取消、信号、非零退出和启动错误。
// 捕获模式还清理专属进程组，避免 helper 后台后代继续运行。
func execute(ctx context.Context, command *exec.Cmd) error {
	err := command.Run()
	cleanupProcess(command)
	if ctx.Err() != nil {
		code := app.GitError
		message := "Git timed out."
		if errors.Is(ctx.Err(), context.Canceled) {
			code, message = app.Cancelled, "Git operation cancelled."
		}
		return app.Wrap(code, message, ctx.Err())
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return &ProcessError{Code: exit.ExitCode()}
	}
	return app.Wrap(app.GitError, "Git could not start, terminated by a signal, or failed to release its streams.", err)
}

// limitedBuffer 保留上限内的前缀，同时持续消耗子进程输出。
// 截断被明确标记，不能把该前缀当成完整成功结果。
type limitedBuffer struct {
	contents  []byte
	limit     int
	truncated bool
}

// Write 接收进程输出且不超过内存上限。
// 返回完整消费长度，避免子进程因输出管道无人读取而阻塞。
func (b *limitedBuffer) Write(contents []byte) (int, error) {
	remaining := b.limit - len(b.contents)
	if len(contents) > remaining {
		b.contents = append(b.contents, contents[:remaining]...)
		b.truncated = true
	} else {
		b.contents = append(b.contents, contents...)
	}
	return len(contents), nil
}
