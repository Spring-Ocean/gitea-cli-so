package cli

import (
	"context"
	"io"
	"os"
	"unicode/utf16"
	"unsafe"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// readSecret 使用控制台输入事件禁用回显并响应取消，不让读取跨进程退出残留。
// 仅消费按下的 Unicode 键，支持退格与 Ctrl-C／Ctrl-D；调用者负责恢复模式。
func readSecret(ctx context.Context, file *os.File) (string, error) {
	return readTerminal(ctx, file, nil)
}

// readTerminal 通过可取消的控制台事件读取有界单行，nil echo 保持输入隐藏。
// 所有终端模式更改由调用者恢复；不将控制字符交给输出终端执行。
func readTerminal(ctx context.Context, file *os.File, echo io.Writer) (string, error) {
	if _, err := term.MakeRaw(int(file.Fd())); err != nil {
		return "", app.Wrap(app.IOError, "Unable to disable terminal echo.", err)
	}
	procedure := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")
	value := []uint16{}
	for {
		if err := ctx.Err(); err != nil {
			return "", app.Failure(err)
		}
		state, err := windows.WaitForSingleObject(windows.Handle(file.Fd()), 50)
		if err != nil {
			return "", app.Wrap(app.IOError, "Unable to wait for secure terminal input.", err)
		}
		if state == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		var record struct {
			EventType  uint16
			Padding    uint16
			KeyDown    int32
			Repeat     uint16
			VirtualKey uint16
			Scan       uint16
			Character  uint16
			Control    uint32
		}
		var count uint32
		ok, _, callErr := procedure.Call(file.Fd(), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return "", app.Wrap(app.IOError, "Unable to read secure console input.", callErr)
		}
		if count == 0 || record.EventType != 1 || record.KeyDown == 0 {
			continue
		}
		switch record.Character {
		case 0:
			continue
		case 3, 4, 27:
			return "", app.Wrap(app.Cancelled, "Input cancelled.", context.Canceled)
		case '\r', '\n':
			return string(utf16.Decode(value)), nil
		case 8, 127:
			if len(value) > 0 {
				last := value[len(value)-1]
				value = value[:len(value)-1]
				if last >= 0xDC00 && last <= 0xDFFF && len(value) > 0 {
					value = value[:len(value)-1]
				}
				if err := echoTerminal(echo, "\b \b"); err != nil {
					return "", err
				}
			}
		default:
			if record.Character < 32 || len(value) >= 1024 {
				return "", app.Wrap(app.InvalidArgument, "Token input is too long or contains control characters.", nil)
			}
			repeats := record.Repeat
			if repeats == 0 {
				repeats = 1
			}
			for i := uint16(0); i < repeats; i++ {
				if len(value) >= 1024 {
					return "", app.Wrap(app.InvalidArgument, "Token input is too long.", nil)
				}
				value = append(value, record.Character)
				if record.Character < 0xD800 || record.Character > 0xDBFF {
					start := len(value) - 1
					if record.Character >= 0xDC00 && record.Character <= 0xDFFF && start > 0 {
						start--
					}
					if err := echoTerminal(echo, string(utf16.Decode(value[start:]))); err != nil {
						return "", err
					}
				}
			}
		}
	}
}
