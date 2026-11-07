package tui

import (
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/charmbracelet/x/ansi"
)

// markdownSpan 将格式化器声明的正文源码与逻辑行范围绑定。
// 范围来自本地Writer，不能从远端返回的标记字符串反推。
type markdownSpan struct {
	start  int
	end    int
	indent int
	source string
}

// WriteMarkdown 有界缓存已脱敏的Markdown源码和可信片段范围。
// 不渲染ANSI到正文缓存，窗口变化可重新排版；超限记录截断并沿用禁止确认的不完整预览边界。
func (s *Session) WriteMarkdown(source string) (int, error) {
	source = s.clean(source)
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := max(0, (2<<20)-s.buffer.Len())
	if len(source) > remaining {
		written := source[:remaining]
		_, _ = s.buffer.WriteString(written)
		s.trackLines([]byte(written))
		s.truncated = true
		return len(source), nil
	}
	span := markdownSpan{start: s.line, indent: s.lineIndent, source: source}
	_, _ = s.buffer.WriteString(source)
	s.trackLines([]byte(source))
	span.end = s.line + 1
	s.markdown = append(s.markdown, span)
	return len(source), nil
}

// renderedRange 仅渲染当前组的逻辑行，正文的Markdown按当前宽度重新换行。
// 只有本地Markdown及可信字段标签产生的SGR可显示；远端文本不参与角色推断。
func (m sessionModel) renderedRange(raw []string, start, end int) ([]string, map[int]output.Style) {
	lines := []string{}
	roles := map[int]output.Style{}
	headings := map[int]output.Style{}
	for _, section := range m.request.sections {
		headings[section.line] = section.role
	}
	markdown := map[int]markdownSpan{}
	fields := map[int]fieldSpan{}
	for _, field := range m.request.fields {
		fields[field.line] = field
	}
	for _, span := range m.request.markdown {
		markdown[span.start] = span
	}
	width := m.contentWidth()
	for line := max(0, start); line < min(len(raw), end); {
		if span, exists := markdown[line]; exists && span.end <= end {
			indent := min(span.indent, max(0, width-8))
			rendered, err := output.RenderMarkdown(m.sessionDiagnostic(), m.sessionColor(), span.source, max(1, width-indent))
			if err == nil {
				for _, value := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
					lines = append(lines, strings.Repeat(" ", indent)+value)
				}
				line = span.end
				continue
			}
		}
		if role, exists := headings[line]; exists {
			roles[len(lines)] = role
		}
		if field, exists := fields[line]; exists {
			prefix := strings.Repeat(" ", field.indent) + field.label
			if strings.HasPrefix(raw[line], prefix) {
				value := strings.Repeat(" ", field.indent) + m.paint(output.StyleFieldKey, field.label) + strings.TrimPrefix(raw[line], prefix)
				lines = append(lines, strings.Split(ansi.Wrap(value, width, ""), "\n")...)
				line++
				continue
			}
		}
		lines = append(lines, wrapScreen(raw[line], width)...)
		line++
	}
	return lines, roles
}
