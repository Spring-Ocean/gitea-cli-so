package tui

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// Session 在一个终端程序中承载串行导航、输入、资源选择和可滚动结果。
// 只能在 RunSession 的控制器内使用；业务自行携带 context，网络不阻塞渲染循环。
type Session struct {
	ctx              context.Context
	program          *tea.Program
	diagnostic       io.Writer
	color            string
	mu               sync.Mutex
	buffer           strings.Builder
	truncated        bool
	clean            func(string) string
	style            func(output.Style, string) string
	line             int
	lineIndent       int
	lineStarted      bool
	sections         []sectionSpan
	pending          []sectionSpan
	pendingBody      string
	pendingTruncated bool
	markdown         []markdownSpan
	pendingMarkdown  []markdownSpan
	fields           []fieldSpan
	pendingFields    []fieldSpan
}

type sectionSpan struct {
	line  int
	depth int
	title string
	role  output.Style
}

// screenRequest 描述一次由会话拥有的页面请求及其返回通道。
// initialFocus 非空时覆盖导航历史，用于显式默认项或安全确认焦点。
type screenRequest struct {
	kind         string
	title        string
	body         string
	choices      []Choice
	initialFocus *string
	provider     Provider
	ctx          context.Context
	multi        bool
	secret       bool
	home         *HomeInfo
	cancel       context.CancelFunc
	external     func() error
	reply        chan screenReply
	sections     []sectionSpan
	markdown     []markdownSpan
	fields       []fieldSpan
	inputOptions *InputOptions
	notice       *NoticeOptions
}

type screenReply struct {
	text    string
	choices []Choice
	err     error
}

type selectorMessage struct {
	generation uint64
	message    tea.Msg
}

type headerMessage string

// updateMessage 只携带已经比较过的版本标签，供首页非阻塞刷新。
// 其他页面保留其导航和输入状态，空标签表示移除更新提示。
type updateMessage string

type sessionModel struct {
	session      *Session
	request      screenRequest
	header       string
	width        int
	height       int
	cursor       int
	offset       int
	groupIndex   int
	query        string
	editing      bool
	input        []rune
	inputCursor  int
	generation   uint64
	selector     *model
	focus        map[string]string
	cancel       context.CancelFunc
	emphasis     func(string) string
	style        func(output.Style, string) string
	location     string
	updateLatest string
}

// RunSession 保持唯一全屏和输入所有权，并执行串行页面控制器。
// 调用方先验证三流终端，clean 用于脱敏；退出取消控制器、等待查询并恢复终端。
// 控制器不得在返回后保留 Session，原始流须使用 External 交接。
func RunSession(ctx context.Context, input io.Reader, diagnostic io.Writer, color string, clean func(string) string, run func(context.Context, *Session) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	session := &Session{
		ctx:        ctx,
		diagnostic: diagnostic,
		color:      color,
		clean:      clean,
	}
	initial := sessionModel{
		session: session,
		width:   80,
		height:  24,
		focus:   map[string]string{},
		emphasis: func(value string) string {
			return output.Emphasis(diagnostic, color, value)
		},
		style: func(role output.Style, value string) string {
			return output.StyleText(diagnostic, color, role, value)
		},
	}
	session.program = tea.NewProgram(initial, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(diagnostic), tea.WithAltScreen(), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		err := run(ctx, session)
		done <- err
		session.program.Quit()
	}()
	_, renderErr := session.program.Run()
	wasCancelled := ctx.Err() != nil
	cancel()
	controllerErr := <-done
	if renderErr != nil && !wasCancelled {
		return app.Wrap(app.IOError, "The interactive session could not run.", renderErr)
	}
	return controllerErr
}

// Write 有界暂存业务显示内容，供输入预览或结果页提取。
// 超过 2 MiB 明确标记省略；Git／API 原始流不得通过此显示缓存。
func (s *Session) Write(value []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := max(0, (2<<20)-s.buffer.Len())
	written := value[:min(len(value), remaining)]
	_, _ = s.buffer.Write(written)
	s.trackLines(written)
	s.truncated = s.truncated || len(value) > remaining
	return len(value), nil
}

