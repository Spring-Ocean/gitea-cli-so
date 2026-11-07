package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/rivo/uniseg"
)

const (
	sectionStart  = "\x1eSOTEA-SECTION:"
	sectionEnd    = "\x1f"
	markdownStart = "\x1eSOTEA-MARKDOWN:"
	fieldStart    = "\x1eSOTEA-FIELD:"
)

// Value 输出可读的业务数据，列表使用表格，对象与嵌套值使用分组字段。
// 先复制并递归脱敏，再过滤控制符；保留数字精度、空值和未知字段，不改变 JSON 出口。
// 无法编码的值或输出流错误返回 IOError，调用方负责结果状态及资源生命周期。
func (r Renderer) Value(value any) error {
	return r.textData(r.redactValue(value))
}

// Details 输出完整的可读字段树，适用于通用 API 这类没有专用详情入口的响应。
// 与列表摘要不同，此方法保留数组每项的所有字段；递归脱敏并返回编码或输出错误。
func (r Renderer) Details(value any) error {
	text, err := r.detail(r.redactValue(value))
	if err != nil {
		return err
	}
	return r.writeDetail(text)
}

// detail 将可序列化的数据整理成可读结构，不输出 JSON 标点和转义正文。
// 非标准 Go 值报错而非隐式 fmt 打印，避免泄露未经过递归脱敏的内部对象。
func (r Renderer) detail(value any) (string, error) {
	if _, err := json.Marshal(value); err != nil {
		return "", app.Wrap(app.IOError, "Unable to format result data.", err)
	}
	var text strings.Builder
	if object, ok := value.(map[string]any); ok && repositoryObject(object) {
		r.repositoryDetails(&text, object)
	} else {
		r.detailValue(&text, value, 0)
	}
	return text.String(), nil
}

// repositoryObject 只把带仓库标识及仓库专用字段的对象识别为仓库详情。
// 用户资料同样可能有 full_name，不能仅凭该字段套用仓库分组。
func repositoryObject(object map[string]any) bool {
	if _, ok := object["full_name"].(string); !ok {
		return false
	}
	for _, key := range []string{"clone_url", "default_branch", "has_issues"} {
		if _, exists := object[key]; exists {
			return true
		}
	}
	return false
}

// repositoryDetails 按开发者常用信息分组，保留所有额外字段供继续滚动查看。
// 缺失字段不生成假值，嵌套所有者和权限也经过同一递归展示边界。
func (r Renderer) repositoryDetails(text *strings.Builder, object map[string]any) {
	remaining := make(map[string]any, len(object))
	for key, value := range object {
		remaining[key] = value
	}
	groups := []struct {
		title string
		keys  []string
	}{
		{
			title: "Overview",
			keys:  []string{"full_name", "description", "private", "internal", "archived", "empty", "fork", "mirror", "default_branch", "language"},
		},
		{
			title: "Activity",
			keys:  []string{"stars_count", "watchers_count", "forks_count", "open_issues_count", "open_pr_counter", "release_counter", "size", "created_at", "updated_at"},
		},
		{
			title: "Links",
			keys:  []string{"html_url", "website", "clone_url", "ssh_url", "original_url"},
		},
		{
			title: "Access",
			keys:  []string{"owner", "permissions"},
		},
	}
	for _, group := range groups {
		present := []string{}
		for _, key := range group.keys {
			if _, exists := remaining[key]; exists {
				present = append(present, key)
			}
		}
		if len(present) == 0 {
			continue
		}
		r.detailHeading(text, group.title, 0)
		for _, key := range present {
			r.detailField(text, key, remaining[key], 1)
			delete(remaining, key)
		}
		text.WriteByte('\n')
	}
	if len(remaining) > 0 {
		r.detailHeading(text, "Additional details", 0)
		r.detailValue(text, remaining, 1)
	}
}

