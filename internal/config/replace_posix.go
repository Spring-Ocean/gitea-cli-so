//go:build !windows

package config

import "os"

// replaceFile 在同一文件系统内原子替换目标目录项。
// 调用者已经完成临时文件同步，替换失败不会先删除旧文件。
func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
