package tui

import (
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
)

// commandLocation 将本地命令路径转为面向用户的页面层级。
// 仅识别已知入口，不从远端正文或资源名称推断导航路径。
func commandLocation(title string) (string, bool) {
	base, _, _ := strings.Cut(title, " · ")
	base = strings.TrimPrefix(base, "sotea ")
	parts := strings.Fields(base)
	if len(parts) == 0 {
		return "", false
	}
	names := map[string]string{
		"repo":      "Repository",
		"issue":     "Issues",
		"pr":        "Pull requests",
		"release":   "Releases",
		"branch":    "Branches",
		"tag":       "Tags",
		"commit":    "Commits",
		"label":     "Labels",
		"milestone": "Milestones",
		"auth":      "Account",
		"instance":  "Instance",
		"git":       "Git",
		"api":       "API",
		"version":   "Version",
	}
	root, exists := names[parts[0]]
	if !exists {
		return "", false
	}
	path := []string{"Home", root}
	for _, part := range parts[1:] {
		name := strings.ReplaceAll(part, "-", " ")
		runes := []rune(name)
		path = append(path, strings.ToUpper(string(runes[0]))+string(runes[1:]))
	}
	return strings.Join(path, " › "), true
}

// updateLocation 随本地页面入口更新导航基线，结果及表单继承所属业务层。
// 加载态不覆盖已有路径；首页和账户浏览入口明确重置层级，避免返回后残留旧位置。
func (m *sessionModel) updateLocation(request screenRequest) {
	if path, ok := commandLocation(request.title); ok {
		m.location = path
		return
	}
	base, _, _ := strings.Cut(request.title, " · ")
	switch base {
	case "Home":
		m.location = "Home"
	case "Browse account", "Account repositories", "Organizations", "Organization repositories":
		m.location = "Home › Browse account"
		if base != "Browse account" {
			m.location += " › " + base
		}
	case "Account", "Connect account", "Account settings":
		m.location = "Home › Account"
	case "Session context":
		m.location = "Home › Context"
	}
}

// pageLocation 返回底部路径，并在分组详情中追加当前组名。
// 路径只作展示，先清理控制符；不包含退出码，也不承担成功或失败的状态表达。
func (m sessionModel) pageLocation() string {
	path := m.location
	if path == "" {
		path = "Home"
	}
	base, _, _ := strings.Cut(m.cleanText(m.request.title), " · ")
	_, command := commandLocation(base)
	if m.request.kind != "busy" && !command && base != "Home" && base != "" && !strings.HasSuffix(path, base) {
		path += " › " + base
	}
	groups := sectionGroups(m.request.body, m.request.sections)
	if len(groups) > 0 {
		path += " › " + groups[min(max(0, m.groupIndex), len(groups)-1)].title
	}
	return output.SingleLine(m.cleanText(path))
}