// WriteSection 缓冲由结果格式化器标识的可信章节标题，供 Display 保留标题样式。
// 远端字段只作为普通 Write 内容处理，不能通过输入控制字符声明 UI 角色。
func (s *Session) WriteSection(title string) (int, error) {
	cleanTitle := output.SingleLine(s.clean(title))
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := max(0, (2<<20)-s.buffer.Len())
	if remaining < len(cleanTitle) {
		s.truncated = true
		return len(title), nil
	}
	_, _ = s.buffer.WriteString(cleanTitle)
	s.trackLines([]byte(cleanTitle))
	s.sections = append(s.sections, sectionSpan{
		line:  s.line,
		depth: s.lineIndent / 2,
		title: cleanTitle,
		role:  output.StyleSection,
	})
	return len(title), nil
}

// Drain 提取并清空本轮显示输出，展示前统一脱敏。
// 缓存截断不影响类型化结果，也不能当作完整原始响应使用。
func (s *Session) Drain() string {
	s.mu.Lock()
	value := s.buffer.String()
	if s.truncated {
		value += "\n[Display limit reached; remaining output omitted.]"
	}
	truncated := s.truncated
	cleanValue := s.clean(value)
	s.buffer.Reset()
	s.truncated = false
	s.pending = append([]sectionSpan(nil), s.sections...)
	s.pendingMarkdown = append([]markdownSpan(nil), s.markdown...)
	s.pendingFields = append([]fieldSpan(nil), s.fields...)
	s.fields = nil
	s.markdown = nil
	s.pendingBody = cleanValue
	s.pendingTruncated = truncated
	s.sections = nil
	s.line = 0
	s.lineIndent = 0
	s.lineStarted = false
	s.mu.Unlock()
	return cleanValue
}

// trackLines 增量记录写入缓存的逻辑行与行首缩进，避免章节写入重复扫描全文。
// 嵌套深度只依据章节前的空白计算，正文及远端内容不参与角色推断。
func (s *Session) trackLines(value []byte) {
	for _, character := range value {
		if character == '\n' {
			s.line++
			s.lineIndent = 0
			s.lineStarted = false
			continue
		}
		if s.lineStarted {
			continue
		}
		if character == ' ' || character == '\t' {
			s.lineIndent++
			continue
		}
		s.lineStarted = true
	}
}

// Header 更新所有页面共享的非敏感上下文摘要。
// 调用方负责提供身份状态，会话不从 Git 作者信息猜测 API 账号。
func (s *Session) Header(value string) {
	s.program.Send(headerMessage(s.clean(value)))
}

// UpdateAvailable 异步更新 Home 的版本提醒，不抢占当前输入或页面。
// value 只能由本地更新服务提供；空值清除提醒，终端控制符在发送前被清理。
func (s *Session) UpdateAvailable(value string) {
	s.program.Send(updateMessage(output.SingleLine(s.clean(value))))
}

// request 将页面交给唯一 UI 循环，等待显式结果或取消。
// 页面回复有界缓冲；取消不会解释为服务端写入回滚。
func (s *Session) request(ctx context.Context, request screenRequest) (screenReply, error) {
	request.title = s.clean(request.title)
	request.body = s.clean(request.body)
	request.ctx = ctx
	request.reply = make(chan screenReply, 1)
	if request.kind == "display" || request.kind == "prompt" {
		s.mu.Lock()
		matchesPending := s.pendingBody != "" && strings.HasPrefix(request.body, s.pendingBody)
		if matchesPending {
			request.sections = append([]sectionSpan(nil), s.pending...)
			request.markdown = append([]markdownSpan(nil), s.pendingMarkdown...)
			request.fields = append([]fieldSpan(nil), s.pendingFields...)
		}
		truncatedPreview := matchesPending && s.pendingTruncated && request.kind == "prompt"
		s.pending = nil
		s.pendingMarkdown = nil
		s.pendingFields = nil
		s.pendingBody = ""
		s.pendingTruncated = false
		s.mu.Unlock()
		if truncatedPreview {
			return screenReply{}, app.Wrap(app.IOError, "The confirmation preview is incomplete; refusing to submit an unverified plan.", nil)
		}
	}
	if err := ctx.Err(); err != nil {
		return screenReply{}, app.Failure(err)
	}
	s.program.Send(request)
	select {
	case reply := <-request.reply:
		return reply, reply.err
	case <-ctx.Done():
		return screenReply{}, app.Failure(ctx.Err())
	case <-s.ctx.Done():
		return screenReply{}, app.Failure(s.ctx.Err())
	}
}

