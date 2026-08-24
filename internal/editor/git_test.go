package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"dmed/internal/vcs"
)

func initTestGitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()

	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	f := filepath.Join(dir, "code.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Add("code.go"); err != nil {
		t.Fatal(err)
	}

	_, err = w.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "tester", Email: "tester@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, f
}

func TestEditorGitGutterAndHunkJump(t *testing.T) {
	_, f := initTestGitRepo(t)

	m := New(f)
	m.width, m.height = 80, 24

	// Status bar should show branch
	v := m.View()
	if !strings.Contains(v, "master") && !strings.Contains(v, "main") {
		t.Fatalf("view should show git branch in status bar:\n%s", v)
	}

	// Modify buffer (insert a new line)
	m = press(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = typeStr(m, "// added comment")

	// View should render gutter diff marker '+'
	v = m.View()
	if !strings.Contains(v, "+") {
		t.Fatalf("gutter should show '+' for added line:\n%s", v)
	}

	// Alt+] jump to hunk from line 0
	m.cur().buf.SetCursor(0, 0)
	m.jumpHunk(1)
	if m.cur().buf.CurLine() != 1 {
		t.Fatalf("expected cursor at line 1 after hunk jump, got %d", m.cur().buf.CurLine())
	}
}

func TestEditorExternalFileReload(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "watched.txt")
	if err := os.WriteFile(f, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	// External modification of clean buffer
	if err := os.WriteFile(f, []byte("externally modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trigger FileChangedMsg
	absF, _ := filepath.Abs(f)
	next, _ := m.Update(FileChangedMsg{Path: absF})
	m = next.(Model)

	if m.cur().buf.Text() != "externally modified\n" {
		t.Fatalf("clean buffer should auto-reload on external change, got %q", m.cur().buf.Text())
	}

	// Dirty buffer conflict
	m = typeStr(m, "local edit\n")
	if err := os.WriteFile(f, []byte("external edit 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, _ = m.Update(FileChangedMsg{Path: absF})
	m = next.(Model)

	if !m.conflictOpen {
		t.Fatal("dirty buffer must trigger conflict prompt")
	}

	// Resolve conflict by reload ('r')
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.conflictOpen {
		t.Fatal("conflict prompt should be closed after 'r'")
	}
	if m.cur().buf.Text() != "external edit 2\n" {
		t.Fatalf("buffer should reload after 'r', got %q", m.cur().buf.Text())
	}
}

func TestEditorGitCommitPanel(t *testing.T) {
	dir, f := initTestGitRepo(t)

	m := New(f)
	m.width, m.height = 80, 24

	// Modify file and save to create working tree changes
	m.cur().buf.Insert('!')
	m.saveActive()

	// Open Git panel (Ctrl+G): starts in status mode with the file list
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.gitOpen {
		t.Fatal("expected gitOpen true after Ctrl+G")
	}
	if m.gitMode != gitModeStatus {
		t.Fatalf("panel must open in status mode, got %d", m.gitMode)
	}
	v := m.View()
	if !strings.Contains(v, "code.go") {
		t.Fatalf("status mode should list changed files, got:\n%s", v)
	}

	// Stage all ('a'), then switch to commit mode ('c')
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.gitMode != gitModeCommit {
		t.Fatalf("expected commit mode after 'c', got %d", m.gitMode)
	}
	v = m.View()
	if !strings.Contains(v, "git commit:") {
		t.Fatalf("commit mode should show git commit line, got:\n%s", v)
	}

	// Type commit message and commit
	m = typeStr(m, "test commit message")
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.gitOpen {
		t.Fatal("gitOpen should close after commit")
	}
	if !strings.Contains(m.msg, "committed:") {
		t.Fatalf("status message should confirm commit, got: %s", m.msg)
	}

	// The commit must actually exist in the repository
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if c.Message != "test commit message\n" && c.Message != "test commit message" {
		t.Fatalf("HEAD commit message = %q", c.Message)
	}

	// Reopening the panel shows a clean state
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if len(m.gitFiles) != 0 {
		t.Fatalf("after commit the file list must be empty, got %d files", len(m.gitFiles))
	}
}

func TestEditorGitPanelLeftRail(t *testing.T) {
	_, f := initTestGitRepo(t)

	if err := os.WriteFile(f, []byte("package main\n\nfunc main() { changed }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	rows := plainRows(m.View())

	found := false
	for _, row := range rows {
		if i := strings.Index(row, "code.go"); i >= 0 {
			found = true
			if i >= gitPanelWidth {
				t.Fatalf("git file must render in the left rail (col %d >= %d): %q", i, gitPanelWidth, row)
			}
			// marker is git-short "XY": unstaged modification renders as " M"
			if !strings.HasPrefix(row, "  M ") {
				t.Fatalf("expected git status marker prefix, got %q", row)
			}
			break
		}
	}
	if !found {
		t.Fatal("changed file not rendered in git panel")
	}
}

func TestQuitKeysWorkInAllModes(t *testing.T) {
	dir, f := initTestGitRepo(t)

	// Project tree focused: Ctrl+Q must quit
	m := New(dir)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB}) // focus tree
	if !m.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ}); cmd == nil {
		t.Fatal("ctrl+q must quit while tree is focused")
	}

	// Git panel open (status mode): Ctrl+Q and Ctrl+C must quit
	m = New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ}); cmd == nil {
		t.Fatal("ctrl+q must quit while git panel is open")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c must quit while git panel is open")
	}

	// Git commit mode: typing 'q'/'c' still enters the message; Ctrl+Q quits
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.gitMode != gitModeCommit {
		t.Fatalf("expected commit mode after 'c', got %d", m.gitMode)
	}
	m = typeStr(m, "msg q c")
	if got := string(m.gitCommitIn); got != "msg q c" {
		t.Fatalf("commit input corrupted by quit-key letters: %q", got)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ}); cmd == nil {
		t.Fatal("ctrl+q must quit from commit mode")
	}

	// Dirty buffer + Ctrl+Q in git mode → confirm prompt, not silent quit
	m = New(f)
	m.width, m.height = 80, 24
	m.cur().buf.Insert('!')
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = m2.(Model)
	if !m.quitConfirm {
		t.Fatal("dirty buffer must raise quit confirmation")
	}
}

