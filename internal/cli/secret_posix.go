//go:build !windows

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"unicode/utf8"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// readSecret 在不回显的原始终端中有界读取，轮询取消而不创建遗留读 goroutine。
// 支持退格与 Ctrl-C／Ctrl-D，调用者负责在所有返回路径恢复先前终端状态。
func readSecret(ctx context.Context, file *os.File) (string, error) {
	return readTerminal(ctx, file, nil)
}

// readTerminal 在独占当前输入终端期间读取有限单行，nil echo 用于隐藏 token。
// 取消通过轮询响应，不启动阻塞读 goroutine；调用方必须恢复此前保存的终端模式。
func readTerminal(ctx context.Context, file *os.File, echo io.Writer) (string, error) {
	if _, err := term.MakeRaw(int(file.Fd())); err != nil {
		return "", app.Wrap(app.IOError, "Unable to disable terminal echo.", err)
	}
	value := []byte{}
	for {
		if err := ctx.Err(); err != nil {
			return "", app.Failure(err)
		}
		events := []unix.PollFd{{
			Fd:     int32(file.Fd()),
			Events: unix.POLLIN,
		}}
		ready, err := unix.Poll(events, 50)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return "", app.Wrap(app.IOError, "Unable to wait for secure terminal input.", err)
		}
		if ready == 0 {
			continue
		}
		var character [1]byte
		count, err := unix.Read(int(file.Fd()), character[:])
		if err != nil || count == 0 {
			return "", app.Wrap(app.Cancelled, "Secure input was closed.", err)
		}
		switch character[0] {
		case 3, 4:
			return "", app.Wrap(app.Cancelled, "Token input cancelled.", context.Canceled)
		case '\r', '\n':
			return string(value), nil
		case 8, 127:
			if len(value) > 0 {
				_, size := utf8.DecodeLastRune(value)
				value = value[:len(value)-size]
				if err := echoTerminal(echo, "\b \b"); err != nil {
					return "", err
				}
			}
		default:
			if character[0] < 32 || len(value) >= 1024 {
				return "", app.Wrap(app.InvalidArgument, "A token must contain at most 1024 bytes without control characters.", nil)
			}
			value = append(value, character[0])
			if err := echoTerminal(echo, string(character[:])); err != nil {
				return "", err
			}
		}
	}
}
