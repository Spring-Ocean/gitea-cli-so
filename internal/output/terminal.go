package output

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
	"golang.org/x/term"
)

// Style 表示程序自有界面的语义强调，不由远端文本决定。
// StyleSecondary 使用较弱对比，StyleBack 只标记返回或取消动作。
type Style string

const (
	// StylePrimary 表示当前页主要操作或键位。
	StylePrimary Style = "primary"
	// StyleSecondary 表示移动、搜索等辅助操作。
	StyleSecondary Style = "secondary"
	// StyleBack 表示返回、取消或退出。
	StyleBack Style = "back"
	// StyleOption 表示字段配置与高级选项入口。
	StyleOption Style = "option"
	// StyleSection 表示数据分组标题。
	StyleSection Style = "section"
	// StyleTitle 表示页面及操作名称，使用终端默认前景保持主题可读。
	StyleTitle Style = "title"
	// StyleDescription 表示辅助说明，不降低终端字形亮度。
	StyleDescription Style = "description"
	// StyleError 表示失败原因与需要处理的问题。
	StyleError Style = "error"
	// StyleWarning 表示待检查或未知结果。
	StyleWarning Style = "warning"
	// StyleSuccess 表示经过核验的成功状态。
	StyleSuccess Style = "success"
	// StyleLabel 表示输入标签和非交互字段名称。
	StyleLabel Style = "label"
	// StyleFieldKey 表示详情标量的字段名，与默认前景的字段值区分。
	StyleFieldKey Style = "field-key"
	// StyleLocation 表示底部当前位置，使用绿色普通字重。
	StyleLocation Style = "location"
	// StyleActiveTab 表示当前分组，使用粗体与背景色，不依赖分隔线。
	StyleActiveTab Style = "active-tab"
	// StyleInactiveTab 表示可切换但尚未激活的分组。
	StyleInactiveTab Style = "inactive-tab"
)

// SectionWriter 允许结果格式化器将可信章节标题交给终端界面结构化渲染。
// 实现保留标题文字及语义角色，不从远端字段推断样式。
type SectionWriter interface {
	WriteSection(string) (int, error)
}

// StyledWriter 接收本地格式化器声明的单行语义强调，不把它当作新的内容分组。
// 交互会话用元数据承载样式；普通文本流仍遵守自身颜色策略。
type StyledWriter interface {
	WriteStyled(Style, string) (int, error)
}

// FieldWriter 接收可信格式化器生成的字段名，供终端仅强调键而保留值的原始内容。
// 实现不能把远端ANSI当作样式；返回已接受的标签字节数及写入错误。
type FieldWriter interface {
	WriteFieldLabel(string) (int, error)
}

// ResultTargetWriter 接收类型化结果目标与状态，供持续界面简化成功摘要。
// 实现必须保留非成功状态，不得依据文本猜测成功或改变业务结果。
type ResultTargetWriter interface {
	WriteResultTarget(string, app.Status) error
}

// StyleText 按固定语义色板突出程序自有标签。
// policy 沿用 --color、NO_COLOR 与终端能力判定，未知样式按无强调文本返回。
func StyleText(writer io.Writer, policy string, style Style, value string) string {
	if !ColorEnabled(writer, policy) {
		return value
	}
	renderer := lipgloss.NewRenderer(writer)
	renderer.SetColorProfile(termenv.ANSI256)
	palette := map[Style]string{
		StylePrimary:     "39",
		StyleSecondary:   "248",
		StyleBack:        "208",
		StyleOption:      "135",
		StyleSection:     "43",
		StyleTitle:       "",
		StyleDescription: "248",
		StyleError:       "203",
		StyleWarning:     "214",
		StyleSuccess:     "42",
		StyleLabel:       "",
		StyleFieldKey:    "248",
		StyleLocation:    "42",
		StyleActiveTab:   "231",
		StyleInactiveTab: "248",
	}
	color, exists := palette[style]
	if !exists {
		return value
	}
	styled := renderer.NewStyle()
	if color != "" {
		styled = styled.Foreground(lipgloss.Color(color))
	}
	switch style {
	case StylePrimary, StyleBack, StyleOption, StyleSection, StyleTitle, StyleError, StyleWarning, StyleSuccess, StyleLabel, StyleActiveTab:
		styled = styled.Bold(true)
	}
	if style == StyleActiveTab {
		styled = styled.Background(lipgloss.Color("24"))
	}
	return styled.Render(value)
}