// detailValue 递归展示对象、数组和标量，字段顺序稳定且以业务主键优先。
// 空对象、空数组、null 与 false 分别显示，文本多行保持可读，不丢弃未知属性。
func (r Renderer) detailValue(text *strings.Builder, value any, depth int) {
	prefix := strings.Repeat("  ", min(depth, 12))
	switch value := value.(type) {
	case map[string]any:
		if len(value) == 0 {
			text.WriteString(prefix + "No fields.\n")
			return
		}
		keys := detailKeys(value)
		for _, key := range keys {
			r.detailField(text, key, value[key], depth)
		}
	case []any:
		if len(value) == 0 {
			text.WriteString(prefix + "No items.\n")
			return
		}
		for index, item := range value {
			switch item.(type) {
			case map[string]any, []any:
				r.detailHeading(text, fmt.Sprintf("Item %d", index+1), depth)
				r.detailValue(text, item, depth+1)
			default:
				r.detailField(text, fmt.Sprintf("%d", index+1), item, depth)
			}
		}
	default:
		text.WriteString(prefix + detailScalar(value) + "\n")
	}
}

// detailField 以固定标签列展示标量，复杂值进入缩进章节。
// 标量键具有独立可信标记，多行内容逐行缩进；值不因字段列宽被裁掉。
func (r Renderer) detailField(text *strings.Builder, key string, value any, depth int) {
	label := fieldLabel(r.Redactor.Text(key))
	prefix := strings.Repeat("  ", min(depth, 12))
	if source, ok := value.(string); ok && source != "" && markdownField(key) {
		r.detailHeading(text, label, depth)
		text.WriteString(prefix + "  " + markdownStart + clean(strings.TrimRight(strings.ReplaceAll(source, "\r\n", "\n"), "\n")) + sectionEnd + "\n")
		return
	}
	switch value.(type) {
	case map[string]any, []any:
		r.detailHeading(text, label, depth)
		r.detailValue(text, value, depth+1)
	default:
		content := detailScalar(value)
		if strings.Contains(content, "\n") {
			r.detailHeading(text, label, depth)
			for _, line := range strings.Split(content, "\n") {
				text.WriteString(prefix + "  " + line + "\n")
			}
			return
		}
		text.WriteString(prefix + fieldStart + label + sectionEnd + strings.Repeat(" ", max(2, 22-uniseg.StringWidth(label))) + content + "\n")
	}
}

// detailHeading 为当前显示流添加章节强调，纯文本模式只保留标题。
// 顶层标题与正文留空行；样式由当前出口承担，不将ANSI写入机器JSON。
func (r Renderer) detailHeading(text *strings.Builder, title string, depth int) {
	text.WriteString(strings.Repeat("  ", min(depth, 12)) + sectionStart + SingleLine(title) + sectionEnd + "\n")
	if depth == 0 {
		text.WriteByte('\n')
	}
}

