package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	rawapi "github.com/Spring-Ocean/gitea-cli-so/internal/app/api"
	"github.com/Spring-Ocean/gitea-cli-so/internal/app/session"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// apiInput 是原始 API 的封闭请求，不提供其他认证或绝对 URL。
// 正文来源互斥，multipart 文件仍使用同一启动授权根。
type apiInput struct {
	Instance  string      `json:"instance"`
	Account   string      `json:"account"`
	Method    string      `json:"method"`
	Path      string      `json:"path"`
	Query     url.Values  `json:"query"`
	Headers   http.Header `json:"headers"`
	Text      *string     `json:"body_text"`
	File      string      `json:"body_file"`
	Multipart *struct {
		Fields []rawapi.Field  `json:"fields"`
		Files  []rawapi.Upload `json:"files"`
	} `json:"multipart"`
}

// apiFileSeal 绑定原始文件身份和完整内容摘要，不持有长期句柄。
// 文件内容只在提交时进入受预算限制的私有快照。
type apiFileSeal struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	info   os.FileInfo
}

// source 将已校验 API 输入映射到共同正文构造器。
// 文件 opener 由真实使用方注入，不能按内部快照名字推导附件名称。
func (input apiInput) source() rawapi.BodySource {
	source := rawapi.BodySource{
		Text: input.Text,
		File: input.File,
	}
	if input.Multipart != nil {
		source.Fields = input.Multipart.Fields
		source.Files = input.Multipart.Files
	}
	return source
}

// validateAPIInput 校验正文互斥及共同请求规则，不打开或发送远端路径。
// multipart 字段名和 Content-Type 边界在 prepare 即拒绝，避免确认后更改请求。
func validateAPIInput(input apiInput) error {
	count := 0
	for _, present := range []bool{input.Text != nil, input.File != "", input.Multipart != nil} {
		if present {
			count++
		}
	}
	if count > 1 {
		return app.Wrap(app.InvalidArgument, "Choose only one of body_text, body_file or multipart.", nil)
	}
	if input.Multipart != nil {
		if len(input.Multipart.Files) == 0 {
			return app.Wrap(app.InvalidArgument, "Multipart requires at least one file.", nil)
		}
		for key := range input.Headers {
			if strings.EqualFold(key, "Content-Type") {
				return app.Wrap(app.InvalidArgument, "Multipart Content-Type is generated from the confirmed fields and files.", nil)
			}
		}
		for _, field := range input.Multipart.Fields {
			if field.Name == "" || strings.ContainsAny(field.Name, "\r\n\x00") {
				return app.Wrap(app.InvalidArgument, "Invalid multipart field name.", nil)
			}
		}
		for _, file := range input.Multipart.Files {
			if file.Field == "" || file.Path == "" || strings.ContainsAny(file.Field, "\r\n\x00") {
				return app.Wrap(app.InvalidArgument, "Invalid multipart upload field or path.", nil)
			}
		}
	}
	return rawapi.Validate(input.Method, input.Path, input.Query, input.Headers)
}

// apiRead 执行有界原始读取，保留 HTTP 状态和重复参数。
// 自定义 GET 端点是否有副作用取决于服务端，工具提示不当作授权保证。
func (h *Host) apiRead(ctx context.Context, arguments map[string]json.RawMessage) response {
	if !h.options.AllowRawAPI {
		return failed(app.Wrap(app.PermissionDenied, "Raw API tools require --allow-raw-api.", nil))
	}
	var input apiInput
	if err := decode(arguments, &input); err != nil {
		return failed(err)
	}
	if err := validateAPIInput(input); err != nil {
		return failed(err)
	}
	state, err := h.resolveInput(ctx, readInput{
		Instance: input.Instance,
		Account:  input.Account,
	}, false)
	if err != nil {
		return failed(err)
	}
	return h.sendAPI(ctx, state, input, input.source())
}

