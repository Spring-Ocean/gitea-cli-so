// Package mcp 提供共享应用服务的本地 stdio 适配。
// P2 仅交付实例诊断原型，不提供写入、登录或任意 API 工具。
package mcp

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// InstanceInput 是诊断工具的封闭输入，不允许额外属性或任意文件参数。
// 未提供的目标由共享上下文解析器处理，不读取协议 stdin 补全参数。
type InstanceInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Registered instance name"`
	Account  string `json:"account,omitempty" jsonschema:"Explicit API account"`
	Refresh  bool   `json:"refresh,omitempty" jsonschema:"Refresh cached server version"`
}

// InstanceInfo 定义诊断应用服务边界，不依赖 CLI 文本或子进程。
// 回调负责每次重新读取配置、验证身份并遵守请求取消。
type InstanceInfo func(context.Context, InstanceInput) app.Result

// New 创建仅含 sotea_instance_info 的 P2 原型服务器。
// 协议固定协商 2025-11-25／2025-06-18，业务错误通过 isError 和结构化结果返回。
func New(version string, diagnose InstanceInfo, redactor *output.Redactor) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "sotea",
		Version: version,
	}, &sdk.ServerOptions{SupportedProtocolVersions: []string{"2025-11-25", "2025-06-18"}})
	readers := make(chan struct{}, 4)
	sdk.AddTool(server, &sdk.Tool{
		Name:        "sotea_instance_info",
		Description: "Inspect the server version and separate detection, API definition, and runtime-test evidence. This P2 prototype is read-only.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, request *sdk.CallToolRequest, input InstanceInput) (*sdk.CallToolResult, app.Result, error) {
		var result app.Result
		select {
		case readers <- struct{}{}:
			defer func() {
				<-readers
			}()
			result = diagnose(ctx, input)
		case <-ctx.Done():
			result = app.NewResult(app.Context{}, nil, app.Failure(ctx.Err()))
		}
		var buffer bytes.Buffer
		render := output.Renderer{
			Out:      &buffer,
			Redactor: redactor,
		}
		if err := render.JSON(result); err != nil {
			result = app.NewResult(app.Context{}, nil, err)
		} else if err := json.Unmarshal(buffer.Bytes(), &result); err != nil {
			result = app.NewResult(app.Context{}, nil, app.Wrap(app.IOError, "Unable to encode the diagnostic result.", err))
		}
		return &sdk.CallToolResult{
			IsError: result.ExitCode() != 0,
			Content: []sdk.Content{&sdk.TextContent{Text: "Instance diagnostic completed. Inspect the structured result for status and evidence."}},
		}, result, nil
	})
	return server
}
