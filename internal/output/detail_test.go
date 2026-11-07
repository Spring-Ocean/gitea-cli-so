package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
)

// TestReadableDetailsPreserveDataAndRedaction 保护各类详情的递归展示与安全边界。
// 大整数、多行正文、未知字段、空值和 false 不丢失，JSON 仍保持原字段及类型。
func TestReadableDetailsPreserveDataAndRedaction(t *testing.T) {
	payload := map[string]any{
		"id":   json.Number("9007199254740993"),
		"body": "中文👩🏽‍💻 first line\nsecond line",
		"future_field": map[string]any{
			"enabled": false,
			"value":   nil,
			"token":   "hidden-field-secret",
			"items":   []any{"alpha", true, nil},
		},
		"unsafe":           "\x1b]52;c;payload\x07 synthetic-secret",
		"synthetic-secret": "A sensitive dynamic key",
	}
	result := app.NewResult(app.Context{}, []app.Item{{
		Target: "detail",
		Status: app.Success,
		Data:   payload,
	}}, nil)
	var stream bytes.Buffer
	render := Renderer{
		Out:        &stream,
		Diagnostic: &stream,
		Redactor:   NewRedactor("synthetic-secret"),
		Color:      "never",
	}
	if err := render.Text(result); err != nil {
		t.Fatal(err)
	}
	text := stream.String()
	for _, expected := range []string{"9007199254740993", "中文👩🏽‍💻 first line", "second line", "Future field", "Enabled", "No", "Not set", "alpha", "Yes", "[REDACTED]"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("readable data was lost: %q", expected)
		}
	}
	for _, unwanted := range []string{"hidden-field-secret", "synthetic-secret", "\x1b", "\x07", "\"future_field\":", "\\nsecond"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("unsafe or raw JSON text reached the display: %q", unwanted)
		}
	}
	stream.Reset()
	if err := render.JSON(result); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(stream.Bytes()) || !strings.Contains(stream.String(), "\"enabled\":false") || !strings.Contains(stream.String(), "\"value\":null") {
		t.Fatal("readable formatting changed the machine result")
	}
}

// TestRepositorySectionsKeepAdditionalFields 保护仓库概览的分组与后续版本字段。
// 仓库常用数据有清晰章节，其余数据仍可查看，不用伪造默认值补齐缺失字段。
func TestRepositorySectionsKeepAdditionalFields(t *testing.T) {
	var stream bytes.Buffer
	render := Renderer{Out: &stream}
	err := render.Value(map[string]any{
		"full_name":      "team/repo",
		"description":    "A shared project",
		"default_branch": "main",
		"private":        true,
		"stars_count":    0,
		"clone_url":      "https://git.example/team/repo.git",
		"permissions":    map[string]any{"pull": true, "push": false},
		"future_setting": map[string]any{
			"mode": "new-mode",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Overview", "Activity", "Links", "Access", "Additional details", "team/repo", "main", "new-mode", "HTTPS clone URL"} {
		if !strings.Contains(stream.String(), expected) {
			t.Fatalf("missing repository detail: %q", expected)
		}
	}
	if strings.Contains(stream.String(), "Updated at") || strings.Contains(stream.String(), "\"default_branch\"") {
		t.Fatal("repository formatting invented a missing field or dumped JSON")
	}
}

// TestObjectListWithNestedFieldsDoesNotLeakSectionMarkers 保护无表格列列表走可信详情渲染。
// 嵌套对象仍显示章节标题与字段值，内部元数据不泄漏到终端文本。
func TestObjectListWithNestedFieldsDoesNotLeakSectionMarkers(t *testing.T) {
	var stream bytes.Buffer
	render := Renderer{Out: &stream}
	if err := render.Value([]any{
		map[string]any{
			"future_setting": map[string]any{
				"enabled": true,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	text := stream.String()
	for _, expected := range []string{"Item 1", "Future setting", "Enabled", "Yes"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("nested list detail was lost: %q", expected)
		}
	}
	if strings.Contains(text, sectionStart) || strings.Contains(text, sectionEnd) {
		t.Fatal("trusted section markers leaked to ordinary output")
	}
}
