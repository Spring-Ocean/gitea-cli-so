package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestSessionNavigationAndRetainedResult 保护 Enter 直接进入和屏内结果返回契约。
// 返回同层后恢复稳定焦点，Esc 只回复当前页面，不生成整个程序的 Quit。
func TestSessionNavigationAndRetainedResult(t *testing.T) {
	current := sessionModel{
		session: &Session{clean: func(value string) string {
			return value
		}},
		width:  40,
		height: 12,
		focus:  map[string]string{},
	}
	request := screenRequest{
		kind:  "navigate",
		title: "Issues",
		choices: []Choice{
			{
				ID:    "issue list",
				Label: "List",
			},
			{
				ID:    "issue close",
				Label: "Close",
			},
		},
		reply: make(chan screenReply, 1),
	}
	next, _ := current.Update(request)
	current = next.(sessionModel).key(tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(current.View(), "[ ]") || strings.Contains(current.View(), "Space") {
		t.Fatal("navigation still uses resource checkboxes")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEnter})
	if reply := <-request.reply; reply.text != "issue close" || reply.err != nil {
		t.Fatalf("Enter did not directly accept the focused action: %+v", reply)
	}
	result := screenRequest{
		kind:  "display",
		title: "Result",
		body:  strings.Repeat("long result 中文👩🏽‍💻\n", 25) + "FINAL UNKNOWN",
		reply: make(chan screenReply, 1),
	}
	next, command := current.Update(result)
	current = next.(sessionModel)
	if command != nil || !strings.Contains(current.View(), "long result") {
		t.Fatal("the result did not remain in the same screen")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(current.View(), "FINAL UNKNOWN") {
		t.Fatal("the final result was not reachable by scrolling")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEsc})
	if reply := <-result.reply; reply.err != nil {
		t.Fatal("leaving a result changed its outcome")
	}
	request.reply = make(chan screenReply, 1)
	next, _ = current.Update(request)
	current = next.(sessionModel)
	if current.cursor != 1 {
		t.Fatal("returning to the parent lost the stable focus")
	}
	next, command = current.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if command != nil || app.Failure((<-request.reply).err).Code != app.Cancelled {
		t.Fatal("Esc did not return from just the current navigation page")
	}
}

// TestSessionDiscardsDepartedSelectorMessages 保护取消后打开新选择器的异步隔离。
// 两个页面都从请求编号一开始，仍不能接受前页结果或让子 Quit 退出会话。
func TestSessionDiscardsDepartedSelectorMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := sessionModel{
		session: &Session{ctx: ctx},
		width:   80,
		height:  24,
		focus:   map[string]string{},
	}
	request := screenRequest{
		kind: "select",
		ctx:  ctx,
		provider: func(context.Context, int, string) (Page, error) {
			return Page{}, nil
		},
		reply: make(chan screenReply, 1),
	}
	next, _ := current.Update(request)
	current = next.(sessionModel)
	oldGeneration := current.generation
	next, command := current.Update(tea.KeyMsg{Type: tea.KeyEsc})
	current = next.(sessionModel)
	if command != nil || app.Failure((<-request.reply).err).Code != app.Cancelled {
		t.Fatal("resource cancellation quit the full session")
	}
	request.reply = make(chan screenReply, 1)
	next, _ = current.Update(request)
	current = next.(sessionModel)
	defer current.selector.cancel()
	next, _ = current.Update(selectorMessage{
		generation: oldGeneration,
		message: loaded{
			id:   1,
			page: Page{Choices: []Choice{{ID: "stale"}}},
		},
	})
	current = next.(sessionModel)
	if !current.selector.loading || len(current.selector.choices) != 0 {
		t.Fatal("a departed page replaced the current resource query")
	}
}

// TestSessionHiddenInputAndCancellation 保护 token 不回显和输入取消语义。
// 输入首尾空白保留给原适配器校验，Esc 不生成空字符串成功。
func TestSessionHiddenInputAndCancellation(t *testing.T) {
	current := sessionModel{
		session: &Session{},
		width:   80,
		height:  24,
		focus:   map[string]string{},
	}
	request := screenRequest{
		kind:   "prompt",
		secret: true,
		title:  "API token",
		reply:  make(chan screenReply, 1),
	}
	next, _ := current.Update(request)
	current = next.(sessionModel).key(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(" private-token "),
	})
	if strings.Contains(current.View(), "private-token") {
		t.Fatal("hidden input reached the rendered screen")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEnter})
	if reply := <-request.reply; reply.text != " private-token " {
		t.Fatal("the input layer modified literal whitespace")
	}
	request.reply = make(chan screenReply, 1)
	next, _ = current.Update(request)
	current = next.(sessionModel).key(tea.KeyMsg{Type: tea.KeyEsc})
	if app.Failure((<-request.reply).err).Code != app.Cancelled {
		t.Fatal("input cancellation became an empty successful value")
	}
}