// Navigate 展示无勾选框的单层菜单，Enter 直接进入焦点项。
// Esc 返回取消；按菜单标题和稳定 ID 恢复焦点，搜索只改变视图。
func (s *Session) Navigate(ctx context.Context, title string, choices []Choice) (string, error) {
	reply, err := s.request(ctx, screenRequest{
		kind:    "navigate",
		title:   title,
		choices: choices,
	})
	return reply.text, err
}

// NavigateWithDefault 以明确的默认 ID 展示单层菜单，覆盖该页历史焦点。
// 默认 ID 不存在或为空时选择首项；Esc 返回取消，不修改业务设置，choices 由调用方拥有。
func (s *Session) NavigateWithDefault(ctx context.Context, title string, choices []Choice, selected string) (string, error) {
	reply, err := s.request(ctx, screenRequest{
		kind:         "navigate",
		title:        title,
		choices:      choices,
		initialFocus: &selected,
	})
	return reply.text, err
}

// Prompt 在持续界面读取一行，空行和首尾空白保持原始语义。
// secret 时完全隐藏输入；body 中的确认预览支持滚动，不把 Esc 当空值。
func (s *Session) Prompt(ctx context.Context, title, body string, secret bool) (string, error) {
	reply, err := s.request(ctx, screenRequest{
		kind:         "prompt",
		title:        title,
		body:         body,
		secret:       secret,
		inputOptions: &InputOptions{Title: title, Label: "Your response", Secret: secret},
	})
	return reply.text, err
}

// Display 保留可滚动结果，直到用户按 Esc 或 Enter 返回。
// 不退出全屏、不重执行业务，返回不会更改已取得的写入结果。
func (s *Session) Display(ctx context.Context, title, body string) error {
	_, err := s.request(ctx, screenRequest{
		kind:  "display",
		title: title,
		body:  body,
	})
	return err
}

// Busy 展示加载态并安装动作取消函数，立即交还业务控制器。
// Esc 只取消当前动作，保留会话以展示最终失败或 unknown 结果。
func (s *Session) Busy(ctx context.Context, title string, cancel context.CancelFunc) error {
	_, err := s.request(ctx, screenRequest{
		kind:   "busy",
		title:  title,
		cancel: cancel,
	})
	return err
}

// Select 在同一全屏内复用跨页资源选择器，保持稳定 ID 和明确勾选。
// 退出取消并等待 provider；关闭门闩阻止尚未启动的命令在返回后进入 provider。
func (s *Session) Select(ctx context.Context, title string, multi bool, provider Provider) ([]Choice, error) {
	ctx, cancel := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	var guard sync.Mutex
	closed := false
	wrapped := func(ctx context.Context, page int, query string) (Page, error) {
		guard.Lock()
		if closed {
			guard.Unlock()
			return Page{}, context.Canceled
		}
		tasks.Add(1)
		guard.Unlock()
		defer tasks.Done()
		values, err := provider(ctx, page, query)
		values.Choices = append([]Choice(nil), values.Choices...)
		for index := range values.Choices {
			values.Choices[index].Label = s.clean(values.Choices[index].Label)
			values.Choices[index].Description = s.clean(values.Choices[index].Description)
		}
		return values, err
	}
	reply, err := s.request(ctx, screenRequest{
		kind:     "select",
		title:    title,
		provider: wrapped,
		multi:    multi,
	})
	guard.Lock()
	closed = true
	cancel()
	guard.Unlock()
	tasks.Wait()
	return reply.choices, err
}

// External 暂停渲染并交出真实终端，执行已准备好的原始流操作。
// run 不得调用会话方法且须响应自身 context；返回后恢复全屏，保留原始错误。
func (s *Session) External(ctx context.Context, run func() error) error {
	_, err := s.request(ctx, screenRequest{
		kind:     "external",
		external: run,
	})
	return err
}

// Init 等待控制器发送首个页面，不在渲染循环读取配置。
// 所有网络操作由控制器或资源 provider 承担。
func (m sessionModel) Init() tea.Cmd {
	return nil
}

// finish 回复当前页面并清空敏感输入，回到加载态。
// 子选择器退出只结束当前页面，不触发整个程序的 Quit。
func (m *sessionModel) finish(reply screenReply) {
	if m.selector != nil {
		m.selector.cancel()
		m.selector = nil
	}
	if m.request.reply != nil {
		m.request.reply <- reply
	}
	m.request = screenRequest{
		kind:  "busy",
		title: "Working...",
	}
	m.input = nil
	m.offset = 0
}

