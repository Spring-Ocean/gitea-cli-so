package tui

import (
	"context"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/rivo/uniseg"
)

// fieldSpan 记录格式化器写入的字段名在逻辑行中的位置。
// 仅行首缩进后的可信标签可声明样式，远端值保持普通内容。
type fieldSpan struct {
	line   int
	indent int
	label  string
}

// WriteFieldLabel 缓存经过脱敏的字段名并记录内联样式，不把值染成字段颜色。
// 与其他会话写入共享2MiB上限；超限标记不完整预览，返回请求接受的原始字节数。
func (s *Session) WriteFieldLabel(label string) (int, error) {
	cleanLabel := output.SingleLine(s.clean(label))
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := max(0, (2<<20)-s.buffer.Len())
	if len(cleanLabel) > remaining {
		s.truncated = true
		return len(label), nil
	}
	if !s.lineStarted {
		s.fields = append(s.fields, fieldSpan{line: s.line, indent: s.lineIndent, label: cleanLabel})
	}
	_, _ = s.buffer.WriteString(cleanLabel)
	s.trackLines([]byte(cleanLabel))
	return len(label), nil
}

// WriteResultTarget 将资源目标与正文字段分区，成功时不附加冗余状态。
// 非成功结果保留真实状态并使用警告强调，返回导航不会改写类型化业务结果。
func (s *Session) WriteResultTarget(target string, status app.Status) error {
	role := output.StyleTitle
	if status != app.Success {
		target += " · " + string(status)
		role = output.StyleWarning
	}
	_, err := s.WriteStyled(role, target+"\n\n")
	return err
}

// InputOptions 描述单步输入的用途、初始值及进度，不持有业务依赖。
// Secret 值只用于本次输入，不进入标题、说明、历史或错误；Value 的首尾空白保持不变。
type InputOptions struct {
	Title       string
	Description string
	Label       string
	Placeholder string
	Value       string
	Step        string
	Secret      bool
}

// NoticeOptions 将状态原因、恢复说明和可执行动作分开呈现。
// Kind 仅选择本地固定色板；Choices 返回稳定 ID，不自动执行远端操作。
// DefaultChoice 非空时覆盖历史焦点，防止确认页默认恢复上一次的批准选项。
type NoticeOptions struct {
	Title         string
	Message       string
	Hint          string
	Code          string
	Kind          output.Style
	Choices       []Choice
	DefaultChoice string
}