// FormatKeyHints 按主操作、辅助操作、返回操作的顺序生成分级键位说明。
// width 按终端显示单元计算；先截断纯文本再加 ANSI 样式，避免控制序列破坏窄屏布局。
func FormatKeyHints(writer io.Writer, policy string, width int, primary, secondary, back string) string {
	if width < 1 {
		return ""
	}
	backWidth := 0
	if back != "" {
		backWidth = uniseg.StringWidth(back)
	}
	separatorWidth := uniseg.StringWidth(" · ")
	var result strings.Builder
	used := 0
	primaryBudget := width
	if backWidth > 0 {
		primaryBudget -= backWidth + separatorWidth
	}
	if primary != "" && primaryBudget > 0 {
		label := Truncate(primary, primaryBudget)
		result.WriteString(StyleText(writer, policy, StylePrimary, label))
		used = uniseg.StringWidth(label)
		if label == primary {
			secondaryBudget := width - used - separatorWidth - backWidth
			if backWidth > 0 {
				secondaryBudget -= separatorWidth
			}
			if secondary != "" && secondaryBudget >= 4 {
				label = Truncate(secondary, secondaryBudget)
				result.WriteString(" · " + StyleText(writer, policy, StyleSecondary, label))
				used += separatorWidth + uniseg.StringWidth(label)
			}
		}
	} else if secondary != "" {
		secondaryBudget := width - backWidth
		if backWidth > 0 {
			secondaryBudget -= separatorWidth
		}
		if secondaryBudget >= 4 {
			label := Truncate(secondary, secondaryBudget)
			result.WriteString(StyleText(writer, policy, StyleSecondary, label))
			used = uniseg.StringWidth(label)
		}
	}
	if back != "" {
		available := width - used
		if result.Len() > 0 {
			available -= separatorWidth
		}
		if available > 0 {
			label := Truncate(back, available)
			if result.Len() > 0 {
				result.WriteString(" · ")
			}
			result.WriteString(StyleText(writer, policy, StyleBack, label))
		}
	}
	return result.String()
}

// ColorEnabled 针对当前输出流判断是否加入颜色与加粗。
// 显式 always／never 优先于 NO_COLOR；auto 要求真实终端及已知 TERM，重定向默认无样式。
func ColorEnabled(writer io.Writer, policy string) bool {
	if policy == "always" {
		return true
	}
	if policy == "never" || os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd())) && CapableTerminal(os.Getenv("TERM"))
}

// CapableTerminal 识别首版可使用 VT 布局的终端声明。
// 未知或 dumb 声明降级纯文本；Windows Terminal 可通过其明确的会话标识识别。
func CapableTerminal(name string) bool {
	if name == "dumb" {
		return false
	}
	return strings.HasPrefix(name, "xterm") || strings.HasPrefix(name, "screen") ||
		strings.HasPrefix(name, "tmux") || strings.HasPrefix(name, "vt") ||
		name == "ansi" || name == "linux" || os.Getenv("WT_SESSION") != ""
}

// Emphasis 使用独立 Lip Gloss renderer 渲染已经清理的自有摘要。
// 不修改全局色彩状态，调用方必须先过滤远端控制符；原始 API／Git 不调用此方法。
func Emphasis(writer io.Writer, policy, value string) string {
	return StyleText(writer, policy, StylePrimary, value)
}

// SingleLine 清理会改变终端结构的控制符及双向覆盖字符。
// 保留中文、Emoji 和组合字形，仅用于显示，不改变传给业务服务的原始内容。
func SingleLine(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || character >= 0x202a && character <= 0x202e ||
			character >= 0x2066 && character <= 0x2069 {
			return ' '
		}
		return character
	}, value)
}

// Truncate 按完整字素和终端显示单元截断单行，截断时显示省略标记。
// 不拆开组合 Emoji 或中文；width 小于一时返回空字符串，输入先经过控制符过滤。
func Truncate(value string, width int) string {
	value = SingleLine(value)
	if width < 1 {
		return ""
	}
	if uniseg.StringWidth(value) <= width {
		return value
	}
	var result strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		part := graphemes.Str()
		size := uniseg.StringWidth(part)
		if used+size > width-1 {
			break
		}
		result.WriteString(part)
		used += size
	}
	return result.String() + "…"
}