// tagged 将子选择器的异步响应绑定到页面代数。
// 请求编号只在单页内有效，旧页面不得覆盖后来打开的选择器。
func tagged(generation uint64, command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		return selectorMessage{
			generation: generation,
			message:    command(),
		}
	}
}

// Update 串行切换页面并转发选择器事件，不启动第二个终端读取器。
// 子模型的退出转换为页面回复，过期异步响应按页面代数丢弃。
func (m sessionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case updateMessage:
		m.updateLatest = string(value)
		return m, nil
	case headerMessage:
		m.header = string(value)
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, value.Width), max(1, value.Height)
		if m.selector != nil {
			message = tea.WindowSizeMsg{
				Width:  m.width,
				Height: max(1, m.height-3),
			}
		}
	case screenRequest:
		if m.selector != nil {
			m.selector.cancel()
		}
		m.generation++
		m.request = value
		m.updateLocation(value)
		m.cursor, m.offset, m.groupIndex, m.query, m.editing, m.input = 0, 0, 0, "", false, nil
		m.inputCursor = 0
		if value.inputOptions != nil {
			m.input = []rune(value.inputOptions.Value)
			m.inputCursor = len(m.input)
		}
		m.selector = nil
		switch value.kind {
		case "busy":
			m.cancel = value.cancel
			m.request.reply <- screenReply{}
			m.request.reply = nil
		case "navigate", "notice":
			selected := m.focus[value.title]
			if value.initialFocus != nil {
				selected = *value.initialFocus
			}
			for index, choice := range value.choices {
				if choice.ID == selected {
					m.cursor = index
				}
			}
		case "select":
			requestCtx, cancel := context.WithCancel(value.ctx)
			m.selector = &model{
				ctx:        value.ctx,
				requestCtx: requestCtx,
				cancel:     cancel,
				provider:   value.provider,
				title:      value.title,
				multi:      value.multi,
				page:       1,
				requestID:  1,
				selected:   map[string]Choice{},
				loading:    true,
				width:      m.width,
				height:     max(1, m.height-3),
				emphasis:   m.emphasis,
				style:      m.style,
				formatHints: func(width int, primary, secondary, back string) string {
					return output.FormatKeyHints(
						m.session.diagnostic,
						m.session.color,
						width,
						primary,
						secondary,
						back,
					)
				},
			}
			return m, tagged(m.generation, m.selector.Init())
		case "external":
			return m, tea.Exec(&terminalAction{run: value.external}, func(err error) tea.Msg {
				return externalReply{
					reply: value.reply,
					err:   err,
				}
			})
		}
		return m, nil
	case externalReply:
		value.reply <- screenReply{err: value.err}
		m.request.reply = nil
		m.request.kind = "busy"
		return m, nil
	case selectorMessage:
		if value.generation != m.generation || m.selector == nil {
			return m, nil
		}
		message = value.message
	}
	if m.selector != nil {
		next, command := m.selector.Update(message)
		child := next.(model)
		m.selector = &child
		if child.finished || child.cancelled {
			reply := screenReply{}
			if child.cancelled {
				reply.err = app.Wrap(app.Cancelled, "Selection cancelled.", context.Canceled)
			} else {
				for _, id := range child.order {
					if choice, exists := child.selected[id]; exists {
						reply.choices = append(reply.choices, choice)
					}
				}
			}
			m.finish(reply)
			return m, nil
		}
		return m, tagged(m.generation, command)
	}
	if key, ok := message.(tea.KeyMsg); ok {
		return m.key(key), nil
	}
	return m, nil
}

