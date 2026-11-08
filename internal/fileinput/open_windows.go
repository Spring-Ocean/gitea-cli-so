package fileinput

import "os"

// openInputFile 在普通文件预检后使用 Windows 的 OPEN_EXISTING 只读打开。
// 不调用等待命名管道的接口；调用者再次检查实际句柄类型并负责关闭。
func openInputFile(path string) (*os.File, error) {
	return os.Open(path)
}

// openRootFile 通过固定目录句柄打开根内文件，不跟随根外重解析路径。
// POSIX 保留非阻塞标志，Windows 使用平台根目录保护；返回句柄归调用者关闭。
func openRootFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|0, 0)
}
