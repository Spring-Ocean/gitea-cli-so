//go:build !windows

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// openInputFile 以非阻塞方式取得只读句柄，阻止路径检查后的 FIFO 替换造成等待。
// 普通文件忽略 O_NONBLOCK；调用者仍须对实际句柄执行 Stat 并负责关闭。
func openInputFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
