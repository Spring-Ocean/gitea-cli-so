// Package api 提供 CLI 与 MCP 共用的显式原始请求校验和单次发送。
// 不读取终端、不确认写入、不改写业务版本字段；响应流归调用者关闭。
package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/auth"
	target "github.com/Spring-Ocean/gitea-cli-so/internal/context"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// Validate 检查原始请求路径、重复查询参数和自定义头的来源边界。
// 不发送网络请求；认证／sudo／传输 framing 由所选身份和 HTTP 传输控制。
func Validate(method, path string, query url.Values, headers http.Header) error {
	if method == "" || strings.ContainsAny(method, " \t\r\n\x00") {
		return app.Wrap(app.InvalidArgument, "Specify a valid HTTP method.", nil)
	}
	if err := gitea.ValidatePath(path); err != nil {
		return err
	}
	for key := range query {
		switch strings.ToLower(strings.ReplaceAll(key, "-", "_")) {
		case "token", "access_token", "refresh_token", "password", "client_secret", "authorization", "sudo":
			return app.Wrap(app.InvalidArgument, "Authentication and impersonation query parameters are controlled by the selected identity.", nil)
		}
	}
	for key, values := range headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t\r\n\x00:") {
			return app.Wrap(app.InvalidArgument, "HTTP header names must be valid tokens.", nil)
		}
		switch strings.ToLower(key) {
		case "host", "authorization", "proxy-authorization", "cookie", "sudo", "x-gitea-otp", "x-gogs-otp", "content-length", "transfer-encoding", "connection", "upgrade", "trailer", "te":
			return app.Wrap(app.InvalidArgument, "Authentication, impersonation, Host and framing headers cannot be overridden.", nil)
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n\x00") {
				return app.Wrap(app.InvalidArgument, "HTTP header values cannot contain control separators.", nil)
			}
		}
	}
	return nil
}

// Open 为已确认身份或显式匿名来源打开原始 API 客户端。
// 返回客户端必须 Close；此方法不探测任意业务路径，也不要求已研究版本。
func Open(ctx context.Context, authentication auth.Service, t target.Target, read bool) (*gitea.Client, target.Target, error) {
	if t.Registered {
		return authentication.Open(ctx, t, read)
	}
	getenv := authentication.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("SOTEA_TOKEN") != "" || t.Account != "" {
		return nil, t, app.Wrap(app.AuthRequired, "Confirm this instance with auth login before sending credentials.", nil)
	}
	options := authentication.HTTPOptions
	options.BaseURL = t.Instance.URL
	options.CAFile = t.Instance.CAPath
	options.ProxyURL = t.Instance.Proxy
	client, err := gitea.New(options)
	return client, t, err
}

// Send 验证并发送一次原始请求，HTTP 失败仍保留响应流。
// 调用者负责关闭 Response.Body；写请求不自动重试或跟随重定向重放。
func Send(ctx context.Context, client *gitea.Client, request gitea.Request) (*gitea.Response, error) {
	if err := Validate(request.Method, request.Path, request.Query, request.Headers); err != nil {
		return nil, err
	}
	return client.Do(ctx, request)
}
