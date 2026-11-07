package output

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gfm "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MarkdownWriter 接收已脱敏的正文源文本，供交互界面在窗口变化后重新排版。
// 这不是任意ANSI入口；实现必须保留源码边界，仅信任本地Markdown渲染器生成的SGR样式。
type MarkdownWriter interface {
	WriteMarkdown(string) (int, error)
}

// RenderMarkdown 用CommonMark／GFM解析器把正文转换为安全终端文本。
// width 是可用字符单元宽度，至少为1；policy沿用颜色策略。链接只显示文本地址，图片只显示说明，
// 不请求网络、不输出HTML或OSC；远端控制符在解析前及实体解码后清理，机器JSON不调用本函数。
func RenderMarkdown(writer io.Writer, policy, source string, width int) (string, error) {
	source = clean(strings.ReplaceAll(source, "\r\n", "\n"))
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM))
	document := parser.Parser().Parse(text.NewReader([]byte(source)))
	renderer := markdownRenderer{writer: writer, policy: policy, source: []byte(source), width: max(1, width)}
	lines := renderer.blocks(document, renderer.width)
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n", nil
}

// markdownRenderer 仅保存一次正文解析的本地显示参数。
// 所有样式来自固定语义色板，源文件、链接和代码内容都不具有执行权限。
type markdownRenderer struct {
	writer io.Writer
	policy string
	source []byte
	width  int
}

// style 给解析后的文本片段添加受控终端强调。
// 颜色关闭时返回相同内容，未知远端属性不参与样式构造。
func (r markdownRenderer) style(role Style, value string) string {
	return StyleText(r.writer, r.policy, role, value)
}

// literal 清理实体解码和反斜线转义后可能重新出现的控制字符。
// 保留正文换行，避免HTML数字实体绕过解析前的终端防注入边界。
func (r markdownRenderer) literal(value []byte) string {
	return clean(html.UnescapeString(string(util.UnescapePunctuations(value))))
}

// inline 渲染行内文本、强调与链接，只产生本地可控SGR。
// HTML作为普通文本保留，图片不加载；地址与标签均显示，避免掩盖链接实际目标。
func (r markdownRenderer) inline(parent ast.Node) string {
	var result strings.Builder
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		switch value := node.(type) {
		case *ast.Text:
			result.WriteString(r.literal(value.Value(r.source)))
			if value.HardLineBreak() || value.SoftLineBreak() {
				result.WriteByte('\n')
			}
		case *ast.String:
			result.WriteString(r.literal(value.Value))
		case *ast.Emphasis:
			result.WriteString(r.style(StyleTitle, r.inline(value)))
		case *ast.CodeSpan:
			var code strings.Builder
			for child := value.FirstChild(); child != nil; child = child.NextSibling() {
				if segment, ok := child.(*ast.Text); ok {
					code.Write(segment.Value(r.source))
				}
			}
			result.WriteString(r.style(StyleOption, clean(code.String())))
		case *ast.Link:
			label, destination := r.inline(value), SingleLine(r.literal(value.Destination))
			result.WriteString(r.style(StylePrimary, label))
			if ansi.Strip(label) != destination && destination != "" {
				result.WriteString(" (" + destination + ")")
			}
		case *ast.Image:
			result.WriteString("[Image: " + r.inline(value) + "]")
		case *ast.AutoLink:
			result.WriteString(r.style(StylePrimary, SingleLine(r.literal(value.URL(r.source)))))
		case *ast.RawHTML:
			for index := 0; index < value.Segments.Len(); index++ {
				segment := value.Segments.At(index)
				result.WriteString(clean(string(segment.Value(r.source))))
			}
		case *gfm.TaskCheckBox:
			if value.IsChecked {
				result.WriteString("[x] ")
			} else {
				result.WriteString("[ ] ")
			}
		case *gfm.Strikethrough:
			result.WriteString(r.style(StyleDescription, "~~"+r.inline(value)+"~~"))
		default:
			result.WriteString(r.inline(node))
		}
	}
	return result.String()
}

// wrapped 按字素和显示单元折行，只有本地生成的SGR可以越过此边界。
// 长URL和中文都可硬换行，不以截断替代完整正文。
func wrapped(value string, width int) []string {
	return strings.Split(ansi.Wrap(value, max(1, width), ""), "\n")
}

