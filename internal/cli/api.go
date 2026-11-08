package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	rawapi "github.com/Spring-Ocean/gitea-cli-so/internal/app/api"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
	"github.com/spf13/cobra"
)

type apiOptions struct {
	method      string
	queries     []string
	headers     []string
	include     bool
	data        string
	input       string
	uploads     []string
	form        []string
	output      string
	timeout     time.Duration
	maxResponse int64
}

// addAPICommand 提供任意端点／方法的原始请求与明确文件传输。
// 写方法复用默认拒绝确认，不按版本改写 body 或自动重放写入。
func addAPICommand(root *cobra.Command, r *runtime) {
	options := &apiOptions{}
	command := &cobra.Command{
		Use:         "api PATH",
		Short:       "Call any API endpoint with explicit raw request and response semantics",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"sotea.raw": "true"},
		RunE: func(command *cobra.Command, args []string) error {
			return r.runAPI(command, args[0], options)
		},
	}
	command.Flags().StringVar(&options.method, "method", "GET", "Use an explicit HTTP method")
	command.Flags().StringArrayVar(&options.queries, "query", nil, "Append key=value; repeat to preserve duplicate query keys")
	command.Flags().StringArrayVar(&options.headers, "header", nil, "Append a non-authentication Name: value header")
	command.Flags().BoolVar(&options.include, "include", false, "Include HTTP status and response headers")
	command.Flags().StringVar(&options.data, "data", "", "Send this raw UTF-8 request text")
	command.Flags().StringVar(&options.input, "input", "", "Stream raw bytes from a regular file")
	command.Flags().StringArrayVar(&options.uploads, "upload", nil, "Stream a multipart FIELD=FILE upload; repeat for multiple files")
	command.Flags().StringArrayVar(&options.form, "form", nil, "Add a multipart NAME=VALUE field")
	command.Flags().StringVar(&options.output, "output", "", "Download to a new file without replacement; - means stdout")
	command.Flags().DurationVar(&options.timeout, "timeout", 30*time.Second, "Set the complete HTTP request and response budget")
	command.Flags().Int64Var(&options.maxResponse, "max-response-bytes", 64<<20, "Bound the response stream, including file downloads")
	root.AddCommand(command)
}

