// Package tui 提供命令缺参时的可取消终端选择原型。
// 应用服务不依赖该包，输入流和诊断流由已验证交互终端的 CLI 显式传入。
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	tea "github.com/charmbracelet/bubbletea"
)

// Choice 使用稳定 ID 关联选择，不以标题或当前页位置作为业务键。
// Label 只用于展示，仍需清理远端终端控制字符。
type Choice struct {
	ID          string
	Label       string
	Description string
	Badge       string
	Role        output.Style
}

// Page 是当前搜索结果中的一页，不声称整个仓库总数。
// HasMore 允许用户主动继续加载，选择器不会后台拉取所有页面。
type Page struct {
	Choices []Choice
	HasMore bool
}

// Provider 按页和明确搜索条件读取候选，必须响应 context 取消。
// 搜索应覆盖服务器查询集，若为本地候选集则由标题明确说明范围。
type Provider func(context.Context, int, string) (Page, error)

type loaded struct {
	id   int
	page Page
	err  error
}

type model struct {
	ctx         context.Context
	requestCtx  context.Context
	cancel      context.CancelFunc
	provider    Provider
	title       string
	multi       bool
	page        int
	requestID   int
	choices     []Choice
	hasMore     bool
	selected    map[string]Choice
	order       []string
	cursor      int
	query       string
	draft       string
	editing     bool
	review      bool
	loading     bool
	finished    bool
	cancelled   bool
	err         error
	message     string
	width       int
	height      int
	emphasis    func(string) string
	style       func(output.Style, string) string
	formatHints func(int, string, string, string) string
}

// Select 运行诊断流上的独立资源界面，单选 Enter 接受焦点，多选 Space 后确认。
// 默认不产生业务目标；搜索翻页保留多选项，取消清理请求并恢复终端，最多 100 个目标。
func Select(ctx context.Context, input io.Reader, diagnostic io.Writer, title string, multi bool, provider Provider) ([]Choice, error) {
	return SelectStyled(ctx, input, diagnostic, title, multi, provider, "auto")
}

// SelectStyled 按诊断流的颜色策略运行选择器，业务目标仍由稳定 ID 返回。
// 退出先取消所有查询并等待响应取消的 provider 结束，再交还终端；调用方负责能力判定。
func SelectStyled(ctx context.Context, input io.Reader, diagnostic io.Writer, title string, multi bool, provider Provider, color string) ([]Choice, error) {
	ctx, cancel := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	var guard sync.Mutex
	closed := false
	wrapped := func(request context.Context, page int, search string) (Page, error) {
		guard.Lock()
		if closed {
			guard.Unlock()
			return Page{}, context.Canceled
		}
		tasks.Add(1)
		guard.Unlock()
		defer tasks.Done()
		return provider(request, page, search)
	}
	defer func() {
		guard.Lock()
		closed = true
		cancel()
		guard.Unlock()
		tasks.Wait()
	}()
	requestCtx, requestCancel := context.WithCancel(ctx)
	initial := model{
		ctx:        ctx,
		requestCtx: requestCtx,
		cancel:     requestCancel,
		provider:   wrapped,
		emphasis: func(value string) string {
			return output.Emphasis(diagnostic, color, value)
		},
		style: func(role output.Style, value string) string {
			return output.StyleText(diagnostic, color, role, value)
		},
		formatHints: func(width int, primary, secondary, back string) string {
			return output.FormatKeyHints(diagnostic, color, width, primary, secondary, back)
		},
		title:     title,
		multi:     multi,
		page:      1,
		requestID: 1,
		selected:  map[string]Choice{},
		loading:   true,
		width:     80,
		height:    24,
	}
	program := tea.NewProgram(initial, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(diagnostic), tea.WithAltScreen(), tea.WithoutSignalHandler())
	result, err := program.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, app.Failure(ctx.Err())
		}
		return nil, app.Wrap(app.IOError, "The interactive selector could not run.", err)
	}
	final := result.(model)
	if final.cancelled || !final.finished {
		return nil, app.Wrap(app.Cancelled, "Selection cancelled.", context.Canceled)
	}
	choices := []Choice{}
	for _, id := range final.order {
		if choice, exists := final.selected[id]; exists {
			choices = append(choices, choice)
		}
	}
	return choices, nil
}