// WriteStyled 为本地生成的表头或状态行记录语义角色，不创建分组分页。
// 文本仍受显示上限和脱敏约束；外部响应不能自行声明SGR或控制终端。
func (s *Session) WriteStyled(role output.Style, value string) (int, error) {
	value = s.clean(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := max(0, (2<<20)-s.buffer.Len())
	written := value[:min(len(value), remaining)]
	if role != "" {
		s.sections = append(s.sections, sectionSpan{line: s.line, depth: -1, role: role})
	}
	_, _ = s.buffer.WriteString(written)
	s.trackLines([]byte(written))
	s.truncated = s.truncated || len(value) > remaining
	return len(value), nil
}

// Input 在当前终端会话中显示带标签的编辑框，返回用户提交的原始输入。
// Enter 接受，Esc 返回 Cancelled；秘密输入不显示字符数量，调用者负责验证与丢弃敏感值。
func (s *Session) Input(ctx context.Context, options InputOptions) (string, error) {
	reply, err := s.request(ctx, screenRequest{
		kind: "prompt", title: options.Title, secret: options.Secret, inputOptions: &options,
	})
	return reply.text, err
}

// Notice 显示结构化状态与恢复动作，返回主动选择的动作 ID。
// 没有动作时 Enter 关闭；Esc 返回 Cancelled，调用者不得把取消解释为已经恢复或重新执行成功。
// 明确 DefaultChoice 时使用该项覆盖历史导航焦点，不将历史批准作为本次默认选择。
func (s *Session) Notice(ctx context.Context, options NoticeOptions) (string, error) {
	request := screenRequest{
		kind: "notice", title: options.Title, choices: options.Choices, notice: &options,
	}
	if options.DefaultChoice != "" {
		request.initialFocus = &options.DefaultChoice
	}
	reply, err := s.request(ctx, request)
	return reply.text, err
}

// cleanText 只处理展示资料，不清理表单提交值。
// 测试模型可以没有会话；正式会话统一使用已注册的脱敏器。
func (m sessionModel) cleanText(value string) string {
	if m.session != nil && m.session.clean != nil {
		return m.session.clean(value)
	}
	return value
}

// paint 将纯文本交给当前终端的语义样式函数。
// 无颜色和测试模型保持同一文本，不能从远端内容读取样式控制序列。
func (m sessionModel) paint(role output.Style, value string) string {
	if m.style != nil {
		return m.style(role, value)
	}
	return value
}

// contentWidth 约束正文可读行宽，并为全局左右留白预留空间。
// 极窄窗口仍至少保留一个显示单元，不让边框把内容挤出终端。
func (m sessionModel) contentWidth() int {
	padding := 4
	if m.width < 40 {
		padding = 2
	}
	return max(1, min(104, m.width-padding))
}

// choiceParts 为新旧菜单统一拆分名称与说明。
// 仅影响展示，搜索和提交仍使用原始 Label 与稳定 ID。
func choiceParts(choice Choice) (string, string) {
	if choice.Description != "" {
		return output.SingleLine(choice.Label), output.SingleLine(choice.Description)
	}
	name, description, _ := strings.Cut(choice.Label, " · ")
	return output.SingleLine(name), output.SingleLine(description)
}

// choiceRows 生成一个菜单项的名称、说明及间隔，共用焦点与多选标记。
// rows 控制小窗口降级，优先保留名称和焦点，不删除任何可选择项。
func choiceRows(choice Choice, pointer string, width, rows int, style func(output.Style, string) string) []string {
	paint := func(role output.Style, value string) string {
		if style != nil {
			return style(role, value)
		}
		return value
	}
	name, description := choiceParts(choice)
	role := output.StyleTitle
	if choice.Role != "" {
		role = choice.Role
	}
	if choice.ID == "@options" {
		role = output.StyleOption
	}
	prefix := paint(output.StylePrimary, pointer)
	badge := ""
	badgeWidth := 0
	if choice.Badge != "" {
		badgeText := output.SingleLine(choice.Badge) + " "
		badgeWidth = uniseg.StringWidth(badgeText)
		badge = paint(output.StyleWarning, badgeText)
	}
	result := []string{prefix + badge + paint(role, output.Truncate(name, max(1, width-uniseg.StringWidth(pointer)-badgeWidth)))}
	if rows > 1 {
		result = append(result, strings.Repeat(" ", min(2, width))+paint(output.StyleDescription, output.Truncate(description, max(1, width-2))))
	}
	if rows > 2 {
		result = append(result, "")
	}
	return result
}

// inputBox 显示可横向编辑的输入区域，使用固定密文提示避免泄漏长度。
// 光标所在片段始终可见；标签、提示和用户可见字符均经过控制符清理。
func (m sessionModel) inputBox() []string {
	width := m.contentWidth()
	inner := max(1, width-4)
	label, placeholder := "Input", ""
	if m.request.inputOptions != nil {
		label = m.request.inputOptions.Label
		placeholder = m.request.inputOptions.Placeholder
	}
	value := "[input hidden]"
	if !m.request.secret {
		position := min(max(0, m.inputCursor), len(m.input))
		before := []rune(output.SingleLine(string(m.input[:position])))
		after := output.SingleLine(string(m.input[position:]))
		for uniseg.StringWidth(string(before))+1 > inner && len(before) > 0 {
			before = before[1:]
		}
		value = output.Truncate(string(before)+"▏"+after, inner)
		if len(m.input) == 0 && placeholder != "" {
			value = output.Truncate("▏ "+output.SingleLine(m.cleanText(placeholder)), inner)
		}
	}
	if width < 10 {
		return []string{m.paint(output.StylePrimary, output.Truncate("> "+value, width))}
	}
	top := "╭" + strings.Repeat("─", width-2) + "╮"
	middle := "│ " + output.Truncate(value, inner)
	middle += strings.Repeat(" ", max(0, inner-uniseg.StringWidth(output.Truncate(value, inner)))) + " │"
	return []string{
		m.paint(output.StyleLabel, output.Truncate(m.cleanText(label), width)),
		m.paint(output.StylePrimary, top),
		middle,
		m.paint(output.StylePrimary, "╰"+strings.Repeat("─", width-2)+"╯"),
	}
}
