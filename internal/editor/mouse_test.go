package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestTabBarClickSwitchesTab(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	b := writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(a)
	m.width, m.height = 80, 24
	m.openPath(b)
	m.setActiveTab(0)

	w0 := lipgloss.Width(m.tabLabel(0))
	_ = m.handleMouseClick(tea.MouseClickMsg{X: w0 + 1, Y: 0})
	if m.activeTabIndex() != 1 || m.activeTab().path != b {
		t.Fatalf("click on tab 2: active=%d path=%q, want tab for %q", m.activeTabIndex(), m.activeTab().path, b)
	}

	// Clicking inside the first label goes back to tab 0.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 1, Y: 0})
	if m.activeTabIndex() != 0 {
		t.Fatalf("click on tab 1: active=%d, want 0", m.activeTabIndex())
	}
}

func TestFinderItemClickOpensFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	f2 := writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(f1)
	m.width, m.height = 80, 24
	m.finderOpen = true
	m.finderHits = []string{"a.txt", "b.txt"}
	m.finderSel = 0

	// Finder overlay: items occupy finderExtraRows rows after the status bar.
	start := m.viewHeight() + 2
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 10, Y: start + 1})
	if m.finderOpen {
		t.Fatal("finder must close after clicking an item")
	}
	if m.activeTab().path != f2 {
		t.Fatalf("active path = %q, want %q", m.activeTab().path, f2)
	}
}

func TestChatRailClickFocusesChat(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.chatOpen = true
	m.chatFocus = false

	_ = m.handleMouseClick(tea.MouseClickMsg{X: 60, Y: 5})
	if !m.chatFocus {
		t.Fatal("click on the chat rail must focus the chat panel")
	}

	// Clicking the buffer area must not grab focus to the chat.
	m.chatFocus = false
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5})
	if m.chatFocus {
		t.Fatal("buffer click must not focus the chat rail")
	}
}

func TestWheelOverChatScrolls(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.chatOpen = true
	for i := 0; i < 25; i++ {
		m.chatRows = append(m.chatRows, chatRow{kind: "ai", text: "row"})
	}

	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 60, Y: 5})
	if m.chatScroll <= 0 {
		t.Fatalf("wheel down over chat must scroll back, got %d", m.chatScroll)
	}
	prev := m.chatScroll
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 60, Y: 5})
	if m.chatScroll >= prev {
		t.Fatalf("wheel up over chat must scroll toward bottom, got %d (was %d)", m.chatScroll, prev)
	}
}

func TestWheelOverTerminalScrolls(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.termOpen = true
	for i := 0; i < 40; i++ {
		m.termLines = append(m.termLines, "line")
	}

	start := m.termStartRow()
	if start >= m.height {
		t.Fatalf("terminal overlay does not fit: start=%d height=%d", start, m.height)
	}
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 40, Y: start + 1})
	if m.termScroll <= 0 {
		t.Fatalf("wheel down over terminal must scroll back, got %d", m.termScroll)
	}
}

func TestTreeRailClickSelects(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	if len(m.treeRows) < 2 {
		t.Fatalf("tree rows = %v, want at least 2", m.treeRows)
	}

	// Click the second rail row: selects it and focuses the tree.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 2})
	if m.treeSel != 1 {
		t.Fatalf("treeSel = %d, want 1", m.treeSel)
	}
	if !m.treeFocus {
		t.Fatal("rail click must focus the tree")
	}

	// Click in the buffer area releases tree focus.
	m.treeFocus = true
	m.treeSel = 1
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 60, Y: 5})
	if m.treeFocus {
		t.Fatal("buffer click must release tree focus")
	}
}

func TestGitRailClickSelects(t *testing.T) {
	dir, f := initTestGitRepo(t)
	other := filepath.Join(dir, "extra.go")
	if err := os.WriteFile(other, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := filepath.Join(dir, "code.go")
	long := "package main\n\nfunc main() {\n" + strings.Repeat("\t_ = i\n", 200) + "}\n"
	if err := os.WriteFile(code, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24
	m.openGitPanel()
	if len(m.gitFiles) < 2 {
		t.Skipf("git status must list two modified files, got %v", m.gitFiles)
	}
	m.gitSel = 0

	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 2})
	if m.gitSel != 1 {
		t.Fatalf("click on second status row: gitSel = %d, want 1", m.gitSel)
	}
	if !m.gitFocus {
		t.Fatal("clicking the git rail must focus the git panel")
	}
	if m.gitDiffFocused {
		t.Fatal("clicking the rail must unfocus the diff")
	}

	// Clicking the AI chat rail while git is open transfers focus to the chat
	// and releases git focus.
	m.chatOpen = true
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 60, Y: 5})
	if !m.chatFocus {
		t.Fatal("chat rail click must focus the chat panel")
	}
	if m.gitFocus {
		t.Fatal("chat rail click must release git focus")
	}

	// Re-click the rail to refocus the git panel.
	m.chatOpen = false
	m.chatFocus = false
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 1})
	if !m.gitFocus {
		t.Fatal("rail click must refocus the git panel")
	}

	// Select the long file (code.go) whose diff is tall enough to scroll.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 1})
	if m.gitSel != 0 {
		t.Fatalf("click on first status row: gitSel = %d, want 0", m.gitSel)
	}
	if m.gitDiffFocused {
		t.Fatal("clicking the rail must unfocus the diff")
	}

	// Click on the inline diff area focuses it.
	if len(m.diffRows) == 0 {
		t.Skip("no inline diff preview")
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 60, Y: 5})
	if !m.gitDiffFocused {
		t.Fatal("click on the diff area must focus the diff")
	}
	before := m.diffOffsetY
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 60, Y: 5})
	if m.diffOffsetY != before+1 {
		t.Fatalf("wheel over diff area: diffOffsetY = %d, want %d", m.diffOffsetY, before+1)
	}
}

func TestCompletionItemClickAccepts(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "c.txt", "hello world\nhelmet head\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Text: "h"})
	m = press(m, tea.KeyPressMsg{Text: "e"})
	if !m.complOpen || len(m.complItems) == 0 {
		t.Fatalf("popup must be open with candidates, got %v", m.complItems)
	}

	// Completion popup: title row, then candidate rows.
	start := m.complStartRow()
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 10, Y: start + 1})
	if m.complOpen {
		t.Fatal("clicking a candidate must accept and close the popup")
	}
	if strings.Contains(m.cur().buf.Text(), "hehello") {
		t.Fatalf("candidate was not inserted, buffer=%q", m.cur().buf.Text())
	}
}

func TestBufferClickSwitchesSplitPane(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(a)
	m.width, m.height = 100, 24
	m.openPath(filepath.Join(dir, "b.txt"))
	m.setActiveTab(0)
	m.splitVert()
	if m.layout != splitVert || m.activePane != 1 {
		t.Fatalf("split setup: layout=%d activePane=%d", m.layout, m.activePane)
	}

	// Click into the right pane (already active).
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 60, Y: 10})
	if m.activePane != 1 {
		t.Fatalf("right-pane click: activePane = %d, want 1", m.activePane)
	}

	// Click into the left pane.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 5, Y: 10})
	if m.activePane != 0 {
		t.Fatalf("left-pane click: activePane = %d, want 0", m.activePane)
	}
}
