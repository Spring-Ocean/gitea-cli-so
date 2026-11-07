package tui

import (
	"fmt"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/charmbracelet/x/ansi"
)

// pageHeader 将品牌、上下文与页面标题分区，避免长地址和操作挤在同一行。
// 成功业务详情由资源标题承担正文层级，操作路径置于底部；失败标题和确认提示仍完整保留。
func (m sessionModel) pageHeader() []string {
	width := m.contentWidth()
	title := pageTitle(m.cleanText(m.request.title), width, m.request.kind == "prompt")
	_, commandPage := commandLocation(m.request.title)
	quietResult := m.request.kind == "display" && commandPage && !strings.Contains(m.request.title, " · exit ")
	if m.height < 16 {
		lines := []string{m.paint(output.StylePrimary, "sotea"), m.paint(output.StyleTitle, title), ""}
		if quietResult {
			lines = []string{m.paint(output.StylePrimary, "sotea"), ""}
		}
		if m.request.inputOptions != nil && m.request.inputOptions.Step != "" {
			lines = append(lines, m.paint(output.StyleSection, output.Truncate(m.request.inputOptions.Step, width)))
		}
		return lines
	}
	lines := []string{m.paint(output.StylePrimary, "sotea")}
	if m.header != "" {
		parts := strings.Split(m.cleanText(m.header), " · ")
		// 当前目标单独显示；连接状态在首页卡片或操作结果中有自己的位置。
		contextLine := strings.Join(parts[:min(2, len(parts))], "  /  ")
		lines = append(lines, m.paint(output.StyleDescription, output.Truncate(contextLine, width)))
	}
	if quietResult {
		return append(lines, "")
	}
	role := output.StyleTitle
	if m.request.notice != nil && m.request.notice.Kind != "" {
		role = m.request.notice.Kind
	}
	lines = append(lines, "", m.paint(role, title), "")
	if options := m.request.inputOptions; options != nil {
		if options.Step != "" {
			lines = append(lines, m.paint(output.StyleSection, output.Truncate(m.cleanText(options.Step), width)))
		}
		if options.Description != "" {
			for _, line := range proseLines(m.cleanText(options.Description), width) {
				lines = append(lines, m.paint(output.StyleDescription, line))
			}
			lines = append(lines, "")
		}
	}
	return lines
}

