package update

import (
	"errors"
	"os"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// replaceExecutable 将正在使用的旧 exe 改名保留，再移动已验证文件到原路径。
// Windows 拒绝改名时保持原状；第二步失败尝试恢复，恢复失败明确返回备份位置。
func replaceExecutable(staged, target, backup string) error {
	if err := os.Rename(target, backup); err != nil {
		return app.Wrap(app.IOError, "Windows could not preserve the running executable. Close other sotea processes and try again, or install the release manually.", err)
	}
	if err := os.Rename(staged, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return app.Wrap(app.ResultUnknown, "Windows could not install or restore the program. Restore the previous executable from "+backup+" to "+target+".", errors.Join(err, restoreErr))
		}
		return app.Wrap(app.IOError, "Windows could not install the update. The previous executable was restored.", err)
	}
	return nil
}