// key 处理直接进入、输入提交与结果滚动的页面语义。
// Esc 在结果页仅返回，加载中仅取消当前业务，不重放请求。
func (m sessionModel) key(key tea.KeyMsg) sessionModel {
	name := key.String()
	if m.request.kind == "busy" {
		if (name == "esc" || name == "ctrl+c") && m.cancel != nil {
			m.cancel()
			m.request.title = "Cancelling... Waiting for the final outcome."
		}
		return m
	}
	if m.request.kind == "display" {
		if name == "esc" || name == "enter" || name == "ctrl+c" {
			m.finish(screenReply{})
			return m
		}
		if m.changeGroup(name) {
			return m
		}
		m.scroll(name)
		return m
	}
	if name == "esc" || name == "ctrl+c" || name == "ctrl+d" {
		if (m.request.kind == "navigate" || m.request.kind == "notice") && m.editing && name == "esc" {
			m.editing = false
			return m
		}
		m.finish(screenReply{err: app.Wrap(app.Cancelled, "Selection cancelled.", context.Canceled)})
		return m
	}
	if m.request.kind == "prompt" {
		if m.changeGroup(name) {
			return m
		}
		switch name {
		case "enter":
			m.finish(screenReply{text: string(m.input)})
		case "backspace", "ctrl+h":
			if m.inputCursor > 0 {
				m.input = append(m.input[:m.inputCursor-1], m.input[m.inputCursor:]...)
				m.inputCursor--
			}
		case "delete":
			if m.inputCursor < len(m.input) {
				m.input = append(m.input[:m.inputCursor], m.input[m.inputCursor+1:]...)
			}
		case "left":
			m.inputCursor = max(0, m.inputCursor-1)
		case "right":
			m.inputCursor = min(len(m.input), m.inputCursor+1)
		case "ctrl+u":
			m.input = nil
			m.inputCursor = 0
		case "home", "end":
			if strings.TrimSpace(m.request.body) != "" {
				m.scroll(name)
			} else if name == "home" {
				m.inputCursor = 0
			} else {
				m.inputCursor = len(m.input)
			}
		case "pgup", "pgdown", "up", "down":
			m.scroll(name)
		default:
			if key.Type == tea.KeyRunes && len(m.input)+len(key.Runes) <= 16384 {
				next := append([]rune(nil), m.input[:m.inputCursor]...)
				next = append(next, key.Runes...)
				m.input = append(next, m.input[m.inputCursor:]...)
				m.inputCursor += len(key.Runes)
			} else if name == " " && len(m.input) < 16384 {
				next := append([]rune(nil), m.input[:m.inputCursor]...)
				next = append(next, ' ')
				m.input = append(next, m.input[m.inputCursor:]...)
				m.inputCursor++
			}
		}
		return m
	}
	if m.request.kind != "navigate" && m.request.kind != "notice" {
		return m
	}
	if m.request.kind == "notice" && (name == "pgup" || name == "pgdown") {
		if name == "pgup" {
			m.offset = max(0, m.offset-max(1, m.height/2))
		} else {
			m.offset += max(1, m.height/2)
		}
		return m
	}
	if m.editing {
		switch name {
		case "enter":
			m.editing = false
		case "backspace":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
		default:
			if key.Type == tea.KeyRunes && len(m.query) < 256 {
				m.query += string(key.Runes)
			} else if name == " " && len(m.query) < 256 {
				m.query += " "
			}
		}
		m.cursor = 0
		return m
	}
	choices := m.navigation()
	switch name {
	case "/":
		m.editing = true
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(max(0, len(choices)-1), m.cursor+1)
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = max(0, len(choices)-1)
	case "enter":
		if m.request.kind == "notice" && len(choices) == 0 {
			m.finish(screenReply{})
			return m
		}
		if m.cursor < len(choices) {
			choice := choices[m.cursor]
			m.focus[m.request.title] = choice.ID
			m.finish(screenReply{text: choice.ID})
		}
	}
	return m
}

// navigation 按名称和稳定路径筛选当前菜单，不改变业务目标。
// 空集时 Enter 不沿用旧焦点；Home 更新入口的强调仅改变显示，不改变稳定 ID。
func (m sessionModel) navigation() []Choice {
	choices := []Choice{}
	decorate := func(choice Choice) Choice {
		if m.request.home != nil && choice.ID == "update" && m.updateLatest != "" {
			choice.Label = "Update available · " + m.updateLatest
			choice.Description = "Review and install the verified GitHub release"
			choice.Role = output.StyleWarning
		}
		return choice
	}
	for _, choice := range m.request.choices {
		if m.query != "" && strings.EqualFold(choice.ID, strings.TrimSpace(m.query)) {
			return []Choice{decorate(choice)}
		}
	}
	for _, choice := range m.request.choices {
		if strings.Contains(strings.ToLower(choice.Label+" "+choice.Description+" "+choice.ID), strings.ToLower(m.query)) {
			choices = append(choices, decorate(choice))
		}
	}
	return choices
}

