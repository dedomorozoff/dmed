package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFuzzySearchRanking(t *testing.T) {
	files := []string{
		"main.go",
		"internal/editor/editor.go",
		"internal/editor/finder.go",
		"go.sum",
		"README.md",
	}
	hits := searchFiles(files, "find")
	if len(hits) == 0 || !strings.Contains(hits[0], "finder.go") {
		t.Fatalf("top hit for 'find' = %v", hits)
	}
	hits = searchFiles(files, "edgo")
	if len(hits) == 0 || !strings.Contains(hits[0], "editor.go") {
		t.Fatalf("top hit for 'edgo' = %v", hits)
	}
	if hits := searchFiles(files, "zzzz"); len(hits) != 0 {
		t.Fatalf("no-match query must return nothing, got %v", hits)
	}
	if hits := searchFiles(files, ""); len(hits) != len(files) {
		t.Fatalf("empty query must match all, got %d", len(hits))
	}
}

func TestFinderOpensAndRefocuses(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(f1)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.finderOpen || len(m.finderHits) != 2 {
		t.Fatalf("finder: open=%v hits=%v", m.finderOpen, m.finderHits)
	}
	if m.finderHits[0] != "a.txt" {
		t.Fatalf("empty-query order: %v", m.finderHits)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.finderSel != 1 {
		t.Fatalf("sel after down = %d", m.finderSel)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.finderOpen || len(m.tabs) != 2 || m.activeTab().path != filepath.Join(dir, "b.txt") {
		t.Fatalf("enter: open=%v tabs=%d active=%q", m.finderOpen, len(m.tabs), m.activeTab().path)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	for _, r := range ".tx" {
		m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.finderHits) != 2 {
		t.Fatalf("query '.tx' must match both files, got %v", m.finderHits)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.finderSel != 1 {
		t.Fatalf("up must wrap to last hit, sel=%d", m.finderSel)
	}
	m.finderSel = 0
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.tabs) != 2 {
		t.Fatalf("enter on open file must not duplicate tab, tabs=%d", len(m.tabs))
	}
	if m.activeTabIndex() != 0 {
		t.Fatalf("must refocus existing tab, activeTabIndex=%d", m.activeTabIndex())
	}
}

func TestFinderCancelPaths(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	f1 := writeTemp(t, dir, "only.txt", "x\n")

	m := New(f1)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.finderOpen || len(m.tabs) != 1 {
		t.Fatal("esc must close finder")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	for _, r := range "zzz" {
		m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.finderHits) != 0 {
		t.Fatalf("expected no hits, got %v", m.finderHits)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if string(m.finderQ) != "zz" {
		t.Fatalf("backspace: q=%q", string(m.finderQ))
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.finderOpen || len(m.tabs) != 1 {
		t.Fatal("enter without hits must just close finder")
	}
}

func TestCtrlChordFromMangledStack(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\x0f'}})
	if !m.finderOpen {
		t.Fatal("lone 0x0f rune must be normalized to ctrl+o")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.tabs[0].buf.Dirty() {
		t.Fatal("mangled chord must not insert into buffer")
	}
}

func TestViewShowsFinderPanel(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	f1 := writeTemp(t, dir, "visible.txt", "x\n")

	m := New(f1)
	m.width, m.height = 80, 24
	v := m.View()
	if strings.Contains(v, "find file:") {
		t.Fatal("finder panel must be hidden while closed")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	v = m.View()
	if !strings.Contains(v, "find file:") || !strings.Contains(v, "visible.txt") {
		t.Fatal("finder panel with candidates must be visible")
	}
}
