package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m Model, k tea.KeyMsg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func typeStr(m Model, s string) Model {
	for _, r := range s {
		m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTabsOpenSwitchClose(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	f2 := writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(f1, f2)
	if len(m.tabs) != 2 || m.active != 1 {
		t.Fatalf("want 2 tabs active=1, got %d tabs active=%d", len(m.tabs), m.active)
	}
	if got := m.activeTab().buf.Text(); got != "bravo\n" {
		t.Fatalf("active content = %q", got)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	if m.active != 0 || m.activeTab().buf.Text() != "alpha\n" {
		t.Fatalf("alt+left: active=%d content=%q", m.active, m.activeTab().buf.Text())
	}
	typeStr(m, "X")
	if !m.tabs[0].buf.Dirty() || m.tabs[1].buf.Dirty() {
		t.Fatal("dirty must be per-tab")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	if m.active != 1 {
		t.Fatalf("alt+right: active=%d", m.active)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m.tabs) != 1 || m.active != 0 || m.activeTab().path != f1 {
		t.Fatalf("ctrl+w: %d tabs active=%d path=%q", len(m.tabs), m.active, m.activeTab().path)
	}
}

func TestPromptOpensAndCancels(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "one.txt", "one\n")
	target := writeTemp(t, dir, "two.txt", "two lines\n")

	m := New(f1)
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if !m.promptOpen {
		t.Fatal("ctrl+t must open prompt")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.promptOpen || len(m.tabs) != 1 {
		t.Fatal("esc must cancel prompt")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = typeStr(m, target)
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.promptOpen || len(m.tabs) != 2 || m.active != 1 {
		t.Fatalf("enter: prompt=%v tabs=%d active=%d", m.promptOpen, len(m.tabs), m.active)
	}
	if got := m.activeTab().buf.Text(); got != "two lines\n" {
		t.Fatalf("opened content = %q", got)
	}
	if m.activeTab().path != target {
		t.Fatalf("opened path = %q", m.activeTab().path)
	}
}

func TestCloseLastTabReturnsQuit(t *testing.T) {
	m := New()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if next.(Model).lenTabs() != 0 {
		t.Fatal("tab must be removed")
	}
	if cmd == nil {
		t.Fatal("closing last tab must quit")
	}
}

func (m Model) lenTabs() int { return len(m.tabs) }

func TestViewShowsTabNames(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "aa.txt", "x\n")
	f2 := writeTemp(t, dir, "bb.txt", "y\n")

	m := New(f1, f2)
	m.width, m.height = 80, 24
	v := m.View()
	if !strings.Contains(v, "aa.txt") || !strings.Contains(v, "bb.txt") {
		t.Fatal("view must show both tab names")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if !strings.Contains(m.View(), "open file:") {
		t.Fatal("prompt line must be visible while open")
	}
}

func TestUntitledCannotSave(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(m.msg, "no file name") {
		t.Fatalf("msg = %q", m.msg)
	}
}

func TestSaveWritesActiveTab(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "s.txt", "")
	f2 := writeTemp(t, dir, "t.txt", "")

	m := New(f1, f2)
	typeStr(m, "hello")
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	data, err := os.ReadFile(f2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("saved = %q", data)
	}
	if m.tabs[1].buf.Dirty() {
		t.Fatal("must be clean after save")
	}
}
