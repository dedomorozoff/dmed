package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpToggle(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if m.helpOpen {
		t.Fatal("help must start closed")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.helpOpen {
		t.Fatal("f1 must open help")
	}
	v := m.View()
	for _, want := range []string{"dmed — keys", "Ctrl+S", "Ctrl+O / F3", "Ctrl+W"} {
		if !strings.Contains(v, want) {
			t.Fatalf("help view missing %q", want)
		}
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.helpOpen {
		t.Fatal("esc must close help")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyF1})
	m = press(m, tea.KeyMsg{Type: tea.KeyF1})
	if m.helpOpen {
		t.Fatal("f1 again must close help")
	}
}

func TestHelpSwallowsTyping(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyF1})
	typeStr(m, "hello world")
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatalf("typing while help open must not edit buffer, got %q", m.tabs[0].buf.Text())
	}
}

func TestStatusBarShowsHint(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if v := m.View(); !strings.Contains(v, "F1 help") {
		t.Fatal("status bar must hint F1 help by default")
	}
}
