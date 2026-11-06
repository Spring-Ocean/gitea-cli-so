package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestDiagnosticProtocolAndSchema 保护初始化、封闭工具输入和结构化业务错误。
// 通过官方 SDK 真实消息往返，不把回调直接调用当作协议验证。
func TestDiagnosticProtocolAndSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls atomic.Int32
	server := New("test", func(ctx context.Context, input InstanceInput) app.Result {
		calls.Add(1)
		if input.Instance == "denied" {
			return app.NewResult(app.Context{}, nil, app.Wrap(app.PermissionDenied, "Access denied.", nil))
		}
		return app.NewResult(app.Context{Instance: input.Instance}, []app.Item{{
			Target: input.Instance,
			Status: app.Success,
			Data:   map[string]string{"evidence": "not_tested", "secret": "synthetic-secret"},
		}}, nil)
	}, output.NewRedactor("synthetic-secret"))
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	client := sdk.NewClient(&sdk.Implementation{
		Name:    "sotea-test",
		Version: "test",
	}, nil)
	connection, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	tools, err := connection.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "sotea_instance_info" || tools.Tools[0].OutputSchema == nil || !tools.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("diagnostic tool contract was not advertised: %v", err)
	}
	result, err := connection.CallTool(ctx, &sdk.CallToolParams{
		Name:      "sotea_instance_info",
		Arguments: map[string]any{"instance": "team"},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("structured success failed: %v", err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil || bytes.Contains(encoded, []byte("synthetic-secret")) {
		t.Fatal("MCP structured result leaked a secret")
	}
	before := calls.Load()
	result, err = connection.CallTool(ctx, &sdk.CallToolParams{
		Name:      "sotea_instance_info",
		Arguments: map[string]any{"instance": "team", "unexpected": true},
	})
	if err == nil && !result.IsError {
		t.Fatal("unknown tool properties were accepted")
	}
	if calls.Load() != before {
		t.Fatal("invalid tool arguments reached the application")
	}
	result, err = connection.CallTool(ctx, &sdk.CallToolParams{
		Name:      "sotea_instance_info",
		Arguments: map[string]any{"instance": "denied"},
	})
	if err != nil || !result.IsError || result.StructuredContent == nil {
		t.Fatal("business failure became a protocol error or lost structured content")
	}
}