// scroll 有界移动结果或确认预览，长字段仍完整可达。
// 窗口改变后按重新换行的长度收敛偏移，避免越界。
func (m *sessionModel) scroll(key string) {
	_, lines, _ := m.contentLines()
	height := m.contentHeight()
	switch key {
	case "up", "k":
		m.offset--
	case "down", "j":
		m.offset++
	case "pgup":
		m.offset -= height
	case "pgdown":
		m.offset += height
	case "home":
		m.offset = 0
	case "end":
		m.offset = len(lines)
	}
	m.offset = min(max(0, m.offset), max(0, len(lines)-height))
}

// changeGroup 切换可信章节分页，并在新组从顶部开始展示。
// 只有顶层章节参与分页，嵌套章节留在所属组；没有章节标注时保留普通滚动行为。
func (m *sessionModel) changeGroup(key string) bool {
	groups := sectionGroups(m.request.body, m.request.sections)
	if len(groups) == 0 {
		return false
	}
	switch key {
	case "pgup", "shift+tab":
		m.groupIndex = max(0, m.groupIndex-1)
	case "pgdown", "tab":
		m.groupIndex = min(len(groups)-1, m.groupIndex+1)
	default:
		return false
	}
	m.offset = 0
	return true
}

// contentLines 返回当前页内容、换行后的可视行和所属章节样式索引。
// 分组页只截取当前顶层章节，未分组页面仍可访问完整正文。
func (m sessionModel) contentLines() (string, []string, map[int]output.Style) {
	raw := strings.Split(strings.ReplaceAll(m.request.body, "\r\n", "\n"), "\n")
	groups := sectionGroups(m.request.body, m.request.sections)
	if len(groups) == 0 {
		lines, roles := m.renderedRange(raw, 0, len(raw))
		return m.request.body, lines, roles
	}
	group := groups[min(max(0, m.groupIndex), len(groups)-1)]
	prefix, roles := m.renderedRange(raw, 0, groups[0].start)
	lines, groupRoles := m.renderedRange(raw, group.start, group.end)
	for index, role := range groupRoles {
		roles[len(prefix)+index] = role
	}
	lines = append(prefix, lines...)
	return strings.Join(lines, "\n"), lines, roles
}

// contentHeight 为当前页面和输入控件保留固定空间后计算正文视口。
// 提示区不会被正文滚动覆盖，窄屏也至少保留一行可读内容。
func (m sessionModel) contentHeight() int {
	reserved := len(m.pageHeader()) + 3
	if m.request.kind == "prompt" {
		reserved += len(m.inputBox()) + 1
	}
	if len(sectionGroups(m.request.body, m.request.sections)) > 0 {
		reserved += 2
		if m.request.kind == "prompt" && strings.Contains(m.request.title, "[y/N]") {
			reserved += 2
		}
	}
	return max(1, m.height-reserved)
}

// View 将页面限制在窗口内，正文按完整字素折行而非丢弃字段。
// 所有页面底部显示当前位置；子选择器沿用同一页脚，远端控制字符仍作为数据处理。
func (m sessionModel) View() string {
	if m.request.kind == "navigate" && m.request.home != nil {
		return m.homeView()
	}
	if m.selector != nil {
		parts := strings.Split(m.cleanText(m.header), " · ")
		contextLine := strings.Join(parts[:min(2, len(parts))], " / ")
		prefix := "  " + m.paint(output.StylePrimary, "sotea") + "\n  " + m.paint(output.StyleDescription, output.Truncate(contextLine, m.contentWidth())) + "\n\n"
		lines := strings.Split(strings.TrimSuffix(m.selector.View(), "\n"), "\n")
		if len(lines) >= 2 {
			lines[len(lines)-2] = "  " + m.paint(output.StyleLocation, output.Truncate(m.pageLocation(), m.contentWidth()))
		}
		return prefix + strings.Join(lines, "\n") + "\n"
	}
	switch m.request.kind {
	case "navigate":
		return m.navigationView(m.pageHeader())
	case "notice":
		return m.noticeView()
	case "display", "prompt":
		return m.resultView()
	default:
		lines := append(m.pageHeader(), m.paint(output.StyleSection, "Loading..."))
		return m.frame(lines, "", "Please wait", "Esc Cancel")
	}
}

// sessionDiagnostic 返回已绑定的终端输出流，供帮助提示沿用会话颜色策略。
// 没有公开终端状态时返回 nil，StyleText 会按 auto 策略降级为纯文本。
func (m sessionModel) sessionDiagnostic() io.Writer {
	return m.session.diagnostic
}

