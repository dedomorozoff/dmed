package editor

import (
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
