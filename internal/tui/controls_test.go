package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestInputEditingAndSecretPresentation 保护真实表单编辑语义及密钥不可见边界。
// 带初值的普通输入可以插入编辑，Secret无论长度或光标位置都只显示同一隐藏提示。
func TestInputEditingAndSecretPresentation(t *testing.T) {
	current := sessionModel{session: &Session{color: "never"}, width: 80, height: 24, focus: map[string]string{}}
	request := screenRequest{
		kind: "prompt", title: "Connect account",
		inputOptions: &InputOptions{Label: "Instance", Value: "abc"},
		reply:        make(chan screenReply, 1),
	}
	next, _ := current.Update(request)
	current = next.(sessionModel).key(tea.KeyMsg{Type: tea.KeyLeft})
	current = current.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if !strings.Contains(current.View(), "abX") || !strings.Contains(current.View(), "╭") {
		t.Fatal("the editable input box lost its current value")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEnter})
	if reply := <-request.reply; reply.text != "abXc" {
		t.Fatalf("input editing changed the submitted value: %q", reply.text)
	}
	current.width, current.height = 28, 12
	request.inputOptions.Step = "Step 1 of 4 · Instance"
	request.reply = make(chan screenReply, 1)
	next, _ = current.Update(request)
	current = next.(sessionModel)
	view := ansi.Strip(current.View())
	if !strings.Contains(view, "abc") || !strings.Contains(view, "Enter Submit") ||
		!strings.Contains(view, "Esc Back") {
		t.Fatal("a short window hid the editable value or its submit/cancel actions")
	}
	request.secret = true
	request.inputOptions = &InputOptions{Label: "Access token", Secret: true}
	request.reply = make(chan screenReply, 1)
	next, _ = current.Update(request)
	current = next.(sessionModel)
	current = current.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("synthetic-private-value")})
	view = ansi.Strip(current.View())
	if strings.Contains(view, "synthetic") || !strings.Contains(view, "[input hidden]") {
		t.Fatal("secret contents or length reached the screen")
	}
	current = current.key(tea.KeyMsg{Type: tea.KeyEsc})
	if reply := <-request.reply; reply.err == nil || reply.text != "" {
		t.Fatal("cancelled secret input returned a value")
	}
}
