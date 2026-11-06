// Package tui 提供命令缺参时的可取消终端选择原型。
// 应用服务不依赖该包，输入流和诊断流由已验证交互终端的 CLI 显式传入。
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Choice 使用稳定 ID 关联选择，不以标题或当前页位置作为业务键。
// Label 只用于展示，仍需清理远端终端控制字符。
type Choice struct {
	ID    string
	Label string
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
	ctx        context.Context
	requestCtx context.Context
	cancel     context.CancelFunc
	provider   Provider
	title      string
	multi      bool
	page       int
	requestID  int
	choices    []Choice
	hasMore    bool
	selected   map[string]Choice
	order      []string
	cursor     int
	query      string
	draft      string
	editing    bool
	review     bool
	loading    bool
	finished   bool
	cancelled  bool
	err        error
	message    string
	width      int
	height     int
}

// Select 运行诊断流上的独立选择界面，Space 明确勾选，Enter 才确认。
// 默认无选择；搜索与翻页保留已选项，取消关闭在途请求并恢复终端，最多 100 个目标。
func Select(ctx context.Context, input io.Reader, diagnostic io.Writer, title string, multi bool, provider Provider) ([]Choice, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	requestCtx, requestCancel := context.WithCancel(ctx)
	initial := model{
		ctx:        ctx,
		requestCtx: requestCtx,
		cancel:     requestCancel,
		provider:   provider,
		title:      title,
		multi:      multi,
		page:       1,
		requestID:  1,
		selected:   map[string]Choice{},
		loading:    true,
		width:      80,
		height:     24,
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
	lines := []string{safe(m.title), fmt.Sprintf("Page %d · Selected %d · Search: %s", m.page, len(m.selected), safe(m.query))}
	if m.editing {
		lines = append(lines, "Search candidates: "+safe(m.draft)+"_")
	}
	if m.loading {
		lines = append(lines, "Loading...")
	} else if m.err != nil {
		lines = append(lines, "Unable to load candidates. Press r to retry or Esc to cancel.")
	} else {
		values := m.visible()
		height := m.height - 8
		if height < 1 {
			height = 1
		}
		start := 0
		if m.cursor >= height {
			start = m.cursor - height + 1
		}
		end := start + height
		if end > len(values) {
			end = len(values)
		}
		for index := start; index < end; index++ {
			choice := values[index]
			pointer, checked := " ", " "
			if index == m.cursor {
				pointer = ">"
			}
			if _, ok := m.selected[choice.ID]; ok {
				checked = "x"
			}
			width := m.width - 8
			if width < 10 {
				width = 10
			}
			lines = append(lines, fmt.Sprintf("%s [%s] %s", pointer, checked, runewidth.Truncate(safe(choice.Label), width, "…")))
		}
		if len(values) == 0 {
			lines = append(lines, "No items match this query.")
		}
	}
	lines = append(lines, "↑↓ Move · Space Select · / Search · n/p Page · s Selected · Enter Continue · Esc Cancel", m.message)
	return strings.Join(lines, "\n") + "\n"
}

// safe 去除远端数据中的控制符，防止 ANSI／OSC 或换行改变界面结构。
// Unicode 普通文字保持原文，显示宽度由渲染层另行计算。
func safe(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
}