// blocks 递归输出块级结构，使用成熟解析器决定嵌套和代码范围。
// 列表、引用及表格均按缩进后的实际宽度排版，不自行用正则猜测Markdown语法。
func (r markdownRenderer) blocks(parent ast.Node, width int) []string {
	lines := []string{}
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		var block []string
		switch value := node.(type) {
		case *ast.Heading:
			block = wrapped(r.style(StyleSection, r.inline(value)), width)
		case *ast.Paragraph, *ast.TextBlock:
			block = wrapped(r.inline(node), width)
		case *ast.List:
			number := value.Start
			for item := value.FirstChild(); item != nil; item = item.NextSibling() {
				marker := "• "
				if value.IsOrdered() {
					marker = fmt.Sprintf("%d. ", number)
					number++
				}
				itemLines := r.blocks(item, max(1, width-ansi.StringWidth(marker)))
				for index, line := range itemLines {
					prefix := strings.Repeat(" ", ansi.StringWidth(marker))
					if index == 0 {
						prefix = marker
					}
					block = append(block, r.style(StylePrimary, prefix)+line)
				}
			}
		case *ast.Blockquote:
			for _, line := range r.blocks(value, max(1, width-2)) {
				block = append(block, r.style(StyleDescription, "│ ")+line)
			}
		case *ast.FencedCodeBlock:
			language := SingleLine(clean(string(value.Language(r.source))))
			block = append(block, r.style(StyleLabel, "Code "+language))
			block = append(block, r.codeLines(value, width)...)
		case *ast.CodeBlock:
			block = append(block, r.style(StyleLabel, "Code"))
			block = append(block, r.codeLines(value, width)...)
		case *ast.ThematicBreak:
			block = []string{r.style(StyleDescription, strings.Repeat("─", min(48, width)))}
		case *ast.HTMLBlock:
			for index := 0; index < value.Lines().Len(); index++ {
				segment := value.Lines().At(index)
				block = append(block, wrapped(clean(string(segment.Value(r.source))), width)...)
			}
			if value.HasClosure() {
				block = append(block, wrapped(clean(string(value.ClosureLine.Value(r.source))), width)...)
			}
		case *gfm.Table:
			block = r.table(value, width)
		default:
			block = r.blocks(node, width)
		}
		if len(lines) > 0 && len(block) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, block...)
	}
	return lines
}

// codeLines 保留代码中的符号与空白，不再按行内Markdown解释。
// 每行按可用宽度换行并使用固定代码区域标记，不运行代码或按语言加载资源。
func (r markdownRenderer) codeLines(node ast.Node, width int) []string {
	lines := []string{}
	for index := 0; index < node.Lines().Len(); index++ {
		segment := node.Lines().At(index)
		content := strings.TrimSuffix(clean(string(segment.Value(r.source))), "\n")
		for _, line := range wrapped(content, max(1, width-2)) {
			lines = append(lines, r.style(StyleDescription, "│ ")+r.style(StyleOption, line))
		}
	}
	return lines
}

// table 将GFM表格转换为保留全部单元格的列结构。
// 窄窗口按行列换行，避免只保留首列或截去长单元格；对齐只使用可见字符宽度。
func (r markdownRenderer) table(node *gfm.Table, width int) []string {
	rows := [][]string{}
	for row := node.FirstChild(); row != nil; row = row.NextSibling() {
		cells := []string{}
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, r.inline(cell))
		}
		rows = append(rows, cells)
	}
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if columns == 0 {
		return nil
	}
	widths := make([]int, columns)
	for _, row := range rows {
		for column, cell := range row {
			for _, line := range strings.Split(cell, "\n") {
				widths[column] = max(widths[column], ansi.StringWidth(line))
			}
		}
	}
	total := (columns - 1) * 3
	for _, size := range widths {
		total += size
	}
	if total > width {
		for column := range widths {
			widths[column] = max(1, (width-(columns-1)*3)/columns)
		}
	}
	lines := []string{}
	for index, row := range rows {
		cells := make([][]string, columns)
		height := 1
		for column, cell := range row {
			cells[column] = wrapped(cell, widths[column])
			height = max(height, len(cells[column]))
		}
		for line := 0; line < height; line++ {
			parts := []string{}
			for column := range cells {
				value := ""
				if line < len(cells[column]) {
					value = cells[column][line]
				}
				parts = append(parts, value+strings.Repeat(" ", max(0, widths[column]-ansi.StringWidth(value))))
			}
			value := strings.TrimRight(strings.Join(parts, " │ "), " ")
			if index == 0 {
				value = r.style(StyleLabel, value)
			}
			lines = append(lines, wrapped(value, width)...)
		}
		if index == 0 {
			lines = append(lines, r.style(StyleDescription, strings.Repeat("─", min(width, max(1, total)))))
		}
	}
	return lines
}
