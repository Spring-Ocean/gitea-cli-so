// Package fileinput 为 CLI 正文、原始请求和业务附件共用普通文件输入边界。
// 特殊文件不会被当作无界流读取；成功句柄由调用者负责关闭。
package fileinput

import (
	"errors"
	"os"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Open 在打开前后核对普通文件，供正文、原始请求及 multipart 共用。
// POSIX 打开采用非阻塞标志避免检查后被换成 FIFO；成功句柄归调用者关闭。
func Open(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, app.Wrap(app.IOError, "Unable to inspect the input file.", err)
	}
	if !info.Mode().IsRegular() {
		return nil, app.Wrap(app.InvalidArgument, "Input must be a regular file; pipes, devices, and directories are not supported.", nil)
	}
	file, err := openInputFile(path)
	if err != nil {
		return nil, app.Wrap(app.IOError, "Unable to open the input file.", err)
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, app.Wrap(app.InvalidArgument, "The opened input must be a regular file.", errors.Join(err, file.Close()))
	}
	return file, nil
}