// runAPI 验证来源和输入，确认写方法后执行一次原始请求。
// HTTP 错误仍保留原始 body；已接受写入的输出失败标记部分完成，文件完整接收后才落盘。
func (r *runtime) runAPI(command *cobra.Command, path string, options *apiOptions) (returnedErr error) {
	method := strings.ToUpper(options.method)
	read := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	acceptedWrite := false
	defer func() {
		if returnedErr != nil && acceptedWrite {
			returnedErr = app.Wrap(app.PartialFailure, "The API write was accepted, but its response or local output could not be completed. Check the server before repeating the write.", returnedErr)
		}
	}()
	if method == "" || options.timeout <= 0 || options.maxResponse <= 0 {
		return app.Wrap(app.InvalidArgument, "Specify a valid method, positive timeout, and positive response limit.", nil)
	}
	if options.output != "" && options.output != "-" {
		if _, err := os.Lstat(options.output); err == nil {
			return app.Wrap(app.Conflict, "The download target already exists. Choose another path.", nil)
		} else if !errors.Is(err, os.ErrNotExist) {
			return app.Wrap(app.IOError, "Unable to inspect the download target.", err)
		}
	}
	query := url.Values{}
	for _, pair := range options.queries {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return app.Wrap(app.InvalidArgument, "Each --query must be key=value.", nil)
		}
		query.Add(key, value)
	}
	header := http.Header{}
	for _, pair := range options.headers {
		key, value, ok := strings.Cut(pair, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.EqualFold(key, "Host") || strings.EqualFold(key, "Authorization") ||
			strings.EqualFold(key, "Proxy-Authorization") || strings.EqualFold(key, "Cookie") {
			return app.Wrap(app.InvalidArgument, "Host and authentication headers are controlled by the selected context.", nil)
		}
		header.Add(key, strings.TrimSpace(value))
	}
	sources := 0
	for _, provided := range []bool{command.Flags().Changed("data"), command.Flags().Changed("input"), len(options.uploads) > 0} {
		if provided {
			sources++
		}
	}
	if sources > 1 || (len(options.form) > 0 && len(options.uploads) == 0) {
		return app.Wrap(app.InvalidArgument, "Use only one of --data, --input, or --upload; --form requires multipart uploads.", nil)
	}
	if !read {
		if _, err := fmt.Fprintf(command.ErrOrStderr(), "API write preview: %s %q; input=%q; uploads=%d\n", method, path, options.input, len(options.uploads)); err != nil {
			return app.Wrap(app.IOError, "Unable to display the API request preview.", err)
		}
		if err := r.confirm(command, "Send this exact API request without automatic retries", r.options.yes); err != nil {
			return err
		}
	}
	body, contentType, cleanup, err := options.bodyContext(command.Context(), command.Flags().Changed("data"))
	if err != nil {
		return err
	}
	defer func() {
		if err := cleanup(); err != nil && returnedErr == nil {
			returnedErr = app.Wrap(app.IOError, "Unable to close the API request input.", err)
		}
	}()
	if header.Get("Content-Type") == "" && contentType != "" {
		header.Set("Content-Type", contentType)
	} else if len(options.uploads) > 0 && header.Get("Content-Type") != contentType {
		return app.Wrap(app.InvalidArgument, "Multipart Content-Type and boundary are generated from the upload fields.", nil)
	}
	t, _, service, err := r.resolve(command, r.input(false), false)
	if err != nil {
		return err
	}
	service.HTTPOptions = gitea.Options{
		Timeout:          options.timeout,
		MaxResponseBytes: options.maxResponse,
	}
	client, _, err := rawapi.Open(command.Context(), service, t, read)
	if err != nil {
		return err
	}
	defer client.Close()
	response, err := rawapi.Send(command.Context(), client, gitea.Request{
		Method:  method,
		Path:    path,
		Query:   query,
		Headers: header,
		Body:    body,
	})
	if err != nil {
		return err
	}
	acceptedWrite = !read && response.StatusCode >= 200 && response.StatusCode < 300
	defer func() {
		if err := response.Body.Close(); err != nil && returnedErr == nil {
			returnedErr = app.Wrap(app.IOError, "Unable to close the API response.", err)
		}
	}()
	statusErr := gitea.StatusError(response.StatusCode)
	responseInput := io.Reader(response.Body)
	if r.session != nil && (options.output == "" || options.output == "-" || statusErr != nil) {
		// 仅菜单尝试有界 JSON 展示；大响应和非 JSON 字节通过原流交接完整送出。
		prefix, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil {
			return app.Failure(err)
		}
		if len(prefix) <= 2<<20 && json.Valid(prefix) {
			decoder := json.NewDecoder(bytes.NewReader(prefix))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return app.Wrap(app.ServerError, "Unable to read the API response.", err)
			}
			render := renderer(command)
			render.Out = r.session
			render.Redactor = r.options.redactor
			if options.include {
				if err := render.Details(map[string]any{
					"http_status": response.StatusCode,
					"headers":     response.Header,
				}); err != nil {
					return err
				}
			}
			if err := render.Details(value); err != nil {
				return err
			}
			return statusErr
		}
		responseInput = io.MultiReader(bytes.NewReader(prefix), response.Body)
	}
	stream := func() error {
		if options.include {
			if _, err := fmt.Fprintf(command.OutOrStdout(), "HTTP %d\n", response.StatusCode); err != nil {
				return app.Wrap(app.IOError, "Unable to write the HTTP status.", err)
			}
			if err := response.Header.Write(command.OutOrStdout()); err != nil {
				return app.Wrap(app.IOError, "Unable to write response headers.", err)
			}
			if _, err := fmt.Fprintln(command.OutOrStdout()); err != nil {
				return app.Wrap(app.IOError, "Unable to finish response headers.", err)
			}
		}
		if options.output != "" && options.output != "-" && statusErr == nil {
			if err := gitea.Download(command.Context(), response.Body, options.output); err != nil {
				return err
			}
		} else if _, err := io.Copy(command.OutOrStdout(), responseInput); err != nil {
			return app.Failure(err)
		}
		return nil
	}
	if r.session != nil {
		if err := r.session.External(command.Context(), stream); err != nil {
			return err
		}
	} else if err := stream(); err != nil {
		return err
	}
	return statusErr
}

// body 为旧有调用方保留同步构造入口，实际能力由共享 API 服务提供。
// CLI 运行使用 bodyContext 传递取消；测试的调用者仍须执行 cleanup。
func (options *apiOptions) body(dataSet bool) (io.Reader, string, func() error, error) {
	return options.bodyContext(context.Background(), dataSet)
}

// bodyContext 将 CLI 参数转换为类型化正文来源，不在应用层读取终端。
// 互斥、普通文件、multipart 生命周期由共享入口处理，原始字节保持不变。
func (options *apiOptions) bodyContext(ctx context.Context, dataSet bool) (io.Reader, string, func() error, error) {
	source := rawapi.BodySource{File: options.input}
	if dataSet {
		source.Text = &options.data
	}
	nothing := func() error {
		return nil
	}
	for _, value := range options.form {
		name, text, ok := strings.Cut(value, "=")
		if !ok || name == "" {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Each --form must be NAME=VALUE.", nil)
		}
		source.Fields = append(source.Fields, rawapi.Field{Name: name, Value: text})
	}
	for _, value := range options.uploads {
		name, path, ok := strings.Cut(value, "=")
		if !ok || name == "" || path == "" {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Each --upload must be FIELD=FILE.", nil)
		}
		source.Files = append(source.Files, rawapi.Upload{Field: name, Path: path})
	}
	return rawapi.OpenBody(ctx, source)
}
