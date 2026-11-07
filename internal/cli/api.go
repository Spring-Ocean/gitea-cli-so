package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
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
	body, contentType, cleanup, err := options.body(command.Flags().Changed("data"))
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
	var client *gitea.Client
	if t.Registered {
		client, _, err = service.Open(command.Context(), t, read)
	} else if os.Getenv("SOTEA_TOKEN") != "" || t.Account != "" {
		return app.Wrap(app.AuthRequired, "Confirm this instance using auth login before sending credentials.", nil)
	} else {
		client, err = gitea.New(gitea.Options{
			BaseURL:          t.Instance.URL,
			CAFile:           t.Instance.CAPath,
			ProxyURL:         t.Instance.Proxy,
			Timeout:          options.timeout,
			MaxResponseBytes: options.maxResponse,
		})
	}
	if err != nil {
		return err
	}
	defer client.Close()
	response, err := client.Do(command.Context(), gitea.Request{
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

// body 在不改变用户字节的前提下构造原始文件或流式 multipart 请求。
// cleanup 必须调用，负责关闭管道、等待生产者及释放已打开文件。
func (options *apiOptions) body(dataSet bool) (io.Reader, string, func() error, error) {
	nothing := func() error {
		return nil
	}
	if dataSet {
		if !utf8.ValidString(options.data) {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "--data must contain valid UTF-8; use --input for arbitrary bytes.", nil)
		}
		contentType := "application/octet-stream"
		if json.Valid([]byte(options.data)) {
			contentType = "application/json"
		}
		return strings.NewReader(options.data), contentType, nothing, nil
	}
	if options.input != "" {
		file, err := openRegular(options.input)
		if err != nil {
			return nil, "", nothing, err
		}
		return file, "application/octet-stream", func() error {
			err := file.Close()
			if errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}, nil
	}
	if len(options.uploads) == 0 {
		return nil, "", nothing, nil
	}
	type upload struct {
		field string
		file  *os.File
	}
	files := []upload{}
	closeFiles := func() error {
		failures := []error{}
		for _, item := range files {
			failures = append(failures, item.file.Close())
		}
		return errors.Join(failures...)
	}
	for _, value := range options.form {
		key, _, ok := strings.Cut(value, "=")
		if !ok || key == "" || strings.ContainsAny(key, "\r\n\x00") {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Each --form must be NAME=VALUE with a valid field name.", nil)
		}
	}
	for _, value := range options.uploads {
		field, path, ok := strings.Cut(value, "=")
		if !ok || field == "" || strings.ContainsAny(field, "\r\n\x00") {
			return nil, "", nothing, app.Wrap(app.InvalidArgument, "Each --upload must be FIELD=FILE with a valid field name.", closeFiles())
		}
		file, err := openRegular(path)
		if err != nil {
			return nil, "", nothing, errors.Join(err, closeFiles())
		}
		files = append(files, upload{
			field: field,
			file:  file,
		})
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	done := make(chan error, 1)
	go func() {
		var writeErr error
		for _, value := range options.form {
			key, field, ok := strings.Cut(value, "=")
			if !ok || key == "" {
				writeErr = app.Wrap(app.InvalidArgument, "Each --form must be NAME=VALUE.", nil)
				break
			}
			if err := multipartWriter.WriteField(key, field); err != nil {
				writeErr = err
				break
			}
		}
		if writeErr == nil {
			for _, item := range files {
				part, err := multipartWriter.CreateFormFile(item.field, filepath.Base(item.file.Name()))
				if err != nil {
					writeErr = err
					break
				}
				if _, err := io.Copy(part, item.file); err != nil {
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
	return reader, contentType, func() error {
		closeErr := reader.Close()
		writeErr := <-done
		return errors.Join(closeErr, writeErr, closeFiles())
	}, nil
}
