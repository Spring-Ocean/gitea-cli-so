package tui

import (
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// TestHomeUpdateRefreshPreservesFocusAndInput 保护后台更新提示不抢占用户导航。
// 有更新时首页和入口高亮；取消提示后恢复普通入口，低矮窗口仍保留按键。
func TestHomeUpdateRefreshPreservesFocusAndInput(t *testing.T) {
	for _, size := range [][2]int{{100, 32}, {40, 18}, {28, 12}} {
		info := HomeInfo{Version: "v0.1.0-rc.1", Status: "Repository unavailable"}
		current := sessionModel{
			session: &Session{color: "never"},
			width:   size[0],
			height:  size[1],
			cursor:  1,
			focus:   map[string]string{},
			request: screenRequest{
				kind: "navigate", title: "Home · Choose an action", home: &info,
				choices: []Choice{{ID: "repo", Label: "Repository"}, {ID: "update", Label: "Update"}, {ID: "quit", Label: "Quit"}},
			},
		}
		next, _ := current.Update(updateMessage("v0.2.0"))
		current = next.(sessionModel)
		if current.cursor != 1 || current.request.kind != "navigate" {
			t.Fatal("background update changed the active page or focus")
		}
		choices := current.navigation()
		if choices[1].ID != "update" || choices[1].Role != output.StyleWarning || !strings.Contains(choices[1].Label, "v0.2.0") {
			t.Fatal("the update action was not highlighted with its stable ID")
		}
		view := current.View()
		if !strings.Contains(view, "Update available") || !strings.Contains(view, "Enter") || !strings.Contains(view, "Esc") {
			t.Fatal("the update notice hid navigation controls")
		}
		for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
			if uniseg.StringWidth(line) > size[0] {
				t.Fatal("the update notice overflowed the terminal")
			}
		}
		current.query, current.editing = "update", true
		next, _ = current.Update(updateMessage(""))
		current = next.(sessionModel)
		if current.query != "update" || !current.editing || current.navigation()[0].Role != "" {
			t.Fatal("clearing the update notice reset user input or retained emphasis")
		}
	}
}

// TestHomeProfileLayoutAndNavigation 保护首页资料可见、宽度边界和末项可达。
// 窄窗口保留焦点与键位，远端字段经过控制符清理，不断言完整样式快照。
func TestHomeProfileLayoutAndNavigation(t *testing.T) {
	redactor := output.NewRedactor("hidden-secret")
	for _, size := range [][2]int{{100, 32}, {80, 24}, {40, 18}, {28, 12}} {
		info := HomeInfo{
			Version:    "dev",
			Instance:   "https://gitea.example.invalid",
			Repository: "team/中文👩🏽‍💻-repo",
			Directory:  "/workspace/project",
			Login:      "alice",
			Name:       "Alice Ocean",
			Email:      "alice@example.invalid",
			Status:     "Connected · Gitea 28.3.0",
		}
		choices := []Choice{}
		for _, name := range []string{"Repository", "Issues", "Pull requests", "Releases", "Branches", "Tags", "Commits", "Account", "Git", "API", "Context", "Quit"} {
			choices = append(choices, Choice{
				ID:    name,
				Label: name,
			})
		}
		reply := make(chan screenReply, 1)
		current := sessionModel{
			session: &Session{clean: redactor.Text},
			width:   size[0],
			height:  size[1],
			focus:   map[string]string{},
			request: screenRequest{
				kind:    "navigate",
				title:   "Home · Choose an action",
				choices: choices,
				home:    &info,
				reply:   reply,
			},
		}
		view := current.View()
		if size[1] >= 26 && (!strings.Contains(view, "Alice Ocean") || !strings.Contains(view, "alice@example.invalid")) {
			t.Fatal("the full-height home screen lost its API profile")
		}
		current = current.key(tea.KeyMsg{Type: tea.KeyEnd})
		view = current.View()
		if !strings.Contains(view, "> Quit") || !strings.Contains(view, "Enter") {
			t.Fatal("the compact home screen hid the selected action or controls")
		}
		lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
		if len(lines) > size[1] {
			t.Fatalf("home exceeded height %d: %d", size[1], len(lines))
		}
		for _, line := range lines {
			if uniseg.StringWidth(line) > size[0] {
				t.Fatalf("home exceeded width %d: %q", size[0], line)
			}
		}
		current = current.key(tea.KeyMsg{Type: tea.KeyEnter})
		if value := <-reply; value.text != "Quit" {
			t.Fatal("Enter did not accept the last visible action")
		}
	}
}
