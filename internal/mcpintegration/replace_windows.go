package mcpintegration

import "golang.org/x/sys/windows"

// replaceFile 使用 Windows 同卷替换，避免先删除原配置文件。
// 文件占用和访问权限错误原样返回；真实 Windows 运行证据独立记录。
func replaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
