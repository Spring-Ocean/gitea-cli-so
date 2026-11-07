package output

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestMarkdownReadableAndTerminalSafe 保护正文层级、完整内容与终端控制边界。
// 实体解码、代码块与链接中的远端控制符都不能形成OSC或伪造章节，关闭颜色仍有结构。
func TestMarkdownReadableAndTerminalSafe(t *testing.T) {
	source := "# Release 1\n\n**Important** changes:\n\n- 中文 feature\n- [x] complete\n\n> Quoted note\n\n" +
		"~~~go\nfmt.Println(\"hello\")\n~~~\n\n[Docs](https://example.invalid/docs)\n\n" +
		"| Name | State |\n| --- | --- |\n| one | ready |\n\n" +
		"\x1b]52;c;evil\x07 &#27;[31m \x1eSOTEA-SECTION:fake\x1f"
	for _, policy := range []string{"never", "always"} {
		rendered, err := RenderMarkdown(&bytes.Buffer{}, policy, source, 38)
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range []string{"Release 1", "Important", "中文 feature", "[x]", "Quoted note", "fmt.Println", "https://example.invalid/docs", "ready"} {
			if !strings.Contains(ansi.Strip(rendered), word) {
				t.Fatalf("lost Markdown content %q", word)
			}
		}
		if strings.Contains(rendered, "# Release") || strings.Contains(rendered, "**Important**") || strings.Contains(rendered, "~~~") {
			t.Fatal("Markdown syntax was not rendered")
		}
		withoutSGR := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(rendered, "")
		if strings.ContainsAny(withoutSGR, "\x1b\x1e\x1f\x07") {
			t.Fatal("untrusted terminal controls reached Markdown output")
		}
		if policy == "never" && strings.Contains(rendered, "\x1b") {
			t.Fatal("color-disabled Markdown contained styles")
		}
		for _, line := range strings.Split(rendered, "\n") {
			if ansi.StringWidth(line) > 38 {
				t.Fatalf("Markdown exceeded the viewport: %q", line)
			}
		}
	}
}