func TestCloseTabKeysWorkInAllModes(t *testing.T) {
	dir, f := initTestGitRepo(t)

	// Git panel open, no selection: Ctrl+X closes the only tab → quit
	m := New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX}); cmd == nil {
		t.Fatal("ctrl+x must close/quit while git panel is open")
	}

	// Tree focused, single untitled tab: Ctrl+W closes it → quit
	mt := New(dir)
	mt.width, mt.height = 80, 24
	if len(mt.tabs) != 1 {
		t.Fatalf("setup: want single untitled tab, got %d", len(mt.tabs))
	}
	mt = press(mt, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !mt.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	if _, cmd := mt.Update(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd == nil {
		t.Fatal("ctrl+w must close/quit while tree is focused")
	}
}

func TestCutWithSelectionInGitMode(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "cut.txt", "")

	m := New(f)
	m.width, m.height = 80, 24
	m = typeStr(m, "hello")
	m = press(m, tea.KeyMsg{Type: tea.KeyHome}) // select whole word
	m.cur().buf.StartSelection()
	m.cur().buf.LineEndWithSelect()
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m = m2.(Model)
	if m.clipboard != "hello" {
		t.Fatalf("cut must fill clipboard, got %q", m.clipboard)
	}
	if m.msg != "cut to clipboard" {
		t.Fatalf("unexpected msg %q", m.msg)
	}
	if !m.gitOpen {
		t.Fatal("cut must not close the git panel")
	}
}

