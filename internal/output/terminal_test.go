package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/rivo/uniseg"
)

// TestTerminalColorAndGraphemes 保护重定向、显式颜色及完整字素边界。
// NO_COLOR 不覆盖显式 always；窄屏不能拆开家庭 Emoji，远端控制符不进入文本。
func TestTerminalColorAndGraphemes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var stream bytes.Buffer
	if ColorEnabled(&stream, "auto") || ColorEnabled(&stream, "never") {
		t.Fatal("redirected output acquired implicit styles")
	}
	if !strings.Contains(Emphasis(&stream, "always", "Heading"), "\x1b[") {
		t.Fatal("explicit color did not override NO_COLOR")
	}
	for _, width := range []int{1, 2, 3, 4, 8} {
		value := Truncate("中👨‍👩‍👧‍👦e\u0301文tail", width)
		if uniseg.StringWidth(value) > width || strings.HasSuffix(strings.TrimSuffix(value, "…"), "\u200d") {
			t.Fatalf("broken display boundary at width %d: %q", width, value)
		}
	}
	if strings.ContainsAny(Truncate("unsafe\x1b]52;c;x\x07\u202e", 100), "\x1b\x07\u202e") {
		t.Fatal("remote control characters survived")
	}
}

// TestKeyHintsKeepTheBackActionWithinNarrowWidths 保护分级提示在窄终端仍可访问返回动作。
// 布局按显示单元计算，空间不足时压缩前序说明，不让 ANSI 控制符参与宽度截断。
func TestKeyHintsKeepTheBackActionWithinNarrowWidths(t *testing.T) {
	const width = 52
	value := FormatKeyHints(nil, "never", width,
		"PgUp/PgDn, Tab/Shift+Tab Switch group",
		"↑↓/Home/End Scroll current group",
		"Esc Back",
	)
	if uniseg.StringWidth(value) > width {
		t.Fatalf("key hints exceed terminal width: %q", value)
	}
	if !strings.Contains(value, "Esc Back") || !strings.Contains(value, "PgUp/PgDn") {
		t.Fatalf("primary or back actions were lost: %q", value)
	}
	wide := FormatKeyHints(nil, "never", 68,
		"PgUp/PgDn, Tab/Shift+Tab Switch group",
		"↑↓/Home/End Scroll current group",
		"Esc Back",
	)
	if uniseg.StringWidth(wide) > 68 || !strings.Contains(wide, "Esc Back") || !strings.Contains(wide, "↑↓/Home/End") {
		t.Fatalf("available secondary help was lost: %q", wide)
	}
}

// TestTablesAndFullMachineOutput 保护可读列表及完整数据出口。
// 表格保留标题和状态，JSON 即使强制颜色也保持合法、无样式且不截断。
func TestTablesAndFullMachineOutput(t *testing.T) {
	var stream bytes.Buffer
	title := strings.Repeat("中文👩🏽‍💻", 30)
	result := app.NewResult(app.Context{}, []app.Item{{
		Target: "issues",
		Status: app.Success,
		Data: app.Page[map[string]any]{
			Items: []map[string]any{{
				"number": 42,
				"title":  title,
				"state":  "open",
			}},
			Page:    1,
			Limit:   30,
			HasMore: false,
		},
	}}, nil)
	render := Renderer{
		Out:        &stream,
		Diagnostic: &stream,
		Color:      "never",
	}
	if err := render.Text(result); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"NUMBER", "TITLE", "STATE", title, "Page 1", "More: false"} {
		if !strings.Contains(stream.String(), fragment) {
			t.Fatalf("missing readable data: %s", fragment)
		}
	}
	stream.Reset()
	render.Color = "always"
	if err := render.JSON(result); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(stream.Bytes()) || strings.Contains(stream.String(), "\x1b") || !strings.Contains(stream.String(), title) {
		t.Fatal("machine output was styled or truncated")
	}
}

// TestReleaseTableOnlyMarksDrafts 保护草稿徽标的条件呈现和机器数据完整性。
// 已发布版本不显示false或状态徽标；草稿保留文字及图标，关闭颜色也能识别。
func TestReleaseTableOnlyMarksDrafts(t *testing.T) {
	var stream bytes.Buffer
	renderer := Renderer{Out: &stream, Color: "never"}
	rows := []map[string]any{
		{"id": 1, "tag_name": "v1", "name": "Published", "draft": false},
		{"id": 2, "tag_name": "v2", "name": "Upcoming", "draft": true},
	}
	if err := renderer.Value(rows); err != nil {
		t.Fatal(err)
	}
	value := stream.String()
	if strings.Count(value, "◇ Draft") != 1 || strings.Contains(value, "false") || strings.Contains(value, "true") || strings.Contains(value, "DRAFT") {
		t.Fatalf("release draft rendering exposed raw state or marked a published release: %q", value)
	}
	if !strings.Contains(value, "v1") || !strings.Contains(value, "v2") {
		t.Fatal("a draft badge removed the release identity")
	}
}
