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
	_, f := initTestGitRepo(t)

	m := New(f)
	m.width, m.height = 80, 24

	// Modify file and save to create working tree changes
	m.cur().buf.Insert('!')
	m.saveActive()

	// Open Git panel (Ctrl+G)
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !m.gitOpen {
		t.Fatal("expected gitOpen true after Ctrl+G")
	}

	v := m.View()
	if !strings.Contains(v, "git commit:") {
		t.Fatalf("view should show git commit line, got:\n%s", v)
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
}
