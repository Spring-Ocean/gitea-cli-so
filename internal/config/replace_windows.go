package config

import (
	"syscall"
	"unsafe"
)

// replaceFile 使用 Windows 同卷替换并等待写入完成。
// 不先删除旧目标；底层拒绝替换时将错误交回调用方，真实 Windows 行为仍需验证。
func replaceFile(source, target string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	move := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	success, _, callErr := move.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x1|0x8)
	if success == 0 {
		return callErr
	}
	return nil
}