func TestGitDiffView(t *testing.T) {
	dir, f := initTestGitRepo(t)

	// HEAD: "package main\n\nfunc main() {}\n"; change main and add many lines
	// so the diff is taller than the viewport and can actually scroll.
	var b strings.Builder
	b.WriteString("package main\n\nfunc main() { changed }\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "extra %d\n", i)
	}
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if len(m.gitFiles) != 1 {
		t.Fatalf("setup: want 1 changed file, got %d", len(m.gitFiles))
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !m.diffViewOpen {
		t.Fatal("'d' must open the side-by-side diff")
	}
	if len(m.diffRows) < m.viewHeight()+5 {
		t.Fatalf("setup: diff must be taller than viewport, rows=%d h=%d", len(m.diffRows), m.viewHeight())
	}

	half := (m.width - 1) / 2
	rows := plainRows(m.View())
	foundOld, foundNew, foundAdded := false, false, false
	for _, row := range rows {
		if i := strings.Index(row, "func main() {}"); i >= 0 {
			if i < half {
				foundOld = true
			}
		}
		if i := strings.Index(row, "changed"); i >= 0 {
			if i >= half {
				foundNew = true
			}
		}
		if strings.Contains(row, "extra") && strings.Index(row, "extra") >= half {
			foundAdded = true
		}
	}
	if !foundOld {
		t.Fatal("old text must render in the LEFT column")
	}
	if !foundNew {
		t.Fatal("modified text must render in the RIGHT column")
	}
	if !foundAdded {
		t.Fatal("added line must render in the RIGHT column")
	}

	// Scrolling moves the shared offset
	before := m.diffOffsetY
	m = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.diffOffsetY != before+1 {
		t.Fatalf("down must scroll: %d -> %d", before, m.diffOffsetY)
	}

	// Esc returns to the git panel state (panel still open)
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.diffViewOpen {
		t.Fatal("esc must close the diff view")
	}
	if !m.gitOpen {
		t.Fatal("git panel must stay open after closing the diff")
	}
	_ = dir
}

func TestSwitchGitTreePanels(t *testing.T) {
	dir, f := initTestGitRepo(t)
	_ = f

	m := New(dir)
	m.width, m.height = 80, 24
	if !m.treeVisible {
		t.Fatal("setup: project tree must be visible with a root")
	}

	// Focus the tree ("project mode"), then Ctrl+G must open the Git panel
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !m.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.gitOpen {
		t.Fatal("ctrl+g from tree mode must open the git panel")
	}
	if m.treeFocus {
		t.Fatal("tree must lose focus when git panel opens")
	}

	// In Git panel mode, Ctrl+B must switch back to the focused tree
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.gitOpen {
		t.Fatal("ctrl+b in git mode must close the git panel")
	}
	if !m.treeVisible || !m.treeFocus {
		t.Fatal("ctrl+b in git mode must show and focus the project tree")
	}

	// The tree rail actually renders project files again
	found := false
	for _, row := range plainRows(m.View()) {
		if strings.Contains(row, "code.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tree must render project entries after switching back")
	}
}

func TestEditorGitPanelStageToggle(t *testing.T) {
	dir, f := initTestGitRepo(t)

	if err := os.WriteFile(f, []byte("package main\n\nfunc main() { changed }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if len(m.gitFiles) != 1 || m.gitSel != 0 {
		t.Fatalf("expected one changed file selected, got %d files sel=%d", len(m.gitFiles), m.gitSel)
	}
	if m.gitFiles[0].IsStaged() {
		t.Fatal("modified file must start unstaged")
	}

	// Space stages
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !m.gitFiles[0].IsStaged() {
		t.Fatal("space must stage the selected file")
	}
	r, err := vcs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.StatusFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !files[0].IsStaged() {
		t.Fatalf("index must contain staged file, got %+v", files)
	}

	// Space again unstages
	m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.gitFiles[0].IsStaged() {
		t.Fatal("second space must unstage the file")
	}
}