// TestSessionSectionsPageByTrustedTopLevelGroup 保护章节分页、嵌套归属与持久摘要。
// 键位只用 PgUp/PgDn 和 Tab/Shift+Tab 切组；窄屏标签栏仍展示活动名称且不越界。
func TestSessionSectionsPageByTrustedTopLevelGroup(t *testing.T) {
	session := &Session{
		clean: func(value string) string {
			return value
		},
	}
	if _, err := session.Write([]byte("issue close: success\n")); err != nil {
		t.Fatal(err)
	}
	render := output.Renderer{Out: session}
	if err := render.Value(map[string]any{
		"full_name":      "team/repo",
		"default_branch": "main",
		"stars_count":    3,
		"clone_url":      "https://git.example/team/repo.git",
		"permissions": map[string]any{
			"pull": true,
		},
		"future_setting": map[string]any{
			"enabled": true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := session.Drain()
	if strings.Contains(body, "SOTEA-SECTION") || strings.Contains(body, "\x1e") {
		t.Fatal("section metadata leaked into the display body")
	}
	groups := sectionGroups(body, session.pending)
	if len(groups) != 5 {
		t.Fatalf("expected five top-level repository groups, got %d", len(groups))
	}
	if groups[3].title != "Access" || groups[4].title != "Additional details" {
		t.Fatalf("nested sections escaped their top-level groups: %+v", groups)
	}
	current := sessionModel{
		session:    session,
		request:    screenRequest{kind: "display", body: body, sections: session.pending},
		width:      80,
		height:     24,
		groupIndex: 0,
	}
	_, firstPage, _ := current.contentLines()
	if !strings.Contains(strings.Join(firstPage, "\n"), "issue close: success") || !strings.Contains(strings.Join(firstPage, "\n"), "Overview") {
		t.Fatal("the first group did not retain the operation summary and section")
	}
	if current.changeGroup("right") || current.groupIndex != 0 {
		t.Fatal("horizontal arrows changed groups")
	}
	if !current.changeGroup("pgdown") || current.groupIndex != 1 {
		t.Fatal("PageDown did not advance to the next group")
	}
	_, secondPage, _ := current.contentLines()
	if !strings.Contains(strings.Join(secondPage, "\n"), "issue close: success") || !strings.Contains(strings.Join(secondPage, "\n"), "Activity") {
		t.Fatal("the next group did not retain the operation summary")
	}
	if !current.changeGroup("tab") || current.groupIndex != 2 || !current.changeGroup("shift+tab") || current.groupIndex != 1 {
		t.Fatal("Tab and Shift+Tab did not navigate groups")
	}
	for _, width := range []int{28, 48, 80} {
		current.width = width
		for _, active := range []int{0, len(groups) - 1} {
			current.groupIndex = active
			bar := ansi.Strip(current.groupTabs(groups))
			if !strings.Contains(bar, "• "+groups[active].title) || ansi.StringWidth(bar) > current.contentWidth() {
				t.Fatalf("the active group became hidden or overflowed at width %d: %q", width, bar)
			}
		}
	}
}

// TestSessionResultHierarchyPreservesUnknown 保护成功简化与未知结果可见性，避免美化掩盖实际状态。
// 标签只使用可信字段元数据着色；正文中的控制符不能伪造字段或改变底部路径。
func TestSessionResultHierarchyPreservesUnknown(t *testing.T) {
	for _, status := range []app.Status{app.Success, app.Unknown, app.Partial} {
		session := &Session{clean: func(value string) string {
			return value
		}}
		render := output.Renderer{Out: session, Diagnostic: session}
		result := app.NewResult(app.Context{}, []app.Item{{
			Target: "0.6.3",
			Status: status,
			Data: map[string]any{
				"id":   109695,
				"name": "V0.6.3",
			},
		}}, nil)
		if err := render.Text(result); err != nil {
			t.Fatal(err)
		}
		body := session.Drain()
		if status == app.Success && strings.Contains(body, "success") {
			t.Fatal("a successful session result retained a redundant status label")
		}
		if status != app.Success && !strings.Contains(body, string(status)) {
			t.Fatal("simplifying a result hid its non-success state")
		}
		if len(session.pendingFields) != 2 || !strings.Contains(body, "\n\nID") {
			t.Fatal("resource heading or trusted field boundaries were lost")
		}
		current := sessionModel{
			session: session,
			request: screenRequest{
				kind:     "display",
				title:    "release view",
				body:     body,
				sections: session.pending,
				fields:   session.pendingFields,
			},
			width:  80,
			height: 24,
		}
		current.updateLocation(current.request)
		if !strings.Contains(current.View(), "Home › Releases › View") {
			t.Fatal("the current page hierarchy is missing from the result footer")
		}
	}
}

// TestTruncatedPreviewCannotBeSubmitted 保护大计划预览被截断时不能进入确认输入。
// 超过 2 MiB 的 Drain 元数据随正文匹配并被一次性消费，提示错误早于 UI 提交。
func TestTruncatedPreviewCannotBeSubmitted(t *testing.T) {
	session := &Session{
		clean: func(value string) string {
			return value
		},
	}
	plan := bytes.Repeat([]byte("x"), (2<<20)+1)
	if _, err := session.Write(plan); err != nil {
		t.Fatal(err)
	}
	preview := session.Drain()
	if !session.pendingTruncated || !strings.Contains(preview, "Display limit reached") {
		t.Fatal("the display cap was not retained with its omission notice")
	}
	_, err := session.request(context.Background(), screenRequest{
		kind:  "prompt",
		title: "Apply this exact plan [y/N]",
		body:  preview + "\nApply this exact plan [y/N]",
	})
	if app.Failure(err).Code != app.IOError {
		t.Fatalf("truncated preview reached confirmation: %v", err)
	}
	if session.pendingTruncated || session.pendingBody != "" {
		t.Fatal("consumed preview metadata remained available for a later prompt")
	}
}