// Init 启动第一页的可取消查询，不勾选任何结果。
// 请求 ID 使后续搜索不接受过期响应。
func (m model) Init() tea.Cmd {
	return m.load()
}

// load 捕获当前查询参数并返回由 Bubble Tea 管理的读取任务。
// 网络任务只返回消息，绝不直接修改界面模型。
func (m model) load() tea.Cmd {
	return func() tea.Msg {
		page, err := m.provider(m.requestCtx, m.page, m.query)
		return loaded{
			id:   m.requestID,
			page: page,
			err:  err,
		}
	}
}

// refresh 取消旧查询并创建新轮次，保持选择集合。
// 搜索、上一页与下一页都显式触发，不后台无限加载。
func (m model) refresh() (tea.Model, tea.Cmd) {
	m.cancel()
	m.requestCtx, m.cancel = context.WithCancel(m.ctx)
	m.requestID++
	m.loading = true
	m.err = nil
	m.cursor = 0
	m.review = false
	return m, m.load()
}

// visible 返回当前页或全部已选项，顺序只由稳定选择次序决定。
// 已选项不因换页或更换搜索条件而悄悄消失。
func (m model) visible() []Choice {
	if !m.review {
		return m.choices
	}
	values := []Choice{}
	for _, id := range m.order {
		if value, ok := m.selected[id]; ok {
			values = append(values, value)
		}
	}
	return values
}

