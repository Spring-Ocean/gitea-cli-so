package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestModelGuidanceDiscovery 保护模型从初始化和发现结果读取字段契约。
// 不锁定整段文案；核对真实工具描述、Release 分支语义和示例均可见且符合 Schema。
func TestModelGuidanceDiscovery(t *testing.T) {
	_, _, client := newProtocolFixture(t, Options{AllowWrite: true})
	instructions := client.InitializeResult().Instructions
	for _, term := range []string{"params.tag", "params.target", "tag_name", "target_commitish", "write_status"} {
		if !strings.Contains(instructions, term) {
			t.Fatalf("initialize instructions omit %s", term)
		}
	}
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var prepare *sdk.Tool
	for _, tool := range listed.Tools {
		if tool.Name == "sotea_write_prepare" {
			prepare = tool
			break
		}
	}
	if prepare == nil || !strings.Contains(prepare.Description, "params.tag") {
		t.Fatal("write preparation lacks discoverable release field guidance")
	}
	encoded, err := json.Marshal(prepare.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var discovered struct {
		Examples []map[string]any
		OneOf    []struct {
			Properties struct {
				Action struct {
					Const string
				}
				Params struct {
					Properties map[string]struct {
						Description string
					}
				}
			}
		}
	}
	if err := json.Unmarshal(encoded, &discovered); err != nil {
		t.Fatal(err)
	}
	if len(discovered.Examples) == 0 {
		t.Fatal("write preparation exposes no complete examples")
	}
	for _, example := range discovered.Examples {
		callProtocolTool(t, client, "write_prepare", example)
	}
	foundRelease := false
	for _, branch := range discovered.OneOf {
		if branch.Properties.Action.Const != "release.create" {
			continue
		}
		foundRelease = true
		for _, field := range []string{"tag", "target"} {
			if branch.Properties.Params.Properties[field].Description == "" {
				t.Fatalf("release.create field %s lacks meaning", field)
			}
		}
	}
	if !foundRelease {
		t.Fatal("release.create branch missing")
	}
}

// TestRawReleaseFieldsRejectedBeforePreparation 保护错误 API 字段不能被悄悄改成写入意图。
// 同一 request_id 在 Schema 拒绝后仍可用规范字段进入应用层，未生成可提交计划或远端写。
func TestRawReleaseFieldsRejectedBeforePreparation(t *testing.T) {
	fixture, host, client := newProtocolFixture(t, Options{AllowWrite: true})
	arguments := map[string]any{
		"request_id": "release-invalid-fields",
		"action":     "release.create",
		"context":    map[string]any{"repo": "team/repo"},
	}
	for _, params := range []map[string]any{
		{
			"tag_name":         "v1.2.3",
			"target_commitish": "main",
		},
		{
			"tag":              "v1.2.3",
			"target":           "main",
			"tag_name":         "v1.2.3",
			"target_commitish": "main",
		},
	} {
		arguments["params"] = params
		result, err := client.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "sotea_write_prepare",
			Arguments: arguments,
		})
		if err == nil && !result.IsError {
			t.Fatal("raw API aliases accepted")
		}
		if len(host.plans.requests) != 0 {
			t.Fatal("invalid schema registered a write intent")
		}
	}
	arguments["params"] = map[string]any{
		"tag":    "v1.2.3",
		"target": "main",
	}
	// 夹具不提供 Release 端点，规范字段应进入领域预检并以结构化业务错误返回。
	// callProtocolTool 会拒绝协议校验错误，因此可验证修正后的参数确实被接受。
	response := callProtocolTool(t, client, "write_prepare", arguments)
	if response.Error == nil || response.Meta != nil && response.Meta.Plan != nil && response.Meta.Plan.Committable {
		t.Fatal("synthetic missing release unexpectedly became committable")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.writes != 0 {
		t.Fatal("release parameter validation performed a business write")
	}
}
