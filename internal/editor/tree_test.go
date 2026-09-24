package editor

import (
	"fmt"
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
	if strings.Contains(v.Content, "▸ sub") {
		t.Fatal("hidden sidebar must not render")
	}
}

func TestViewRendersTreePanel(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())

	m := New(root)
	m.width, m.height = 100, 24
	v := m.View()
	if !strings.Contains(v.Content, "▸ sub") || !strings.Contains(v.Content, "b.txt") {
		t.Fatalf("sidebar must render entries, got:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "n:new file") {
		t.Fatalf("sidebar must render the key hint bar, got:\n%s", v.Content)
	}
}

func TestTreeScrollKeepsSelectionVisible(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 50; i++ {
		writeTemp(t, root, fmt.Sprintf("f%02d.txt", i), "x\n")
	}

	m := New(root)
	m.width, m.height = 100, 12
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	er := m.treeEntryRows(m.viewHeight())

	// Walk to the very bottom of the list.
	for i := 0; i < len(m.treeRows); i++ {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}

	if m.treeSel != len(m.treeRows)-1 {
		t.Fatalf("selection must land on the last entry, sel=%d n=%d", m.treeSel, len(m.treeRows))
	}
	if m.treeSel < m.treeOffset || m.treeSel >= m.treeOffset+er {
		t.Fatalf("selection %d outside visible window [%d,%d)", m.treeSel, m.treeOffset, m.treeOffset+er)
	}
	if m.treeOffset == 0 {
		t.Fatal("offset must scroll down past the top")
	}
	// The very last entry must be the final visible row.
	if m.treeOffset != len(m.treeRows)-er {
		t.Fatalf("offset=%d want %d (last row fills the panel)", m.treeOffset, len(m.treeRows)-er)
	}
	if !strings.Contains(m.View().Content, m.treeRows[m.treeSel].name) {
		t.Fatal("selected file must be visible in the rendered panel")
	}
}

func TestTreeNewFilePromptPrefillsTargetDir(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.width, m.height = 100, 24

	// Focus the tree and select sub dir.
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp}) // sub
	m = press(m, tea.KeyPressMsg{Code: 'n'})
	if !m.promptOpen || !m.promptNewFile {
		t.Fatal("n must open the new-file prompt")
	}
	if got := string(m.promptIn); got != "sub/" {
		t.Fatalf("new-file prompt must be prefilled with target dir, got %q", got)
	}
	m = typeStr(m, "c.txt")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	tab := m.activeTab()
	if tab.path != filepath.Join(root, "sub", "c.txt") {
		t.Fatalf("new file must open at selected dir, got %q", tab.path)
	}
}

func TestTreeNewFolderPromptCreatesInDir(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.width, m.height = 100, 24

	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp}) // sub
	m = press(m, tea.KeyPressMsg{Code: 'N'})
	if !m.promptOpen || !m.promptNewFolder {
		t.Fatal("N must open the new-folder prompt")
	}
	m = typeStr(m, "inner")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if st, err := os.Stat(filepath.Join(root, "sub", "inner")); err != nil || !st.IsDir() {
		t.Fatalf("new folder must be created inside selected dir: %v", err)
	}
}

func TestTreeRenameMovesTabAndDisk(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.openPath(filepath.Join(root, "b.txt"))
	m.width, m.height = 100, 24

	m.renameTreeEntry("b.txt", "c.txt")
	if _, err := os.Stat(filepath.Join(root, "c.txt")); err != nil {
		t.Fatalf("renamed target missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("old path still exists: %v", err)
	}
	if m.activeTab().path != filepath.Join(root, "c.txt") {
		t.Fatalf("open tab must follow the rename, got %q", m.activeTab().path)
	}
	if got := m.activeTab().buf.Text(); got != "bee\n" {
		t.Fatalf("renamed tab must keep content, got %q", got)
	}
}

func TestTreeRenameRejectsExistingName(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.width, m.height = 100, 24

	m.renameTreeEntry("b.txt", "sub/a.txt")
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("rename onto an existing file must leave the source: %v", err)
	}
	if !strings.Contains(m.msg, "exist") {
		t.Fatalf("expected existence error message, got %q", m.msg)
	}
}

func TestTreeDuplicateCopiesFile(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.width, m.height = 100, 24

	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // select b.txt (0-index: after expand sorting)
	m = press(m, tea.KeyPressMsg{Code: 'd'})
	if _, err := os.Stat(filepath.Join(root, "b_copy.txt")); err != nil {
		t.Fatalf("duplicate must create b_copy.txt: %v", err)
	}
	if got := m.msg; !strings.Contains(got, "b_copy.txt") {
		t.Fatalf("duplicate message = %q", got)
	}
}

func TestTreeDeleteConfirmRemovesFileAndTab(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.openPath(filepath.Join(root, "b.txt"))
	m.width, m.height = 100, 24

	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // select b.txt
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDelete})
	if m.treeConfirm != "delete" {
		t.Fatalf("Del must arm delete confirmation, got %q", m.treeConfirm)
	}
	// 'y' confirms (also works with Cyrillic «н»).
	m = press(m, tea.KeyPressMsg{Code: 'y'})
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("file must be deleted after confirm: %v", err)
	}
	if m.treeConfirm != "" {
		t.Fatal("confirmation must clear after action")
	}
	for _, tb := range m.tabs {
		if tb.path == filepath.Join(root, "b.txt") {
			t.Fatal("tab for deleted file must be dropped")
		}
	}
}

func TestTreeTrashConfirmCancelKeepsFile(t *testing.T) {
	root := mkProj(t)
	chdir(t, t.TempDir())
	m := New(root)
	m.width, m.height = 100, 24

	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // select b.txt
	m = press(m, tea.KeyPressMsg{Code: 't'})
	if m.treeConfirm != "trash" {
		t.Fatalf("t must arm trash confirmation, got %q", m.treeConfirm)
	}
	if !strings.Contains(m.View().Content, "trash") {
		t.Fatal("confirm line must mention trash")
	}
	// 'n' cancels (also works with Cyrillic «т»).
	m = press(m, tea.KeyPressMsg{Code: 'n'})
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("cancelling must keep the file: %v", err)
	}
	if m.treeConfirm != "" {
		t.Fatal("confirmation must clear on cancel")
	}
}

func TestTrashMovesFileOffDisk(t *testing.T) {
	dir := t.TempDir()
	file := writeTemp(t, dir, "trashme.txt", "bye\n")

	if err := moveToTrash(file); err != nil {
		t.Fatalf("moveToTrash: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("file must no longer exist after moveToTrash: %v", err)
	}
}