// prepareAPI 固定原始请求和授权文件摘要，不探测任意业务路径。
// 与专用业务不同，不承诺对象状态并发保护，未知版本仍保留原始 API 语义。
func (h *Host) prepareAPI(ctx context.Context, state session.State, params json.RawMessage) (*preparedOperation, error) {
	var input apiInput
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, app.Wrap(app.InvalidArgument, "Unable to decode the API request.", err)
	}
	if err := validateAPIInput(input); err != nil {
		return nil, err
	}
	paths := []string{}
	if input.File != "" {
		paths = append(paths, input.File)
	}
	if input.Multipart != nil {
		for _, file := range input.Multipart.Files {
			paths = append(paths, file.Path)
		}
	}
	seals := []apiFileSeal{}
	var total int64
	for _, path := range paths {
		if h.scope == nil {
			return nil, h.localAccessError()
		}
		file, err := h.scope.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			return nil, errors.Join(err, file.Close())
		}
		size, digest, readErr := digestFile(ctx, file, h.options.MaxFileBytes)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, err
		}
		total += size
		if total > h.options.MaxFileBytes {
			return nil, app.Wrap(app.ResourceLimit, "The API request's total file content exceeds the file budget.", nil)
		}
		seals = append(seals, apiFileSeal{
			Path:   path,
			Size:   size,
			SHA256: digest,
			info:   info,
		})
	}
	preview := map[string]any{
		"method": input.Method, "path": input.Path, "query": input.Query, "headers": input.Headers,
		"files": seals, "warning": "This fixes the exact request. It does not provide business-object concurrency protection.",
	}
	if input.Text != nil {
		digest := sha256.Sum256([]byte(*input.Text))
		preview["body_sha256"] = hex.EncodeToString(digest[:])
		if json.Valid([]byte(*input.Text)) {
			decoder := json.NewDecoder(strings.NewReader(*input.Text))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			preview["body"] = value
		} else {
			preview["body"] = *input.Text
		}
	}
	if input.Multipart != nil {
		preview["multipart"] = input.Multipart
	}
	operation := &preparedOperation{
		preview:  preview,
		eligible: 1,
		memory:   len(params)*2 + 8192,
	}
	operation.apply = func(ctx context.Context, observed func()) response {
		release, err := h.environment.Locks.Acquire(ctx, state.Target.Instance.URL+"\x00"+state.Target.Account+"\x00raw-api")
		if err != nil {
			return failed(err)
		}
		defer release()
		source := input.source()
		snapshots := map[string][]*os.File{}
		cleanups := []func() error{}
		cleanup := func() error {
			failures := []error{}
			for _, close := range cleanups {
				failures = append(failures, close())
			}
			return errors.Join(failures...)
		}
		for _, seal := range seals {
			original, err := h.scope.Open(seal.Path)
			if err != nil {
				return failed(errors.Join(err, cleanup()))
			}
			info, statErr := original.Stat()
			size, digest, readErr := digestFile(ctx, original, h.options.MaxFileBytes)
			if statErr != nil || readErr != nil || !os.SameFile(info, seal.info) || !info.ModTime().Equal(seal.info.ModTime()) || size != seal.Size || digest != seal.SHA256 {
				return failed(app.Wrap(app.PreconditionChanged, "An API source file changed after preview.", errors.Join(statErr, readErr, original.Close(), cleanup())))
			}
			frozen, close, freezeErr := h.scope.Freeze(ctx, original)
			closeErr := original.Close()
			if freezeErr != nil || closeErr != nil {
				return failed(errors.Join(freezeErr, closeErr, close(), cleanup()))
			}
			cleanups = append(cleanups, close)
			frozenSize, frozenDigest, inspectErr := digestFile(ctx, frozen, h.options.MaxFileBytes)
			if inspectErr != nil || frozenSize != seal.Size || frozenDigest != seal.SHA256 {
				return failed(app.Wrap(app.PreconditionChanged, "The fixed API snapshot differs from the confirmed content.", errors.Join(inspectErr, cleanup())))
			}
			snapshots[seal.Path] = append(snapshots[seal.Path], frozen)
		}
		source.Open = func(path string) (*os.File, error) {
			files := snapshots[path]
			if len(files) == 0 {
				return nil, app.Wrap(app.PathOutsideRoot, "This file was not included in the prepared API request.", nil)
			}
			file := files[0]
			snapshots[path] = files[1:]
			return file, nil
		}
		current := state
		current.Auth.HTTPOptions.OnWrite = observed
		current.Auth.HTTPOptions.Timeout = h.options.UploadTimeout
		result := h.sendAPI(ctx, current, input, source)
		if err := cleanup(); err != nil {
			result = businessResponse(current, result.Result, app.Wrap(app.PartialFailure, "The request finished but private input cleanup failed. Inspect its existing outcome before retrying.", err))
		}
		return result
	}
	return operation, nil
}

