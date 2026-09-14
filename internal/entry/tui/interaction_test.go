package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPanelNavigationSupportsReverseTab(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning
	m.focusPane = focusEvents

	next, _ := m.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyShiftTab})
	got := next.(Model)
	if got.focusPane != focusState {
		t.Fatalf("Shift+Tab should move to previous panel: got %v", got.focusPane)
	}

	next, _ = got.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	got = next.(Model)
	if got.focusPane != focusEvents {
		t.Fatalf("Tab should move back to first panel: got %v", got.focusPane)
	}
}

func TestHistoryUsesExplicitControlKeys(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning
	m.pushInputHistory("第一条")
	m.pushInputHistory("第二条")
	m.textarea.SetValue("")

	next, _ := m.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlP})
	got := next.(Model)
	if got.textarea.Value() != "第二条" {
		t.Fatalf("Ctrl+P should recall the latest history: got %q", got.textarea.Value())
	}

	next, _ = got.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlN})
	got = next.(Model)
	if got.textarea.Value() != "" {
		t.Fatalf("Ctrl+N should return to the draft: got %q", got.textarea.Value())
	}
}

func TestHistoryDoesNotReplaceMultilineDraft(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning
	m.pushInputHistory("历史内容")
	m.textarea.SetValue("当前第一行\n当前第二行")

	next, _ := m.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlP})
	got := next.(Model)
	if got.textarea.Value() != "当前第一行\n当前第二行" {
		t.Fatalf("Ctrl+P must not replace a multiline draft: got %q", got.textarea.Value())
	}
}
