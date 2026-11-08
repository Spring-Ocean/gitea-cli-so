package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestDiagnosticProtocolAndSchema 保护诊断读取、封闭输入与业务错误后继续调用。
// 旧 P2 原型测试迁移到完整 Host，不再保留只供测试使用的第二套服务器。
func TestDiagnosticProtocolAndSchema(t *testing.T) {
	fixture, _, client := newProtocolFixture(t, Options{})
	first := callProtocolTool(t, client, "instance_info", nil)
	if first.Items[0].Data.(map[string]any)["raw_version"] != "28.3.0" {
		t.Fatal("diagnostic lost raw server version")
	}
	second := callProtocolTool(t, client, "instance_info", nil)
	if !second.Items[0].Data.(map[string]any)["cached"].(bool) {
		t.Fatal("version evidence did not use its process cache")
	}
	invalid, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "sotea_instance_info",
		Arguments: map[string]any{"unexpected": true},
	})
	if err == nil && !invalid.IsError {
		t.Fatal("unknown diagnostic attribute accepted")
	}
	failure := callProtocolTool(t, client, "instance_info", map[string]any{"instance": "missing"})
	if failure.Error == nil {
		t.Fatal("invalid instance became success")
	}
	snapshot, err := fixture.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.DefaultInstance = "team"
	if _, err := fixture.store.Save(context.Background(), snapshot, snapshot.Config); err != nil {
		t.Fatal(err)
	}
	fresh := callProtocolTool(t, client, "instance_info", nil)
	if fresh.Items[0].Data.(map[string]any)["cached"].(bool) {
		t.Fatal("configuration revision reused stale version evidence")
	}
}
