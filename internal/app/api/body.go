package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/fileinput"
)

// Field 是显式 multipart 文本项，名称和值不经过 shell。
// 重复名称保留输入次序，不转换为丢失重复值的对象。
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Upload 是单个 multipart 文件来源，路径不推导新的授权根。
// 远端文件名始终采用原路径 basename，不采用内部快照名字。
type Upload struct {
	Field string `json:"field_name"`
	Path  string `json:"file_path"`
}

// BodySource 表达一种明确正文，Text 指针区分无正文和零字节正文。
// Open 为真实文件使用方提供授权／快照边界；返回句柄交由 OpenBody 的 cleanup 关闭。
type BodySource struct {
	Text   *string
	File   string
	Fields []Field
	Files  []Upload
	Open   func(string) (*os.File, error)
}

// OpenBody 构造原始或 multipart 正文，保留重复字段和任意文件字节。
// 调用者必须在发送结束或失败后执行 cleanup；取消关闭生产管道并等待其退出。
func OpenBody(ctx context.Context, source BodySource) (io.Reader, string, func() error, error) {
	nothing := func() error {
		return nil
	}
	count := 0
	for _, present := range []bool{source.Text != nil, source.File != "", len(source.Files) > 0} {
		if present {
			count++
		}
	}
	if count > 1 || (len(source.Fields) > 0 && len(source.Files) == 0) {
		return nil, "", nothing, app.Wrap(app.InvalidArgument, "Use one explicit body source; multipart fields require files.", nil)
	}
	if source.Text != nil {
		if !utf8.ValidString(*source.Text) {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Inline body must contain valid UTF-8. Use a file for arbitrary bytes.", nil)
		}
		contentType := "application/octet-stream"
		if json.Valid([]byte(*source.Text)) {
			contentType = "application/json"
		}
		return strings.NewReader(*source.Text), contentType, nothing, nil
	}
	opener := source.Open
	if opener == nil {
		opener = fileinput.Open
	}
	if source.File != "" {
		file, err := opener(source.File)
		if err != nil {
			return nil, "", nothing, err
		}
		return file, "application/octet-stream", func() error {
			return closeFile(file)
		}, nil
	}
	if len(source.Files) == 0 {
		return nil, "", nothing, nil
	}
	for _, field := range source.Fields {
		if field.Name == "" || strings.ContainsAny(field.Name, "\r\n\x00") {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Multipart fields require explicit names without control separators.", nil)
		}
	}
	files := []*os.File{}
	closeFiles := func() error {
		failures := []error{}
		for _, file := range files {
			failures = append(failures, closeFile(file))
		}
		return errors.Join(failures...)
	}
	for _, upload := range source.Files {
		if upload.Field == "" || upload.Path == "" || strings.ContainsAny(upload.Field, "\r\n\x00") {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Multipart uploads require a valid field name and file path.", closeFiles())
		}
		file, err := opener(upload.Path)
		if err != nil {
			return nil, "", nothing, errors.Join(err, closeFiles())
		}
		files = append(files, file)
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	done := make(chan error, 1)
	go func() {
		var writeErr error
		for _, field := range source.Fields {
			if err := multipartWriter.WriteField(field.Name, field.Value); err != nil {
				writeErr = err
				break
			}
		}
		if writeErr == nil {
			for i, file := range files {
				if err := ctx.Err(); err != nil {
					writeErr = err
					break
				}
				part, err := multipartWriter.CreateFormFile(source.Files[i].Field, filepath.Base(source.Files[i].Path))
				if err != nil {
					writeErr = err
					break
				}
				if _, err := io.Copy(part, file); err != nil {
					writeErr = err
					break
				}
			}
		}
		if writeErr == nil {
			writeErr = multipartWriter.Close()
		}
		closeErr := writer.CloseWithError(writeErr)
		if errors.Is(writeErr, io.ErrClosedPipe) {
			writeErr = nil
		}
		done <- errors.Join(writeErr, closeErr)
	}()
	var once sync.Once
	var cleanupErr error
	cleanup := func() error {
		once.Do(func() {
			pipeErr := reader.Close()
			producerErr := <-done
			cleanupErr = errors.Join(pipeErr, producerErr, closeFiles())
		})
		return cleanupErr
	}
	return reader, contentType, cleanup, nil
}

// closeFile 将已由请求传输关闭的句柄视作已释放，不隐藏其他关闭错误。
// 正文 cleanup 可以在早期发送失败后再次调用，不制造误报的重复上传提示。
func closeFile(file *os.File) error {
	err := file.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
