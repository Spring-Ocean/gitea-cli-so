package cli

import (
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
	"os"
)

// openRegular 复用正文、原始 API 和业务附件共同的普通文件检查。
// 成功句柄归调用者关闭；特殊文件拒绝行为由 fileinput 统一维护。
func openRegular(path string) (*os.File, error) {
	return fileinput.Open(path)
}
