package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// wire 将同一 envelope 放进结构化内容与 JSON 文本副本并测量最终预算。
// 调用方不得在测量后追加远端字段；错误作为稳定业务故障处理。
func (h *Host) wire(result response) (*sdk.CallToolResult, map[string]any, int, error) {
	value, err := h.safeResult(result.Result)
	if err != nil {
		return nil, nil, 0, err
	}
	if result.Meta != nil {
		encoded, err := json.Marshal(result.Meta)
		if err != nil {
			return nil, nil, 0, err
		}
		var meta map[string]any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&meta); err != nil {
			return nil, nil, 0, err
		}
		value["meta"] = meta
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, nil, 0, err
	}
	summary := "Operation completed successfully."
	if result.Status != app.Success {
		summary = fmt.Sprintf("Operation status: %s. Inspect every target and error before continuing.", result.Status)
	}
	if result.Meta != nil && result.Meta.Plan != nil {
		switch result.Meta.Plan.State {
		case "prepared":
			summary = "Write prepared. Review every target and change before using the confirmation token."
		case "executing":
			summary = "Write is still executing. Query its status; do not prepare a replacement."
		case "expired", "invalidated":
			summary = "This plan cannot be committed. Inspect the reason before preparing a new request."
		}
	}
	content := &sdk.CallToolResult{
		StructuredContent: json.RawMessage(encoded),
		IsError:           result.ExitCode() != 0,
		Content:           []sdk.Content{&sdk.TextContent{Text: summary}, &sdk.TextContent{Text: string(encoded)}},
	}
	final, err := json.Marshal(content)
	return content, value, len(final), err
}

// reply 输出有界结果，列表或写预览超限时给出完整的错误而非假分页。
// 已完成写入先压缩可省略远端数据，保留全部目标、步骤状态与资源编号。
func (h *Host) reply(result response, name string) (*sdk.CallToolResult, map[string]any) {
	content, value, size, err := h.wire(result)
	if err == nil && size <= h.options.MaxOutput {
		return content, value
	}
	if err == nil && name != "write_prepare" && !isListTool(name) {
		result = compactResponse(result)
		content, value, size, err = h.wire(result)
		if err == nil && size <= h.options.MaxOutput {
			return content, value
		}
	}
	failure := failed(app.Wrap(app.ResponseTooLarge, "The result exceeds the MCP output budget. For a list, use a smaller limit and restart at page=1; use the CLI for complete large content.", err))
	if name == "write_commit" || name == "write_status" {
		// 最小执行结果在 prepare 时预留；异常输出故障也不能建议重复写入。
		failure = compactResponse(result)
		failure.Error = app.Wrap(app.PartialFailure, "The operation result could not be fully displayed. Inspect retained target states and query the server before retrying.", err)
		failure.Status = app.Partial
	}
	content, value, _, encodeErr := h.wire(failure)
	if encodeErr != nil {
		// 仅使用字面量的最后编码边界，不包含用户或服务端数据。
		value = map[string]any{"schema_version": 1, "status": "failed", "context": map[string]any{}, "items": []any{}, "error": map[string]any{"code": "IO_ERROR", "message": "Unable to encode the MCP result."}}
		encoded := []byte(`{"schema_version":1,"status":"failed","context":{},"items":[],"error":{"code":"IO_ERROR","message":"Unable to encode the MCP result."}}`)
		content = &sdk.CallToolResult{
			StructuredContent: json.RawMessage(encoded),
			IsError:           true,
			Content:           []sdk.Content{&sdk.TextContent{Text: string(encoded)}},
		}
	}
	return content, value
}

// isListTool 区分完整分页页与允许裁剪的详情正文。
// PR 文件／提交同样属于列表，不能为了预算无声丢失页面内资源。
func isListTool(name string) bool {
	return strings.HasSuffix(name, "_list") || name == "pr_files" || name == "pr_commits"
}

// compactResponse 深拷贝后仅保留执行结构及可定位资源字段。
// 不修改原计划、缓存或调用者对象，避免并发状态查询互相破坏。
func compactResponse(result response) response {
	encoded, err := json.Marshal(result)
	if err != nil {
		return result
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var copy response
	if err := decoder.Decode(&copy); err != nil {
		return result
	}
	for i := range copy.Items {
		copy.Items[i].Data = compactValue(copy.Items[i].Data)
	}
	if copy.Meta == nil {
		copy.Meta = &metadata{}
	}
	if len(copy.Meta.API) > 0 {
		var api map[string]any
		if err := json.Unmarshal(copy.Meta.API, &api); err == nil {
			delete(api, "body")
			delete(api, "headers")
			if encoded, err := json.Marshal(api); err == nil {
				copy.Meta.API = encoded
			}
		}
	}
	copy.Meta.Truncation = &truncationMetadata{
		Truncated:     true,
		OmittedFields: []string{"nonessential_remote_data"},
		Reason:        "Remote detail exceeds the tool output budget.",
		NextAction:    "Use the corresponding CLI command for complete remote content; retained write states must not be replayed.",
	}
	return copy
}

// compactValue 保留所有步骤及资源身份，省略大正文和任意扩展详情。
// 不裁断 JSON、不修改状态码，也不把未知或未执行目标过滤掉。
func compactValue(value any) any {
	switch value := value.(type) {
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = compactValue(item)
		}
		return result
	case map[string]any:
		result := map[string]any{}
		for key, field := range value {
			switch key {
			case "body":
				if _, ok := field.(map[string]any); ok {
					result[key] = compactValue(field)
				}
			case "steps", "resource", "issue", "release", "asset", "items", "data":
				result[key] = compactValue(field)
			case "id", "number", "target", "status", "error", "state", "tag_name", "sha", "html_url", "draft", "prerelease", "page", "limit", "has_more":
				result[key] = field
			}
		}
		return result
	default:
		return value
	}
}

// retainedResult 在进入长驻结果缓存前再次执行共同脱敏，保留稳定数字精度。
// 不持久保留服务端返回的 token 字段；计划确认凭据不放入终态结果。
func (h *Host) retainedResult(result response) response {
	safe, err := h.safeResult(result.Result)
	if err != nil {
		return failed(err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return failed(app.Wrap(app.IOError, "Unable to retain a safe operation result.", err))
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var retained app.Result
	if err := decoder.Decode(&retained); err != nil {
		return failed(app.Wrap(app.IOError, "Unable to decode the retained operation result.", err))
	}
	result.Result = retained
	return result
}
