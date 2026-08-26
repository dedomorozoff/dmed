package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}) // focus tree
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})            // select b.txt
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})           // open file, defocus tree
	if m.activeTab().path != filepath.Join(root, "b.txt") {
		t.Fatalf("enter on file must open it, got %q", m.activeTab().path)
	}
	if m.activeTab().buf.Text() != "bee\n" {
		t.Fatalf("content = %q", m.activeTab().buf.Text())
	}
	if m.treeFocus {
		t.Fatal("enter on file must return focus to editor")
	}
	if !m.treeVisible {
		t.Fatal("sidebar must stay visible after opening file")
	}

	// Re-focus tree to navigate to dir
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}) // re-focus tree
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})             // select sub dir
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})          // expand sub (stays in tree)
	found := false
	for _, r := range m.treeRows {
		if r.rel == "sub/a.txt" && r.depth == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("enter on dir must expand children, rows=%v", m.treeRows)
	}
	if !m.treeFocus {
		t.Fatal("enter on dir must keep focus in tree")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyLeft})
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
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
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
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.treeFocus {
		t.Fatal("ctrl+b must focus visible tree")
	}
	typeStr(m, "zzz")
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatal("typing with focused tree must not edit buffer")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.treeFocus || !m.treeVisible {
		t.Fatal("esc must return focus to editor keeping panel")
	}
	typeStr(m, "hi")
	if m.tabs[0].buf.Text() != "hi\n" {
		t.Fatal("typing after esc must edit buffer")
	}
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.treeVisible {
		t.Fatal("ctrl+b twice more must hide panel")
	}
	v := m.View()
	if strings.Contains(v.Content, "+ sub") {
		t.Fatal("hidden sidebar must not render")
	}
}

func TestViewRendersTreePanel(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	v := m.View()
	if !strings.Contains(v.Content, "+ sub") || !strings.Contains(v.Content, "b.txt") {
		t.Fatalf("sidebar must render entries, got:\n%s", v.Content)
	}
}
