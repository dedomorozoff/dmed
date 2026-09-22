package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestOpenFolderCommandInPalette(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	cmds := m.getPaletteCommands()
	var found bool
	for _, c := range cmds {
		if c.id == "open_folder" {
			found = true
			if c.action == nil {
				t.Fatal("open_folder command has no action")
			}
		}
	}
	if !found {
		t.Fatal("palette must include the open_folder command")
	}

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeStr(m, "folder")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "open_folder" {
		t.Fatalf("filtering 'folder' must surface open_folder, got: %+v", hits)
	}
}

func TestSwitchRoot(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	m := New(dirA)
	m.width, m.height = 80, 24
	if m.root == "" {
		t.Fatal("New(dir) must set the project root")
	}

	m.switchRoot(dirB)
	want, _ := filepath.Abs(dirB)
	if m.root != want {
		t.Fatalf("root = %q, want %q", m.root, want)
	}
	if !m.treeVisible {
		t.Fatal("switchRoot must enable the project tree")
	}
	if !m.treeFocus {
		t.Fatal("switchRoot must move the caret to the project tree")
	}
	if !strings.Contains(m.msg, filepath.Base(dirB)) {
		t.Fatalf("status must announce the new project, got %q", m.msg)
	}
}

func TestReadFolderEntriesDirsFirst(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "zzdir"))
	mustMkdir(t, filepath.Join(dir, "adirs"))
	mustWrite(t, filepath.Join(dir, "bbb.txt"), "b")
	mustWrite(t, filepath.Join(dir, "aaa.txt"), "a")

	entries := readFolderEntries(dir)
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4: %+v", len(entries), entries)
	}
	if !entries[0].dir || entries[0].name != "adirs" {
		t.Fatalf("dirs must sort first, got %+v", entries[0])
	}
	if !entries[1].dir || entries[1].name != "zzdir" {
		t.Fatalf("dirs must sort by name, got %+v", entries[1])
	}
	if entries[2].dir || entries[2].name != "aaa.txt" {
		t.Fatalf("files after dirs, got %+v", entries[2])
	}
}

func TestFolderBrowserNavigateAndUse(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	mustMkdir(t, sub)
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")

	m := New(dir)
	m.width, m.height = 80, 24
	m.startFolderBrowser()
	if !m.folderOpen {
		t.Fatal("startFolderBrowser must open the picker")
	}
	if m.folderPath != filepath.Clean(dir) {
		t.Fatalf("browser path = %q, want %q", m.folderPath, dir)
	}
	// One dir + one file.
	if len(m.folderEntries) != 2 || !m.folderEntries[0].dir {
		t.Fatalf("entries wrong: %+v", m.folderEntries)
	}

	// Start on "", the parent row; move down onto the folder and enter it.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.folderSel != 1 {
		t.Fatalf("sel after down = %d, want 1", m.folderSel)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.folderOpen {
		t.Fatal("picker must stay open after entering a dir")
	}
	if m.folderPath != filepath.Clean(sub) {
		t.Fatalf("path after enter = %q, want %q", m.folderPath, sub)
	}

	// Backspace walks up again.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.folderPath != filepath.Clean(dir) {
		t.Fatalf("path after backspace = %q, want %q", m.folderPath, dir)
	}

	// 'o' uses the current folder: root switches and the picker closes.
	m = press(m, tea.KeyPressMsg{Text: "o"})
	if m.folderOpen {
		t.Fatal("picker must close after use")
	}
	want, _ := filepath.Abs(dir)
	if m.root != want {
		t.Fatalf("root after use = %q, want %q", m.root, want)
	}
}

func TestFolderBrowserParentRowEnter(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	mustMkdir(t, sub)

	m := New(dir)
	m.width, m.height = 80, 24
	m.startFolderBrowser()
	m.folderSel = 1 // select the sub directory
	m.folderEnter() // walk into sub
	if m.folderPath != filepath.Clean(sub) {
		t.Fatalf("enter must descend, path = %q", m.folderPath)
	}
	m.folderSel = 0
	m.folderEnter() // Enter on the parent row -> walk back up to dir
	if m.folderPath != filepath.Clean(dir) {
		t.Fatalf("parent-row enter must go up, path = %q", m.folderPath)
	}
}

func TestFolderBrowserPaint(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "sub"))

	m := New(dir)
	m.width, m.height = 80, 24
	m.startFolderBrowser()

	rows := m.folderPanel()
	if len(rows) < 4 {
		t.Fatalf("panel must render header+up+entry+hint, got %d rows", len(rows))
	}
	if !strings.Contains(rows[0], m.folderPath) {
		t.Fatalf("header must show the path, got %q", rows[0])
	}
	if !strings.Contains(rows[2], "sub") {
		t.Fatalf("listing must include the sub dir, got %q", rows[2])
	}
	if v := m.View(); !strings.Contains(v.Content, "Open folder") {
		t.Fatalf("view must render the folder panel header (len=%d)", len(v.Content))
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}