package gitea

import (
	"encoding/json"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"io"
)

// StatusError 将业务 HTTP 失败转换为稳定类别，不回传原始服务端 body。
// 通用 API 不调用此函数，以便保留原始状态与响应内容。
func StatusError(status int) error {
	if status >= 200 && status < 300 {
		return nil
	}
	code, message := app.ServerError, "The server returned an unsuccessful response."
	switch status {
	case 401:
		code, message = app.AuthRequired, "Authentication is required or the credential has expired. Run sotea auth login."
	case 403:
		code, message = app.PermissionDenied, "The server denied this operation. Check account permissions and token scopes."
	case 404:
		code, message = app.NotFound, "The requested resource is missing or not visible to this account."
	case 409:
		code, message = app.Conflict, "The server reported a conflict."
	}
	return &app.Error{Code: code, Message: message, HTTPStatus: status}
}

// Decode 读取完整有界 JSON 并保留数字精度，关闭响应体。
// 非成功状态按业务错误返回；多份 JSON 或截断内容不会伪装为成功。
func Decode(response *Response, destination any) error {
	defer response.Body.Close()
	if err := StatusError(response.StatusCode); err != nil {
		return err
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		if failure, ok := err.(*app.Error); ok {
			return failure
		}
		return app.Wrap(app.ServerError, "The server returned invalid or incomplete JSON.", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if failure, ok := err.(*app.Error); ok {
			return failure
		}
		return app.Wrap(app.ServerError, "The server response contains trailing or incomplete JSON.", err)
	}
	return nil
}