// writeDetail 将可信章节元数据交给终端界面，普通响应内容保持不带样式。
// 章节、正文与标量键的标记仅由本地格式化器生成；直接文本出口应用相同的语义样式。
func (r Renderer) writeDetail(value string) error {
	writer, ok := r.Out.(SectionWriter)
	rest := value
	for {
		start := strings.Index(rest, sectionStart)
		markdown := strings.Index(rest, markdownStart)
		isMarkdown := markdown >= 0 && (start < 0 || markdown < start)
		marker := sectionStart
		if isMarkdown {
			start, marker = markdown, markdownStart
		}
		field := strings.Index(rest, fieldStart)
		isField := field >= 0 && (start < 0 || field < start)
		if isField {
			start, marker, isMarkdown = field, fieldStart, false
		}
		if start < 0 {
			break
		}
		if _, err := io.WriteString(r.Out, rest[:start]); err != nil {
			return app.Wrap(app.IOError, "Unable to write result details.", err)
		}
		rest = rest[start+len(marker):]
		end := strings.Index(rest, sectionEnd)
		if end < 0 {
			return app.Wrap(app.IOError, "Unable to format result details.", nil)
		}
		title := rest[:end]
		if isField {
			if fields, supported := r.Out.(FieldWriter); supported {
				if _, err := fields.WriteFieldLabel(title); err != nil {
					return app.Wrap(app.IOError, "Unable to display field labels.", err)
				}
			} else if _, err := io.WriteString(r.Out, StyleText(r.Out, r.Color, StyleFieldKey, title)); err != nil {
				return app.Wrap(app.IOError, "Unable to display field labels.", err)
			}
		} else if isMarkdown {
			if markdownWriter, supported := r.Out.(MarkdownWriter); supported {
				if _, err := markdownWriter.WriteMarkdown(title); err != nil {
					return app.Wrap(app.IOError, "Unable to display Markdown content.", err)
				}
			} else {
				rendered, err := RenderMarkdown(r.Out, r.Color, title, 80)
				if err != nil {
					return err
				}
				if _, err := io.WriteString(r.Out, strings.TrimSuffix(rendered, "\n")); err != nil {
					return app.Wrap(app.IOError, "Unable to display Markdown content.", err)
				}
			}
		} else if ok {
			if _, err := writer.WriteSection(title); err != nil {
				return app.Wrap(app.IOError, "Unable to style result section.", err)
			}
		} else {
			if _, err := fmt.Fprint(r.Out, StyleText(r.Out, r.Color, StyleSection, title)); err != nil {
				return app.Wrap(app.IOError, "Unable to style result section.", err)
			}
		}
		rest = rest[end+len(sectionEnd):]
	}
	if _, err := io.WriteString(r.Out, rest); err != nil {
		return app.Wrap(app.IOError, "Unable to write result details.", err)
	}
	return nil
}

// markdownField 仅对协议中语义明确的正文、说明和评论字段启用Markdown。
// URL、命令参数、用户名称及未知标量保持原样，防止把普通数据误解释为格式语法。
func markdownField(key string) bool {
	switch strings.ToLower(key) {
	case "body", "description", "notes", "release_notes", "content", "readme":
		return true
	default:
		return false
	}
}

// detailScalar 将 JSON 标量转换为英文界面值，保留原始数字表示。
// 空文本、未设置与布尔 false 不混为同一值，远端正文保持换行。
func detailScalar(value any) string {
	switch value := value.(type) {
	case nil:
		return "Not set"
	case bool:
		if value {
			return "Yes"
		}
		return "No"
	case string:
		if value == "" {
			return "Empty"
		}
		return clean(strings.ReplaceAll(value, "\r\n", "\n"))
	case json.Number:
		return value.String()
	default:
		return clean(fmt.Sprint(value))
	}
}

// detailKeys 将常用身份和正文放在前面，其余字段按名称稳定排列。
// 顺序只影响人读视图，不修改对象本身或机器输出。
func detailKeys(object map[string]any) []string {
	priority := []string{"full_name", "login", "number", "id", "name", "title", "tag_name", "state", "status", "description", "body"}
	keys := make([]string, 0, len(object))
	seen := map[string]bool{}
	for _, key := range priority {
		if _, exists := object[key]; exists {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := []string{}
	for key := range object {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// fieldLabel 将协议字段转为人读标题，常用缩写保持大写。
// 未知字段仍有稳定标签，不要求为每个 Gitea 版本复制展示模型。
func fieldLabel(key string) string {
	names := map[string]string{
		"full_name":        "Full name",
		"html_url":         "Web URL",
		"clone_url":        "HTTPS clone URL",
		"ssh_url":          "SSH clone URL",
		"open_pr_counter":  "Open pull requests",
		"release_counter":  "Releases",
		"target_commitish": "Target commit",
	}
	if label := names[key]; label != "" {
		return label
	}
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(SingleLine(key)))
	for index, word := range words {
		switch strings.ToLower(word) {
		case "id", "url", "ssh", "http", "https", "api", "pr", "sha":
			words[index] = strings.ToUpper(word)
		default:
			if index == 0 {
				runes := []rune(word)
				words[index] = strings.ToUpper(string(runes[0])) + string(runes[1:])
			}
		}
	}
	return strings.Join(words, " ")
}
