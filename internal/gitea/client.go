// Package gitea 提供不依赖 CLI 或认证生命周期的 HTTP 基础层。
// 它保留原始状态及内容，业务模型和版本适配由上层处理。
package gitea

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// Options 定义一个固定实例的传输策略。
// ProxyURL 为空时使用系统代理，direct 禁用代理；读取重试默认关闭，最多三次。
type Options struct {
	BaseURL          string
	CAFile           string
	ProxyURL         string
	Timeout          time.Duration
	MaxResponseBytes int64
	ReadRetries      int
	Authenticate     func(*http.Request) error
	// OnWrite 在一次非读取发送前通知入口，不包含地址、请求体或认证材料。
	OnWrite func()
}

// Client 固定请求实例根地址并维护独立的读取及写入连接池。
// 并发调用共享只读配置；Authenticate 必须可并发调用且只注入当前实例身份。
type Client struct {
	root    *url.URL
	read    *http.Client
	write   *http.Client
	options Options
}

// Request 描述相对于 /api/v1 的一次请求。
// Headers、Query 和 Body 保留调用者语义，Body 由 HTTP 传输读取；不自动改写版本字段。
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Body    io.Reader
}

// Response 保留原始 HTTP 状态、头和有界响应流。
// 调用者必须关闭 Body 并检查读取错误，禁止把截断内容视为完整响应。
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// New 创建固定实例客户端，默认超时 30 秒、响应上限 1 MiB。
// 地址不能包含凭证、query 或 fragment；自定义 CA 加到系统信任集，TLS 始终验证。
func New(options Options) (*Client, error) {
	root, err := url.Parse(options.BaseURL)
	if err != nil || root.Hostname() == "" || root.User != nil || root.RawQuery != "" || root.Fragment != "" ||
		(root.Scheme != "http" && root.Scheme != "https") || !safePath(root.EscapedPath()) {
		return nil, app.Wrap(app.InvalidArgument, "Invalid instance base URL.", nil)
	}
	if options.ReadRetries < 0 || options.ReadRetries > 3 || options.Timeout < 0 || options.MaxResponseBytes < 0 {
		return nil, app.Wrap(app.InvalidArgument, "Invalid HTTP timeout, response limit, or retry count.", nil)
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 1 << 20
	}
	root.Path = strings.TrimRight(root.Path, "/") + "/api/v1"
	root.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if options.CAFile != "" {
		contents, err := os.ReadFile(options.CAFile)
		if err != nil {
			return nil, app.Wrap(app.TLSError, "Unable to read the private CA file.", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, app.Wrap(app.TLSError, "Unable to load system trust roots.", err)
		}
		if !roots.AppendCertsFromPEM(contents) {
			return nil, app.Wrap(app.TLSError, "The CA file contains no usable certificates.", nil)
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	if options.ProxyURL == "direct" {
		transport.Proxy = nil
	} else if options.ProxyURL != "" {
		proxy, err := url.Parse(options.ProxyURL)
		if err != nil || proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https") {
			return nil, app.Wrap(app.InvalidArgument, "Invalid HTTP proxy URL.", nil)
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	writeTransport := transport.Clone()
	// 写入使用新的 HTTP/1 连接，阻止连接池和幂等头触发隐含重放。
	writeTransport.DisableKeepAlives = true
	writeTransport.ForceAttemptHTTP2 = false
	writeTransport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	client := &Client{root: root, options: options}
	client.read = &http.Client{Transport: transport, Timeout: options.Timeout, CheckRedirect: client.redirect}
	client.write = &http.Client{Transport: writeTransport, Timeout: options.Timeout, CheckRedirect: client.redirect}
	return client, nil
}

// Do 执行一次请求，HTTP 非成功状态仍以原始 Response 返回。
// 仅无 body 的读取可执行有界重试；发送写请求头后断连返回 RESULT_UNKNOWN，不重放写入。
func (c *Client) Do(ctx context.Context, spec Request) (result *Response, returnedErr error) {
	ctx, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer func() {
		if result == nil {
			cancel()
		}
	}()
	if !safePath(spec.Path) {
		return nil, app.Wrap(app.InvalidArgument, "API paths must not contain traversal or control characters.", nil)
	}
	resource, err := url.Parse(spec.Path)
	if err != nil || resource.IsAbs() || resource.Host != "" || resource.RawQuery != "" || resource.Fragment != "" || strings.HasPrefix(spec.Path, "//") {
		return nil, app.Wrap(app.InvalidArgument, "Use an API-relative path and separate query parameters.", nil)
	}
	address := *c.root
	address.Path += "/" + strings.TrimPrefix(resource.Path, "/")
	address.RawPath = c.root.EscapedPath() + "/" + strings.TrimPrefix(resource.EscapedPath(), "/")
	address.RawQuery = spec.Query.Encode()
	method := spec.Method
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)
	read := isRead(method)
	attempts := 1
	if read && spec.Body == nil {
		attempts += c.options.ReadRetries
	}
	for attempt := 0; attempt < attempts; attempt++ {
		sent := &atomic.Bool{}
		traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			WroteHeaders: func() {
				sent.Store(true)
			},
		})
		request, err := http.NewRequestWithContext(traced, method, address.String(), spec.Body)
		if err != nil {
			return nil, app.Wrap(app.InvalidArgument, "Unable to construct the HTTP request.", err)
		}
		request.Header = spec.Headers.Clone()
		if request.Header == nil {
			request.Header = http.Header{}
		}
		if c.options.Authenticate != nil {
			if err := c.options.Authenticate(request); err != nil {
				return nil, app.Failure(err)
			}
		}
		transport := c.read
		if !read {
			transport = c.write
			request.GetBody = nil
		}
		if !read && c.options.OnWrite != nil {
			c.options.OnWrite()
		}
		response, requestErr := transport.Do(request)
		retry := attempt+1 < attempts && ctx.Err() == nil && (requestErr != nil ||
			response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504)
		if retry {
			if response != nil {
				response.Body.Close()
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, transportError(ctx, ctx.Err(), false)
			case <-timer.C:
				continue
			}
		}
		if requestErr != nil {
			return nil, transportError(ctx, requestErr, !read && sent.Load())
		}
		return &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header.Clone(),
			Body: &limitedBody{
				ReadCloser: response.Body,
				remaining:  c.options.MaxResponseBytes,
				cancel:     cancel,
				write:      !read,
			},
		}, nil
	}
	return nil, app.Wrap(app.NetworkError, "HTTP request failed.", nil)
}

