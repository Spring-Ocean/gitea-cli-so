//go:build !windows

package update

import (
	"errors"
	"os"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// replaceExecutable 先为旧程序保留独立目录项，再原子替换安装路径。
// 硬链接或替换失败不删除原程序；不支持硬链接的安装卷明确要求手动安装。
func replaceExecutable(staged, target, backup string) error {
	if err := os.Link(target, backup); err != nil {
		return app.Wrap(app.IOError, "Unable to preserve the previous executable. Use manual installation on this filesystem.", err)
	}
	if err := os.Rename(staged, target); err != nil {
		return app.Wrap(app.IOError, "Unable to replace the executable. The previous program remains installed.", errors.Join(err, os.Remove(backup)))
	}
	return nil
}
