package cli

import "os"

// openInputFile 在普通文件预检后使用 Windows 的 OPEN_EXISTING 只读打开。
// 不调用等待命名管道的接口；调用者再次检查实际句柄类型并负责关闭。
func openInputFile(path string) (*os.File, error) {
	return os.Open(path)
}