// sessionColor 返回当前会话颜色策略，避免提示区另建色彩判定规则。
// 测试模型或未初始化会话使用 auto，与 CLI 默认设置一致。
func (m sessionModel) sessionColor() string {
	return m.session.color
}

// pageTitle 保留确认语义后缀，防止窄窗口截去 [y/N] 中的否决选项。
// 其他页面标题仍按完整字素截断；极窄确认页至少显示 No 的选择语义。
func pageTitle(title string, width int, confirmation bool) string {
	const suffix = "[y/N]"
	if !confirmation || !strings.HasSuffix(title, suffix) {
		return output.Truncate(title, width)
	}
	if width < uniseg.StringWidth(suffix) {
		return output.Truncate("No", width)
	}
	if width == uniseg.StringWidth(suffix) {
		return suffix
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(title, suffix))
	remaining := width - uniseg.StringWidth(suffix) - 1
	if prefix == "" {
		return suffix
	}
	return output.Truncate(prefix, remaining) + " " + suffix
}

// keyHintText 为无样式测试模型生成与正式提示相同的截断文本。
// 该路径不插入控制字符，保持非终端输出和快照中的纯文本语义。
func keyHintText(width int, primary, secondary, back string) string {
	parts := []string{}
	for _, part := range []string{primary, secondary, back} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return output.Truncate(strings.Join(parts, " · "), width)
}

// wrapScreen 清理控制字符并按字素折行，保留所有可显示正文。
// 极窄窗口也始终推进，超宽单字素独占一行，避免循环停滞。
func wrapScreen(value string, width int) []string {
	lines, _, _ := wrapScreenAnnotated(value, width, nil)
	return lines
}

// wrapScreenAnnotated 按逻辑行折叠正文并保留章节行的终端样式位置。
// 行号来自 SectionWriter 元数据，不解析正文标记或远端控制字符。
func wrapScreenAnnotated(value string, width int, sections []sectionSpan) ([]string, map[int]output.Style, []int) {
	lines := []string{}
	styles := map[int]output.Style{}
	starts := []int{}
	sectionRoles := map[int]output.Style{}
	for _, section := range sections {
		sectionRoles[section.line] = section.role
	}
	for lineNumber, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		starts = append(starts, len(lines))
		role, marked := sectionRoles[lineNumber]
		firstLine := len(lines)
		var current strings.Builder
		used := 0
		graphemes := uniseg.NewGraphemes(output.SingleLine(line))
		for graphemes.Next() {
			part, size := graphemes.Str(), graphemes.Width()
			if used+size > max(1, width) && current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
				used = 0
			}
			current.WriteString(part)
			used += size
		}
		lines = append(lines, current.String())
		if marked {
			styles[firstLine] = role
		}
	}
	return lines, styles, starts
}

type displayGroup struct {
	start int
	end   int
	title string
}

// sectionGroups 依据顶层可信标题将详情拆分成可横向浏览的完整章节。
// 前置标量字段作为 Summary 保留，嵌套章节仍属于其最近的顶层分组。
func sectionGroups(body string, sections []sectionSpan) []displayGroup {
	lineCount := len(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
	roots := []sectionSpan{}
	for _, section := range sections {
		if section.depth == 0 && section.line >= 0 && section.line < lineCount {
			roots = append(roots, section)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	groups := []displayGroup{}
	for index, root := range roots {
		end := lineCount
		if index+1 < len(roots) {
			end = roots[index+1].line
		}
		groups = append(groups, displayGroup{
			start: root.line,
			end:   end,
			title: root.title,
		})
	}
	return groups
}

type externalReply struct {
	reply chan screenReply
	err   error
}

type terminalAction struct {
	run func() error
}

// Run 执行原始流闭包，Bubble Tea 已在此前暂停输入和渲染。
// 回调不得请求会话页面，否则会阻塞当前消息循环。
func (a *terminalAction) Run() error {
	return a.run()
}

// SetStdin 保留闭包捕获的真实输入流。
// 交接接口需要此方法，但不替换子进程的流所有权。
func (a *terminalAction) SetStdin(io.Reader) {
}

// SetStdout 保留闭包捕获的真实输出流。
// 原始字节不经过显示缓存或文本解析。
func (a *terminalAction) SetStdout(io.Writer) {
}

// SetStderr 保留闭包捕获的真实诊断流。
// 子进程结束后由 Bubble Tea 恢复全屏。
func (a *terminalAction) SetStderr(io.Writer) {
}