// Update 处理键盘与异步分页消息，显式选择前不会产生默认目标。
// 取消与提交前只影响本地选择，没有业务写入副作用。
func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.WindowSizeMsg:
		if value.Width > 0 {
			m.width = value.Width
		}
		if value.Height > 0 {
			m.height = value.Height
		}
	case loaded:
		if value.id != m.requestID {
			return m, nil
		}
		m.loading = false
		m.err = value.err
		m.choices, m.hasMore = value.page.Choices, value.page.HasMore
		m.cursor = 0
	case tea.KeyMsg:
		key := value.String()
		if key == "ctrl+c" {
			m.cancelled = true
			m.cancel()
			return m, tea.Quit
		}
		if m.editing {
			switch key {
			case "esc":
				m.editing = false
			case "enter":
				m.query, m.page, m.editing = m.draft, 1, false
				return m.refresh()
			case "backspace":
				runes := []rune(m.draft)
				if len(runes) > 0 {
					m.draft = string(runes[:len(runes)-1])
				}
			case " ":
				if len(m.draft) < 256 {
					m.draft += " "
				}
			default:
				if value.Type == tea.KeyRunes && len(m.draft) < 256 {
					m.draft += string(value.Runes)
				}
			}
			return m, nil
		}
		if key == "esc" || key == "q" {
			m.cancelled = true
			m.cancel()
			return m, tea.Quit
		}
		if key == "/" {
			m.editing, m.draft = true, m.query
			return m, nil
		}
		if key == "r" && m.err != nil {
			return m.refresh()
		}
		if m.loading {
			return m, nil
		}
		choices := m.visible()
		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(choices) {
				m.cursor++
			}
		case "s":
			m.review = !m.review
			m.cursor = 0
		case "n", "pgdown":
			if !m.review && m.hasMore {
				m.page++
				return m.refresh()
			}
		case "p", "pgup":
			if !m.review && m.page > 1 {
				m.page--
				return m.refresh()
			}
		case " ":
			if m.cursor >= len(choices) {
				break
			}
			choice := choices[m.cursor]
			if _, ok := m.selected[choice.ID]; ok {
				delete(m.selected, choice.ID)
			} else if len(m.selected) < 100 {
				if !m.multi {
					m.selected = map[string]Choice{}
				}
				known := false
				for _, id := range m.order {
					known = known || id == choice.ID
				}
				if !known {
					m.order = append(m.order, choice.ID)
				}
				m.selected[choice.ID] = choice
			} else {
				m.message = "At most 100 targets can be selected."
			}
		case "enter":
			if !m.multi && m.cursor < len(choices) {
				choice := choices[m.cursor]
				m.selected = map[string]Choice{choice.ID: choice}
				m.order = []string{choice.ID}
			}
			if len(m.selected) == 0 {
				m.message = "Select at least one item with Space."
			} else {
				m.finished = true
				m.cancel()
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// View 渲染有限可见行，远端标题只作为普通数据展示。
// 窄终端按显示宽度截断，最终提交前另有完整目标预览。
func (m model) View() string {
	width := max(1, min(104, m.width-4))
	paint := func(role output.Style, value string) string {
		if m.style != nil {
			return m.style(role, value)
		}
		return value
	}
	status := fmt.Sprintf("Page %d · Selected %d · Search: %s", m.page, len(m.selected), safe(m.query))
	if !m.multi {
		status = fmt.Sprintf("Page %d · Search: %s", m.page, safe(m.query))
	}
	lines := []string{paint(output.StyleTitle, output.Truncate(m.title, width)), paint(output.StyleDescription, output.Truncate(status, width)), ""}
	if m.editing {
		lines = append(lines, paint(output.StyleLabel, output.Truncate("Search candidates: "+safe(m.draft)+"▏", width)))
	}
	if m.loading {
		lines = append(lines, paint(output.StyleSection, "Loading..."))
	} else if m.err != nil {
		lines = append(lines, paint(output.StyleError, "Unable to load candidates."), paint(output.StyleDescription, output.Truncate("Press r to retry or Esc to return.", width)))
	} else {
		values := m.visible()
		available := max(1, m.height-len(lines)-4)
		rowSize := navigationRows(available)
		visible := max(1, available/rowSize)
		start := max(0, m.cursor-visible+1)
		for index := start; index < min(len(values), start+visible); index++ {
			pointer := "  "
			if index == m.cursor {
				pointer = "> "
			}
			if m.multi {
				mark := " "
				if _, exists := m.selected[values[index].ID]; exists {
					mark = "x"
				}
				pointer += "[" + mark + "] "
			}
			lines = append(lines, choiceRows(values[index], pointer, width, rowSize, m.style)...)
		}
		if len(values) == 0 {
			lines = append(lines, "No items match this query.")
		}
	}
	primary, secondary, back := "Enter Choose", "↑↓ Move · / Search · n/p Page", "Esc Back"
	if m.multi {
		primary, secondary, back = "Space Select · Enter Continue", "↑↓ Move · / Search · n/p Page · s Selected", "Esc Cancel"
	}
	if m.message != "" {
		lines = append(lines, paint(output.StyleWarning, output.Truncate(m.message, width)))
	}
	hints := keyHintText(width, primary, secondary, back)
	if m.formatHints != nil {
		hints = m.formatHints(width, primary, secondary, back)
	}
	for len(lines) < max(1, m.height-3) {
		lines = append(lines, "")
	}
	lines = append(lines, "", hints)
	for index := range lines {
		lines[index] = "  " + lines[index]
	}
	return strings.Join(lines, "\n") + "\n"
}

// styledChoiceLine 为本地动作 ID 应用稳定语义色，远端 label 始终只作为普通文本。
// 高级选项统一使用紫色；焦点箭头另用主操作色，不将选择状态覆盖成另一颜色。
func styledChoiceLine(style func(output.Style, string) string, choice Choice, line string) string {
	pointer := "  "
	rest := line
	if strings.HasPrefix(line, ">") {
		pointer = "> "
		rest = strings.TrimPrefix(line, ">")
		rest = strings.TrimPrefix(rest, " ")
	}
	role := choice.Role
	if choice.ID == "@options" {
		role = output.StyleOption
	}
	if role != "" {
		rest = style(role, rest)
	}
	if strings.TrimSpace(pointer) != "" {
		pointer = style(output.StylePrimary, pointer)
	}
	return pointer + rest
}

// safe 去除远端数据中的控制符，防止 ANSI／OSC 或换行改变界面结构。
// Unicode 普通文字保持原文，显示宽度由渲染层另行计算。
func safe(value string) string {
	return output.SingleLine(value)
}
