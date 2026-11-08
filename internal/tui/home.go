package tui

import (
	"context"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/rivo/uniseg"
)

// HomeInfo 提供首页展示的非敏感上下文和已核对的 API 用户资料。
// Login、Name、Email 仅接受成功身份验证后的响应；失败和匿名状态不得保留先前资料。
type HomeInfo struct {
	Version       string
	ServerVersion string
	Instance      string
	Repository    string
	Directory     string
	Login         string
	Name          string
	Email         string
	Status        string
	LastResult    string
}

// Home 在持续会话展示上下文卡片及主菜单，返回用户明确进入的稳定动作 ID。
// 传入资料按值复制并脱敏；Esc 返回取消，窗口变小时压缩卡片以保留菜单和操作说明。
func (s *Session) Home(ctx context.Context, info HomeInfo, choices []Choice) (string, error) {
	fields := []*string{
		&info.Version, &info.ServerVersion, &info.Instance, &info.Repository,
		&info.Directory, &info.Login, &info.Name, &info.Email, &info.Status, &info.LastResult,
	}
	for _, field := range fields {
		*field = output.SingleLine(s.clean(*field))
	}
	reply, err := s.request(ctx, screenRequest{
		kind:    "navigate",
		title:   "Home · Choose an action",
		choices: choices,
		home:    &info,
	})
	return reply.text, err
}

// homeView 按当前尺寸安排首页信息卡、动作列表和固定键位说明。
// 菜单焦点始终可见；后台更新消息仅增加提醒，不重置焦点、搜索或当前选择。
func (m sessionModel) homeView() string {
	cardModel := m
	cardModel.width = m.contentWidth()
	card := cardModel.homeCard(*m.request.home)
	if m.updateLatest != "" {
		card = append(card, "", m.paint(output.StyleWarning, output.Truncate("Update available · "+m.updateLatest+" · Open Update to install", m.contentWidth())))
	}
	lines := append(card, "", m.paint(output.StyleTitle, output.Truncate(m.request.title, m.contentWidth())), "")
	return m.navigationView(lines)
}

// homeCard 生成接近终端开发工具欢迎区的信息卡，真实账号与仓库分行展示。
// 极窄或低矮窗口使用紧凑摘要；可选资料缺失时省略，不伪造邮箱或昵称。
func (m sessionModel) homeCard(info HomeInfo) []string {
	user := "Anonymous · Sign in through Account"
	if info.Login != "" {
		user = "@" + info.Login
		if info.Name != "" && info.Name != info.Login {
			user = info.Name + "  (" + user + ")"
		}
	}
	repository := info.Repository
	if repository == "" {
		repository = "No repository selected"
	}
	title := "sotea"
	if info.Version != "" {
		title += "  " + info.Version
	}
	if m.height < 18 || m.width < 40 {
		lines := []string{
			output.Truncate(title, m.width),
			output.Truncate(user+" · "+repository, m.width),
		}
		if m.height >= 12 {
			lines = append(lines, output.Truncate(info.Status, m.width))
		}
		if m.emphasis != nil {
			lines[0] = m.emphasis(lines[0])
		}
		return lines
	}
	width := min(94, m.width)
	inner := width - 4
	rows := []string{"Account     " + user}
	if info.Email != "" && m.height >= 26 {
		rows = append(rows, "Email       "+info.Email)
	}
	if info.Instance != "" {
		rows = append(rows, "Instance    "+info.Instance)
	}
	rows = append(rows, "Repository  "+repository)
	if info.Directory != "" && m.height >= 24 {
		rows = append(rows, "Directory   "+info.Directory)
	}
	rows = append(rows, "Status      "+info.Status)
	if info.LastResult != "" && m.height >= 30 {
		rows = append(rows, "Last        "+info.LastResult)
	}
	heading := output.Truncate(" "+title+" ", width-4)
	border := "╭─" + heading + strings.Repeat("─", max(0, width-uniseg.StringWidth(heading)-3)) + "╮"
	if m.emphasis != nil {
		border = m.emphasis(border)
	}
	lines := []string{border}
	for _, row := range rows {
		content := output.Truncate(row, inner)
		lines = append(lines, "│ "+content+strings.Repeat(" ", max(0, inner-uniseg.StringWidth(content)))+" │")
	}
	lines = append(lines, "╰"+strings.Repeat("─", width-2)+"╯")
	return lines
}
