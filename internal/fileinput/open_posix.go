//go:build !windows

package fileinput

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

// openRootFile 通过固定目录句柄打开根内文件，不跟随根外重解析路径。
// POSIX 保留非阻塞标志，Windows 使用平台根目录保护；返回句柄归调用者关闭。
func openRootFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