// textData 对资源列表选择稳定列，详情和嵌套对象采用分组字段展示。
// 数据已在调用处递归脱敏；详情不因终端宽度丢失字段，输出错误立即返回。
func (r Renderer) textData(value any) error {
	rows, isList := value.([]any)
	var page map[string]any
	if object, ok := value.(map[string]any); ok {
		_, hasPage := object["page"]
		_, hasMore := object["has_more"]
		if list, exists := object["items"]; exists && hasPage && hasMore {
			rows, isList = list.([]any)
			if list == nil {
				isList = true
			}
			page = object
		}
	}
	var contents string
	if isList && tabular(rows) {
		var err error
		contents, err = r.table(rows)
		if err != nil {
			return err
		}
		if page != nil {
			contents += fmt.Sprintf("Page %v · More: %v\n", page["page"], page["has_more"])
		}
	} else {
		var err error
		contents, err = r.detail(value)
		if err != nil {
			return err
		}
	}
	if !isList || !tabular(rows) {
		return r.writeDetail(contents)
	}
	if strings.Contains(contents, sectionStart) {
		return r.writeDetail(contents)
	}
	if writer, ok := r.Out.(StyledWriter); ok {
		for index, line := range strings.Split(strings.TrimSuffix(contents, "\n"), "\n") {
			role := Style("")
			if index == 0 {
				role = StyleSection
			} else if index <= len(rows) {
				row := rows[index-1].(map[string]any)
				if draft, _ := row["draft"].(bool); draft && row["tag_name"] != nil {
					role = StyleWarning
				}
			}
			if _, err := writer.WriteStyled(role, line+"\n"); err != nil {
				return app.Wrap(app.IOError, "Unable to display result table.", err)
			}
		}
		return nil
	}
	if _, err := io.WriteString(r.Out, contents); err != nil {
		return app.Wrap(app.IOError, "Unable to write result data.", err)
	}
	return nil
}

// tabular 判断列表能否按对象列展示，避免将标量或混合列表吞掉。
// 空列表也是有效列表，由表格输出明确的空状态。
func tabular(rows []any) bool {
	for _, row := range rows {
		if _, ok := row.(map[string]any); !ok {
			return false
		}
	}
	return true
}

// table 为共同资源模型生成有标题的紧凑表格。
// 真实终端按当前宽度裁切整行，管道保留完整列值；更多字段由 view／JSON 获取。
func (r Renderer) table(rows []any) (string, error) {
	if len(rows) == 0 {
		return "No items.\n", nil
	}
	keys := []string{}
	for _, key := range []string{"number", "state", "tag_name", "login", "name", "title", "draft", "size", "filename", "sha", "id"} {
		if key == "id" && len(keys) > 0 {
			continue
		}
		for _, row := range rows {
			if key == "draft" && row.(map[string]any)["tag_name"] != nil {
				continue
			}
			if _, exists := row.(map[string]any)[key]; exists {
				keys = append(keys, key)
				break
			}
		}
	}
	if len(keys) == 0 {
		return r.detail(rows)
	}
	width := 0
	if file, ok := r.Out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		width, _, _ = term.GetSize(int(file.Fd()))
	}
	widths := make([]int, len(keys))
	lines := make([][]string, 0, len(rows)+1)
	lines = append(lines, make([]string, len(keys)))
	for column, key := range keys {
		lines[0][column] = strings.ToUpper(strings.ReplaceAll(key, "_", " "))
	}
	for _, row := range rows {
		cells := make([]string, len(keys))
		for column, key := range keys {
			if value := row.(map[string]any)[key]; value != nil {
				cells[column] = SingleLine(fmt.Sprint(value))
				if draft, _ := row.(map[string]any)["draft"].(bool); key == "tag_name" && draft {
					cells[column] = "◇ Draft " + cells[column]
				}
			}
		}
		lines = append(lines, cells)
	}
	for _, cells := range lines {
		for column, value := range cells {
			size := uniseg.StringWidth(value)
			if width > 0 && size > 48 {
				size = 48
			}
			if size > widths[column] {
				widths[column] = size
			}
		}
	}
	var result strings.Builder
	for index, cells := range lines {
		parts := make([]string, len(cells))
		for column, value := range cells {
			if width > 0 {
				value = Truncate(value, widths[column])
			}
			parts[column] = value + strings.Repeat(" ", widths[column]-uniseg.StringWidth(value))
		}
		line := strings.TrimRight(strings.Join(parts, "  "), " ")
		if width > 0 {
			line = Truncate(line, width)
		}
		if _, structured := r.Out.(StyledWriter); !structured {
			if index == 0 {
				line = StyleText(r.Out, r.Color, StyleSection, line)
			} else if draft, _ := rows[index-1].(map[string]any)["draft"].(bool); draft && rows[index-1].(map[string]any)["tag_name"] != nil {
				line = strings.Replace(line, "◇ Draft", StyleText(r.Out, r.Color, StyleWarning, "◇ Draft"), 1)
			}
		}
		result.WriteString(line + "\n")
	}
	return result.String(), nil
}