// digestFile 读取固定普通文件的完整摘要并恢复到开头，复制循环可取消。
// 超限或读取中元信息变化拒绝确认，不把前缀当作完整文件。
func digestFile(ctx context.Context, file *os.File, limit int64) (int64, string, error) {
	before, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	digest := sha256.New()
	buffer := make([]byte, 64<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		count, readErr := file.Read(buffer)
		size += int64(count)
		if size > limit {
			return 0, "", app.Wrap(app.ResourceLimit, "File content exceeds the configured budget.", nil)
		}
		if _, err := digest.Write(buffer[:count]); err != nil {
			return 0, "", err
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, "", readErr
		}
	}
	after, statErr := file.Stat()
	_, seekErr := file.Seek(0, io.SeekStart)
	if statErr != nil || seekErr != nil || before.Size() != size || after.Size() != size || !before.ModTime().Equal(after.ModTime()) {
		return 0, "", app.Wrap(app.PreconditionChanged, "The file changed while inspecting its content.", errors.Join(statErr, seekErr))
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

// sendAPI 使用共同校验／正文／单次发送服务，响应正文只读取有限前缀。
// HTTP 失败保留 body；已接受写入后的响应错误与普通读取错误分别分类。
func (h *Host) sendAPI(ctx context.Context, state session.State, input apiInput, source rawapi.BodySource) response {
	body, contentType, cleanup, err := rawapi.OpenBody(ctx, source)
	if err != nil {
		return failed(err)
	}
	input.Headers = input.Headers.Clone()
	if input.Headers == nil {
		input.Headers = http.Header{}
	}
	if contentType != "" && input.Headers.Get("Content-Type") == "" {
		input.Headers.Set("Content-Type", contentType)
	}
	read := input.Method == "GET" || input.Method == "HEAD" || input.Method == "OPTIONS"
	client, resolved, err := rawapi.Open(ctx, state.Auth, state.Target, read)
	if err != nil {
		return failed(errors.Join(err, cleanup()))
	}
	defer client.Close()
	state.Target = resolved
	remote, sendErr := rawapi.Send(ctx, client, gitea.Request{
		Method:  input.Method,
		Path:    input.Path,
		Query:   input.Query,
		Headers: input.Headers,
		Body:    body,
	})
	if sendErr != nil {
		return failed(errors.Join(sendErr, cleanup()))
	}
	budget := int64(h.options.MaxOutput / 8)
	prefix, readErr := io.ReadAll(io.LimitReader(remote.Body, budget+1))
	closeErr := remote.Body.Close()
	cleanupErr := cleanup()
	truncated := int64(len(prefix)) > budget
	if truncated {
		prefix = prefix[:budget]
	}
	accepted := !read && remote.StatusCode >= 200 && remote.StatusCode < 300
	resultErr := gitea.StatusError(remote.StatusCode)
	if err := errors.Join(readErr, closeErr, cleanupErr); err != nil {
		if accepted {
			resultErr = app.Wrap(app.ResultUnknown, "The API write was accepted but response handling failed. Inspect the server before retrying.", err)
		} else if resultErr == nil {
			resultErr = app.Wrap(app.IOError, "Unable to complete the API response.", err)
		}
	}
	encoding := "utf8"
	var value any = string(prefix)
	if !truncated && json.Valid(prefix) {
		decoder := json.NewDecoder(bytes.NewReader(prefix))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return failed(err)
		}
		encoding = "json"
	} else if !utf8.Valid(prefix) {
		encoding = "base64"
		value = base64.StdEncoding.EncodeToString([]byte(h.redactor.Text(string(prefix))))
	}
	payload := map[string]any{"status": remote.StatusCode, "headers": remote.Header, "body_encoding": encoding, "body": value}
	result := dataResponse(state, payload, resultErr)
	result.Meta = &metadata{}
	if truncated {
		result.Meta.Truncation = &truncationMetadata{
			Truncated:     true,
			OmittedFields: []string{"api.body_suffix"},
			Reason:        "Raw response exceeds the bounded MCP preview.",
			NextAction:    "Use sotea api to retrieve the full response; do not replay a write to retrieve its body.",
		}
	}
	// 复用业务脱敏后的对象作为 meta.api，避免响应 token／Cookie 进入协议。
	safe, err := h.safeResult(result.Result)
	if err != nil {
		return failed(err)
	}
	items := safe["items"].([]any)
	data := items[0].(map[string]any)["data"]
	encoded, err := json.Marshal(data)
	if err != nil {
		return failed(err)
	}
	result.Meta.API = encoded
	return result
}