// Close 释放两个传输池的空闲连接。
// 不中断正在执行的请求；应用退出时还应取消对应调用的 context。
func (c *Client) Close() {
	c.read.CloseIdleConnections()
	c.write.CloseIdleConnections()
}

// redirect 只允许读取在原实例 API 根下进行有限跳转。
// 写请求及跨来源跳转保留原始 3xx，不发送下一次请求或转发认证。
func (c *Client) redirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 || !isRead(via[0].Method) || request.URL.Scheme != c.root.Scheme ||
		!strings.EqualFold(request.URL.Host, c.root.Host) || request.URL.User != nil ||
		!safePath(request.URL.EscapedPath()) ||
		(request.URL.Path != c.root.Path && !strings.HasPrefix(request.URL.Path, c.root.Path+"/")) {
		return http.ErrUseLastResponse
	}
	return nil
}

// ValidatePath 校验相对于实例 API 根的请求路径，不执行网络访问。
// 拒绝绝对 URL、认证地址、内嵌查询和多重编码逃逸；query 必须单独提供。
func ValidatePath(path string) error {
	resource, err := url.Parse(path)
	if !safePath(path) || err != nil || resource.IsAbs() || resource.Host != "" || resource.RawQuery != "" || resource.Fragment != "" || strings.HasPrefix(path, "//") {
		return app.Wrap(app.InvalidArgument, "Use an API-relative path without traversal, embedded query, or an absolute URL.", err)
	}
	return nil
}

// safePath 对多重转义进行有限解码并拒绝逃逸片段。
// 原始路径仍交给 net/url 编码，合法的文件名和路径段不会被重新解释成主机名。
func safePath(path string) bool {
	for count := 0; count < 5; count++ {
		if strings.ContainsAny(path, "\\\x00\r\n") {
			return false
		}
		for _, segment := range strings.Split(path, "/") {
			if segment == "." || segment == ".." {
				return false
			}
		}
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return false
		}
		if decoded == path {
			return true
		}
		path = decoded
	}
	return false
}

// isRead 集中定义客户端的读取分类。
// 该分类控制重试，不保证任意自定义服务端端点没有副作用。
func isRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// transportError 根据发送证据区分读取失败和写入结果未知。
// TLS 与网络错误保留 cause 供诊断，面向用户的摘要不包含 URL 或认证。
func transportError(ctx context.Context, err error, writeSent bool) error {
	if writeSent {
		return app.Wrap(app.ResultUnknown, "The write outcome is unknown. Check the server before retrying.", err)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return app.Wrap(app.Cancelled, "HTTP request cancelled.", ctx.Err())
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return app.Wrap(app.TLSError, "TLS certificate verification failed. Check the configured CA.", err)
	}
	return app.Wrap(app.NetworkError, "HTTP request failed or timed out.", err)
}

// limitedBody 在保持流式读取的同时检测响应上限。
// 达到精确长度后额外探测一个字节，区分完整响应和被截断内容。
type limitedBody struct {
	io.ReadCloser
	remaining int64
	exceeded  bool
	write     bool
	cancel    context.CancelFunc
}

// Close 关闭底层响应并释放本次请求的总超时计时器。
// 调用者仍应检查读取和关闭结果，取消不会证明任何写入已回滚。
func (body *limitedBody) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

// Read 返回上限内的响应字节，超限返回可识别错误。
// 调用方即使已得到部分数据，也必须处理错误而不能报告完整成功。
func (body *limitedBody) Read(buffer []byte) (int, error) {
	if body.exceeded {
		return 0, app.Wrap(app.ResponseTooLarge, "HTTP response exceeded the configured limit.", nil)
	}
	limit := int64(len(buffer))
	if limit > body.remaining+1 {
		limit = body.remaining + 1
	}
	count, err := body.ReadCloser.Read(buffer[:limit])
	if int64(count) > body.remaining {
		count = int(body.remaining)
		body.remaining = 0
		body.exceeded = true
		return count, app.Wrap(app.ResponseTooLarge, "HTTP response exceeded the configured limit.", nil)
	}
	body.remaining -= int64(count)
	if err != nil && err != io.EOF {
		if body.write {
			return count, app.Wrap(app.ResultUnknown, "The write response was interrupted. Check the server before retrying.", err)
		}
		if errors.Is(err, context.Canceled) {
			return count, app.Wrap(app.Cancelled, "HTTP response cancelled.", err)
		}
		return count, app.Wrap(app.NetworkError, "HTTP response was interrupted or timed out.", err)
	}
	return count, err
}
