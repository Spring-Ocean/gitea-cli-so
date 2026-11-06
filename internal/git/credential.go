package git

import (
	"context"
	"errors"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Credential 是 Git credential 协议的查询上下文与返回值。
// Password 永不序列化；调用方必须用实际 API 身份验证 Username 的映射。
type Credential struct {
	Protocol string
	Host     string
	Path     string
	Username string
	Password string `json:"-"`
}

// Credential 执行 fill、approve 或 reject，并捕获 helper 的敏感输出。
// directory 指定仓库配置上下文，interactive 只供显式登录使用；返回错误不含原始 helper 输出。
func (r Runner) Credential(ctx context.Context, directory, operation string, credential Credential, interactive bool) (Credential, error) {
	if operation != "fill" && operation != "approve" && operation != "reject" {
		return Credential{}, app.Wrap(app.InvalidArgument, "Unsupported Git credential operation.", nil)
	}
	if (credential.Protocol != "https" && credential.Protocol != "http") || credential.Host == "" {
		return Credential{}, app.Wrap(app.InvalidArgument, "HTTP credentials require a protocol and host.", nil)
	}
	fields := [][2]string{
		{"protocol", credential.Protocol},
		{"host", credential.Host},
		{"path", credential.Path},
		{"username", credential.Username},
		{"password", credential.Password},
	}
	var input strings.Builder
	for _, field := range fields {
		if strings.ContainsAny(field[1], "\r\n\x00") {
			return Credential{}, app.Wrap(app.InvalidArgument, "Credential fields must not contain control delimiters.", nil)
		}
		if field[1] != "" {
			input.WriteString(field[0] + "=" + field[1] + "\n")
		}
	}
	input.WriteByte('\n')
	result, err := r.capture(ctx, directory, []string{"credential", operation}, strings.NewReader(input.String()), interactive)
	if err != nil {
		var failure *app.Error
		if errors.As(err, &failure) {
			return Credential{}, err
		}
		return Credential{}, app.Wrap(app.AuthRequired, "Git credentials are unavailable. Sign in explicitly or provide an API token.", err)
	}
	if operation != "fill" {
		return Credential{}, nil
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	if values["password"] == "" {
		return Credential{}, app.Wrap(app.AuthRequired, "Git did not return a usable credential.", nil)
	}
	return Credential{
		Protocol: values["protocol"],
		Host:     values["host"],
		Path:     values["path"],
		Username: values["username"],
		Password: values["password"],
	}, nil
}
