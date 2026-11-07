package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSelectorRetainsExplicitSelectionAcrossPages 保护跨页、搜索与已选回查，不自动勾选首项。
// 直接驱动模型验证稳定 ID 契约，实际终端恢复另由 PTY 脚本验证。
func TestSelectorRetainsExplicitSelectionAcrossPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := model{
		ctx:        ctx,
		requestCtx: ctx,
		cancel: func() {
		},
		provider: func(context.Context, int, string) (Page, error) {
			return Page{}, nil
		},
		selected:  map[string]Choice{},
		page:      1,
		requestID: 1,
		width:     80,
		height:    24,
		multi:     true,
	}
	updated, _ := current.Update(loaded{
		id: 1,
		page: Page{
			Choices: []Choice{{
				ID:    "1",
				Label: "one",
			}},
			HasMore: true,
		},
	})
	current = updated.(model)
	updated, command := current.Update(tea.KeyMsg{Type: tea.KeyEnter})
	current = updated.(model)
	if current.finished || command != nil || len(current.selected) != 0 {
		t.Fatal("Enter silently selected the first candidate")
	}
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{' '},
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'n'},
	})
	current = updated.(model)
	updated, _ = current.Update(loaded{
		id: current.requestID,
		page: Page{Choices: []Choice{{
			ID:    "2",
			Label: "two",
		}}},
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{' '},
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'/'},
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("changed"),
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{Type: tea.KeySpace})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("query"),
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{Type: tea.KeyEnter})
	current = updated.(model)
	if len(current.selected) != 2 || current.page != 1 || current.query != "changed query" {
		t.Fatal("search or pagination lost prior explicit selections")
	}
	updated, _ = current.Update(loaded{
		id:   current.requestID - 1,
		page: Page{Choices: []Choice{{ID: "stale"}}},
	})
	if !updated.(model).loading {
		t.Fatal("stale page replaced the active request")
	}
	updated, _ = current.Update(loaded{
		id:   current.requestID,
		page: Page{Choices: []Choice{}},
	})
	current = updated.(model)
	updated, _ = current.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'s'},
	})
	current = updated.(model)
	if len(current.visible()) != 2 || strings.Contains(safe("unsafe\x1b]title\x07"), "\x1b") {
		t.Fatal("selected review or terminal control filtering failed")
	}
}
