package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// githubCredential 按显式环境、GitHub CLI 和 Git helper 的顺序寻找可用 token。
// 查询不触发交互，固定 github.com 且在仓库外执行；不保存或回显任何敏感输出。
func githubCredential(ctx context.Context) string {
	for _, name := range []string{"SOTEA_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if token := cleanToken(os.Getenv(name)); token != "" {
			return token
		}
	}
	directory, err := os.MkdirTemp("", "sotea-github-auth-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(directory)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if token := cleanToken(credentialCommand(ctx, directory, "", "gh", "auth", "token", "--hostname", "github.com")); token != "" {
		return token
	}
	reply := credentialCommand(ctx, directory, "protocol=https\nhost=github.com\npath=Spring-Ocean/gitea-cli-so\n\n", "git", "-c", "core.askPass=", "-c", "credential.interactive=false", "credential", "fill")
	fields := map[string]string{}
	for _, line := range strings.Split(reply, "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if ok {
			fields[key] = value
		}
	}
	if fields["protocol"] != "https" || fields["host"] != "github.com" || (fields["path"] != "" && fields["path"] != Repository) {
		return ""
	}
	return cleanToken(fields["password"])
}

// cleanToken 只接受单行且不含控制字符的认证值。
// 只移除命令输出尾部换行，防止环境变量造成 HTTP 头注入。
func cleanToken(value string) string {
	value = strings.TrimRight(value, "\r\n")
	for _, char := range value {
		if char <= 32 || char >= 127 {
			return ""
		}
	}
	return value
}

// credentialCommand 有界捕获凭证命令输出并禁止 Git 提示与仓库环境注入。
// stderr 不透传，失败或取消视为该来源不可用；子进程始终等待清理。
func credentialCommand(ctx context.Context, directory, input, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.WaitDelay = 500 * time.Millisecond
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") && !strings.EqualFold(key, "GCM_INTERACTIVE") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GCM_INTERACTIVE=Never", "GH_PROMPT_DISABLED=1")
	command.Stdin = strings.NewReader(input)
	var output credentialBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return ""
	}
	return output.String()
}

// credentialBuffer 限制 helper 的敏感输出占用，避免恶意或异常进程消耗无限内存。
// 超出上限返回错误，调用方取消或超时后统一回收进程。
type credentialBuffer struct {
	buffer bytes.Buffer
}

// Write 接收至多 16 KiB 的协议输出，超限后拒绝继续保存。
// 缓冲区只在本次查询期间存在，不写文件或诊断。
func (b *credentialBuffer) Write(value []byte) (int, error) {
	if b.buffer.Len()+len(value) > 16384 {
		return 0, errors.New("Credential output exceeds its limit.")
	}
	return b.buffer.Write(value)
}

// String 返回本次捕获的有界敏感输出，仅供凭证解析使用。
// 不暴露ReadFrom等可绕过Write上限的缓冲区接口。
func (b *credentialBuffer) String() string {
	return b.buffer.String()
}
