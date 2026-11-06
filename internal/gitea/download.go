package gitea

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Download 完整接收显式文件目标，使用同目录临时文件且永不覆盖现有路径。
// 成功同步后以原子不覆盖硬链接提交；reader 由调用者关闭，错误／取消清理临时文件。
func Download(ctx context.Context, reader io.Reader, target string) (returnedErr error) {
	if target == "" || target == "-" {
		return app.Wrap(app.InvalidArgument, "A file download requires an explicit target path.", nil)
	}
	if _, err := os.Lstat(target); err == nil {
		return app.Wrap(app.Conflict, "The download target already exists. Choose another path.", nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return app.Wrap(app.IOError, "Unable to inspect the download target.", err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".sotea-download-*")
	if err != nil {
		return app.Wrap(app.IOError, "Unable to create a temporary download in the target directory.", err)
	}
	open := true
	defer func() {
		if open {
			if err := file.Close(); err != nil {
				returnedErr = app.Wrap(app.IOError, "The temporary download could not be closed.", errors.Join(returnedErr, err))
			}
		}
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) && returnedErr == nil {
			returnedErr = app.Wrap(app.IOError, "The download was committed, but its temporary link could not be removed.", err)
		}
	}()
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return app.Failure(err)
		}
		count, readErr := reader.Read(buffer)
		if count > 0 {
			if _, err := file.Write(buffer[:count]); err != nil {
				return app.Wrap(app.IOError, "The download could not be written.", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return app.Failure(readErr)
		}
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	open = false
	if err := errors.Join(syncErr, closeErr); err != nil {
		return app.Wrap(app.IOError, "Unable to complete the temporary download.", err)
	}
	if err := ctx.Err(); err != nil {
		return app.Failure(err)
	}
	if err := os.Link(file.Name(), target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return app.Wrap(app.Conflict, "Another process created the download target. Its contents were preserved.", err)
		}
		return app.Wrap(app.IOError, "This filesystem could not commit the download without replacement. No overwrite fallback was used.", err)
	}
	return nil
}