// frame 给所有页面使用相同边距和固定操作区，输入或结果不会覆盖按键提示。
// 固定底部先显示绿色普通字重路径，再显示操作提示；内容只接受本地渲染的可信SGR文本。
func (m sessionModel) frame(lines []string, primary, secondary, back string) string {
	width := m.contentWidth()
	padding := "  "
	if m.width < 40 {
		padding = " "
	}
	available := max(1, m.height-3)
	if len(lines) > available {
		lines = lines[:available]
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	location := output.Truncate(m.pageLocation(), width)
	lines = append(lines, m.paint(output.StyleLocation, location), output.FormatKeyHints(m.sessionDiagnostic(), m.sessionColor(), width, primary, secondary, back))
	for index, line := range lines {
		lines[index] = padding + ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines, "\n") + "\n"
}

// navigationRows 根据可用高度选择带说明与间隔的菜单项密度。
// 正常窗口使用三行，低矮窗口仍保留稳定焦点和访问全部动作的能力。
func navigationRows(height int) int {
	if height >= 10 {
		return 3
	}
	if height >= 5 {
		return 2
	}
	return 1
}

// navigationView 按统一名称与说明层级展示当前菜单，并保留搜索范围。
// 所有动作仍由稳定ID提交，屏幕外项目可通过移动或搜索访问。
func (m sessionModel) navigationView(header []string) string {
	width := m.contentWidth()
	choices := m.navigation()
	available := max(1, m.height-len(header)-5)
	rowSize := navigationRows(available)
	visible := max(1, available/rowSize)
	start := max(0, m.cursor-visible+1)
	end := min(len(choices), start+visible)
	lines := append([]string(nil), header...)
	for index := start; index < end; index++ {
		choice := choices[index]
		choice.Label, choice.Description = m.cleanText(choice.Label), m.cleanText(choice.Description)
		pointer := "  "
		if index == m.cursor {
			pointer = "> "
		}
		lines = append(lines, choiceRows(choice, pointer, width, rowSize, m.style)...)
	}
	if len(choices) == 0 {
		lines = append(lines, m.paint(output.StyleDescription, "No matching actions."))
	}
	if len(choices) > visible {
		lines = append(lines, m.paint(output.StyleDescription, fmt.Sprintf("Actions %d–%d of %d", min(start+1, len(choices)), end, len(choices))))
	}
	primary, secondary := "Enter Open", "↑↓ Move · / Search"
	if m.query != "" || m.editing {
		lines = append(lines, m.paint(output.StyleLabel, output.Truncate("Search: "+m.query, width)))
	}
	if m.editing {
		primary, secondary = "Enter Finish", "Type to search"
	}
	return m.frame(lines, primary, secondary, "Esc Back")
}

// noticeView 将失败原因、诊断代码与下一步说明分开呈现。
// 长错误信息可分页查看，恢复动作始终可见，不根据显示内容自动重试业务。
func (m sessionModel) noticeView() string {
	options := m.request.notice
	if options == nil {
		return m.frame(m.pageHeader(), "Enter Back", "", "Esc Back")
	}
	width := m.contentWidth()
	role := options.Kind
	if role == "" {
		role = output.StyleError
	}
	content := []string{}
	if options.Code != "" {
		content = append(content, m.paint(role, output.Truncate(m.cleanText(options.Code), width)))
	}
	for _, line := range proseLines(m.cleanText(options.Message), width) {
		content = append(content, line)
	}
	if options.Hint != "" {
		content = append(content, "", m.paint(output.StylePrimary, "Next step"))
		for _, line := range proseLines(m.cleanText(options.Hint), width) {
			content = append(content, m.paint(output.StyleDescription, line))
		}
	}
	header := m.pageHeader()
	choices := m.navigation()
	rowSize := 3
	if m.height < 18 {
		rowSize = 1
	}
	extra := 0
	if m.query != "" || m.editing {
		extra = 1
	}
	visible := min(len(choices), max(1, (m.height-len(header)-7-extra)/rowSize))
	startChoice := max(0, m.cursor-visible+1)
	bodyHeight := max(1, m.height-len(header)-visible*rowSize-4-extra)
	start := min(max(0, m.offset), max(0, len(content)-bodyHeight))
	lines := append(header, content[start:min(len(content), start+bodyHeight)]...)
	lines = append(lines, "")
	for index := startChoice; index < min(len(choices), startChoice+visible); index++ {
		pointer := "  "
		if index == m.cursor {
			pointer = "> "
		}
		choice := choices[index]
		choice.Label, choice.Description = m.cleanText(choice.Label), m.cleanText(choice.Description)
		lines = append(lines, choiceRows(choice, pointer, width, rowSize, m.style)...)
	}
	primary, secondary := "Enter Continue", "↑↓ Choose · / Search"
	if m.query != "" || m.editing {
		lines = append(lines, m.paint(output.StyleLabel, output.Truncate("Search: "+m.query, width)))
	}
	if m.editing {
		primary, secondary = "Enter Finish", "Type to search"
	}
	if len(content) > bodyHeight {
		secondary += " · PgUp/PgDn Details"
	}
	return m.frame(lines, primary, secondary, "Esc Back")
}

// resultView 以独立正文区域展示当前组，并为输入框和确认语义预留空间。
// Markdown渲染产生的SGR只在此信任边界保留；切组不改变业务计划或确认对象。
func (m sessionModel) resultView() string {
	lines := m.pageHeader()
	width := m.contentWidth()
	_, body, styles := m.contentLines()
	if strings.TrimSpace(m.request.body) == "" {
		body = nil
	}
	height := m.contentHeight()
	start := min(max(0, m.offset), max(0, len(body)-height))
	groups := sectionGroups(m.request.body, m.request.sections)
	if len(groups) > 0 {
		lines = append(lines, m.groupTabs(groups), "")
	}
	confirmation := m.request.kind == "prompt" && strings.Contains(m.request.title, "[y/N]")
	if confirmation && len(groups) > 0 {
		lines = append(lines, m.paint(output.StyleWarning, output.Truncate("Confirmation applies to the complete plan.", width)), "")
	}
	for index := start; index < min(len(body), start+height); index++ {
		line := body[index]
		if role, exists := styles[index]; exists {
			line = m.paint(role, line)
		}
		lines = append(lines, line)
	}
	primary, secondary, back := "", "", "Esc Back · Enter Back"
	if len(groups) > 1 {
		primary = "PgUp/PgDn · Tab/Shift+Tab Switch tab"
	}
	if len(body) > height {
		secondary = "↑↓/Home/End Scroll group"
		if len(groups) == 0 {
			primary, secondary = "↑↓/Home/End Scroll", "PgUp/PgDn Page"
		}
	}
	if m.request.kind == "prompt" {
		lines = append(lines, "")
		lines = append(lines, m.inputBox()...)
		secondary = strings.TrimSpace(primary + " " + secondary)
		primary, back = "Enter Submit", "Esc Back"
		if strings.TrimSpace(m.request.body) == "" {
			secondary = "←→ Edit · Ctrl+U Clear"
		}
	}
	return m.frame(lines, primary, secondary, back)
}

// groupTabs 使用粗体与背景强调当前组，其他标签通过留白分隔。
// 关闭颜色时以圆点标记活动组；窄屏优先保留当前名称，标签不改变切组或确认范围。
func (m sessionModel) groupTabs(groups []displayGroup) string {
	if len(groups) == 0 {
		return ""
	}
	width := m.contentWidth()
	current := min(max(0, m.groupIndex), len(groups)-1)
	labels := make([]string, len(groups))
	plain := !output.ColorEnabled(m.sessionDiagnostic(), m.sessionColor())
	for index, group := range groups {
		labels[index] = " " + output.SingleLine(m.cleanText(group.title)) + " "
		if index == current && plain {
			labels[index] = "•" + labels[index]
		}
	}
	measure := func(start, end int) int {
		cells := max(0, end-start-1) * 2
		for _, label := range labels[start:end] {
			cells += ansi.StringWidth(label)
		}
		if start > 0 {
			cells += 2
		}
		if end < len(groups) {
			cells += 2
		}
		return cells
	}
	start, end := current, current+1
	if measure(start, end) > width {
		title := output.SingleLine(m.cleanText(groups[current].title))
		if plain && width >= 3 {
			return m.paint(output.StyleActiveTab, "• "+output.Truncate(title, width-2))
		}
		return m.paint(output.StyleActiveTab, output.Truncate(title, width))
	}
	for {
		expanded := false
		if start > 0 && measure(start-1, end) <= width {
			start--
			expanded = true
		}
		if end < len(groups) && measure(start, end+1) <= width {
			end++
			expanded = true
		}
		if !expanded {
			break
		}
	}
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		role := output.StyleInactiveTab
		if index == current {
			role = output.StyleActiveTab
		}
		parts = append(parts, m.paint(role, labels[index]))
	}
	bar := strings.Join(parts, "  ")
	if start > 0 {
		bar = m.paint(output.StyleDescription, "… ") + bar
	}
	if end < len(groups) {
		bar += m.paint(output.StyleDescription, " …")
	}
	return bar
}

// proseLines 在状态说明的单词边界换行，同时保留明确的段落换行。
// 先清理远端控制符，只有本地样式包装完成后的文本才交给页面框架。
func proseLines(value string, width int) []string {
	cleaned := []string{}
	for _, line := range strings.Split(value, "\n") {
		cleaned = append(cleaned, output.SingleLine(line))
	}
	return strings.Split(ansi.Wrap(strings.Join(cleaned, "\n"), max(1, width), ""), "\n")
}
