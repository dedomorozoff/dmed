package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func mkProj(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, root, "b.txt", "bee\n")
	writeTemp(t, filepath.Join(root, "sub"), "a.txt", "ay\n")
	return root
}

func TestNewWithDirOpensProject(t *testing.T) {
	root := mkProj(t)
	outer := t.TempDir()
	chdir(t, outer)

	m := New(root)
	if m.root != root {
		t.Fatalf("root = %q, want %q", m.root, root)
	}
	if !m.treeVisible || len(m.treeRows) != 2 {
		t.Fatalf("visible=%v rows=%v", m.treeVisible, m.treeRows)
	}
	if !m.treeRows[0].isDir || m.treeRows[0].name != "sub" {
		t.Fatalf("dirs must come first, rows=%v", m.treeRows)
	}
	if m.tabs[0].path != "" {
		t.Fatalf("opening dir must leave scratch tab, got %q", m.tabs[0].path)
	}
}

func TestTreeNavigateOpenAndFold(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	m = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeTab().path != filepath.Join(root, "b.txt") {
		t.Fatalf("enter on file must open it, got %q", m.activeTab().path)
	}
	if m.activeTab().buf.Text() != "bee\n" {
		t.Fatalf("content = %q", m.activeTab().buf.Text())
	}

	m = press(m, tea.KeyMsg{Type: tea.KeyUp})
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	found := false
	for _, r := range m.treeRows {
		if r.rel == "sub/a.txt" && r.depth == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("enter on dir must expand children, rows=%v", m.treeRows)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	for _, r := range m.treeRows {
		if r.isDir && r.rel == "sub" && m.expanded[r.rel] {
			t.Fatal("left on expanded dir must collapse it")
		}
	}
}

func TestPathsResolveAgainstRoot(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.openPath("newfile.txt")
	want := filepath.Join(root, "newfile.txt")
	if m.activeTab().path != want {
		t.Fatalf("openPath resolved to %q, want %q", m.activeTab().path, want)
	}
	if got := m.activeTab().name(m.baseDir()); got != "newfile.txt" {
		t.Fatalf("display name = %q", got)
	}
}

func TestFinderUsesProjectRoot(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	joined := strings.Join(m.finderHits, ",")
	if !strings.Contains(joined, "b.txt") || !strings.Contains(joined, "sub/a.txt") {
		t.Fatalf("finder must walk project root, hits=%v", m.finderHits)
	}
}

func TestSidebarFocusLifecycle(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	if m.treeFocus {
		t.Fatal("tree starts unfocused")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !m.treeFocus {
		t.Fatal("ctrl+b must focus visible tree")
	}
	typeStr(m, "zzz")
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatal("typing with focused tree must not edit buffer")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.treeFocus || !m.treeVisible {
		t.Fatal("esc must return focus to editor keeping panel")
	}
	typeStr(m, "hi")
	if m.tabs[0].buf.Text() != "hi\n" {
		t.Fatal("typing after esc must edit buffer")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.treeVisible {
		t.Fatal("ctrl+b twice more must hide panel")
	}
	v := m.View()
	if strings.Contains(v, "▸ sub") {
		t.Fatal("hidden sidebar must not render")
	}
}

func TestViewRendersTreePanel(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	v := m.View()
	if !strings.Contains(v, "▸ sub") || !strings.Contains(v, "b.txt") {
		t.Fatalf("sidebar must render entries, got:\n%s", v)
	}
}
