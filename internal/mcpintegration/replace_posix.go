//go:build !windows

package mcpintegration

import "os"

// replaceFile 使用同目录 rename 替换 POSIX 配置文件。
// 原文件在替换前保持可用，调用者负责同步临时文件与清理失败。
func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
