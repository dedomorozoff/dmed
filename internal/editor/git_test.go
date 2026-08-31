package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
	plain := strings.Join(plainRows(v.Content), "\n")
	if !strings.Contains(plain, "master") && !strings.Contains(plain, "main") {
		t.Fatalf("view should show git branch in status bar:\n%s", v.Content)
	}

	// Modify buffer (insert a new line)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeStr(m, "// added comment")

	// View should render gutter diff marker '+'
	v = m.View()
	if !strings.Contains(v.Content, "+") {
		t.Fatalf("gutter should show '+' for added line:\n%s", v.Content)
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
	m = press(m, tea.KeyPressMsg{Text: string('r')})
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
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("expected gitOpen true after Ctrl+G")
	}
	if m.gitMode != gitModeStatus {
		t.Fatalf("panel must open in status mode, got %d", m.gitMode)
	}
	v := m.View()
	if !strings.Contains(v.Content, "code.go") {
		t.Fatalf("status mode should list changed files, got:\n%s", v.Content)
	}

	// Stage all ('a'), then switch to commit mode ('c')
	m = press(m, tea.KeyPressMsg{Text: string('a')})
	m = press(m, tea.KeyPressMsg{Text: string('c')})
	if m.gitMode != gitModeCommit {
		t.Fatalf("expected commit mode after 'c', got %d", m.gitMode)
	}
	v = m.View()
	if !strings.Contains(v.Content, "git commit:") {
		t.Fatalf("commit mode should show git commit line, got:\n%s", v.Content)
	}

	// Type commit message and commit
	m = typeStr(m, "test commit message")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
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

	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	rows := plainRows(m.View().Content)

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

func TestEditorGitPanelStatusHints(t *testing.T) {
	_, f := initTestGitRepo(t)

	// With a repo: the git status line shows the action hints.
	m := New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	content := m.View().Content
	if !strings.Contains(content, "stage") || !strings.Contains(content, "commit") {
		t.Fatalf("git status line should show action hints, got:\n%s", content)
	}

	// Without a repo: the status line must hint at git init.
	norepo := t.TempDir()
	nf := filepath.Join(norepo, "plain.txt")
	if err := os.WriteFile(nf, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = New(nf)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	content = m.View().Content
	if !strings.Contains(content, "init repo") {
		t.Fatalf("git status line should hint at init for no-repo dir, got:\n%s", content)
	}
}

func TestQuitKeysWorkInAllModes(t *testing.T) {
	dir, f := initTestGitRepo(t)

	// Project tree focused: Ctrl+Q must quit
	m := New(dir)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}) // focus tree
	if !m.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+q must quit while tree is focused")
	}

	// Git panel open (status mode): Ctrl+Q and Ctrl+C must quit
	m = New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+q must quit while git panel is open")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+c must quit while git panel is open")
	}

	// Git commit mode: typing 'q'/'c' still enters the message; Ctrl+Q quits
	m = press(m, tea.KeyPressMsg{Text: string('c')})
	if m.gitMode != gitModeCommit {
		t.Fatalf("expected commit mode after 'c', got %d", m.gitMode)
	}
	m = typeStr(m, "msg q c")
	if got := string(m.gitCommitIn); got != "msg q c" {
		t.Fatalf("commit input corrupted by quit-key letters: %q", got)
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+q must quit from commit mode")
	}

	// Dirty buffer + Ctrl+Q in git mode → confirm prompt, not silent quit
	m = New(f)
	m.width, m.height = 80, 24
	m.cur().buf.Insert('!')
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
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
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+x must close/quit while git panel is open")
	}

	// Tree focused, single untitled tab: Ctrl+W closes it → quit
	mt := New(dir)
	mt.width, mt.height = 80, 24
	if len(mt.tabs) != 1 {
		t.Fatalf("setup: want single untitled tab, got %d", len(mt.tabs))
	}
	mt = press(mt, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !mt.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	if _, cmd := mt.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+w must close/quit while tree is focused")
	}
}

func TestCutWithSelectionInGitMode(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "cut.txt", "")

	m := New(f)
	m.width, m.height = 80, 24
	m = typeStr(m, "hello")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyHome}) // select whole word
	m.cur().buf.StartSelection()
	m.cur().buf.LineEndWithSelect()
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}

	m2, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
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

	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if len(m.gitFiles) != 1 {
		t.Fatalf("setup: want 1 changed file, got %d", len(m.gitFiles))
	}
	m = press(m, tea.KeyPressMsg{Text: string('d')})
	if !m.diffViewOpen {
		t.Fatal("'d' must open the side-by-side diff")
	}
	if len(m.diffRows) < m.viewHeight()+5 {
		t.Fatalf("setup: diff must be taller than viewport, rows=%d h=%d", len(m.diffRows), m.viewHeight())
	}

	half := (m.width - 1) / 2
	rows := plainRows(m.View().Content)
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
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.diffOffsetY != before+1 {
		t.Fatalf("down must scroll: %d -> %d", before, m.diffOffsetY)
	}

	// Esc returns to the git panel state (panel still open)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
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
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.treeFocus {
		t.Fatal("setup: tree must be focused")
	}
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("ctrl+g from tree mode must open the git panel")
	}
	if m.treeFocus {
		t.Fatal("tree must lose focus when git panel opens")
	}

	// In Git panel mode, Ctrl+B must switch back to the focused tree
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.gitOpen {
		t.Fatal("ctrl+b in git mode must close the git panel")
	}
	if !m.treeVisible || !m.treeFocus {
		t.Fatal("ctrl+b in git mode must show and focus the project tree")
	}

	// The tree rail actually renders project files again
	found := false
	for _, row := range plainRows(m.View().Content) {
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

	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if len(m.gitFiles) != 1 || m.gitSel != 0 {
		t.Fatalf("expected one changed file selected, got %d files sel=%d", len(m.gitFiles), m.gitSel)
	}
	if m.gitFiles[0].IsStaged() {
		t.Fatal("modified file must start unstaged")
	}

	// Space stages
	m = press(m, tea.KeyPressMsg{Text: string(' ')})
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
	m = press(m, tea.KeyPressMsg{Text: string(' ')})
	if m.gitFiles[0].IsStaged() {
		t.Fatal("second space must unstage the file")
	}
}

func TestGitKeyNameCyrillicLayout(t *testing.T) {
	// 'и' is the Cyrillic letter that physically occupies the 'b' key (ЙЦУКЕН).
	if got := gitKeyName(tea.KeyPressMsg{Text: "и"}); got != "b" {
		t.Fatalf("Cyrillic 'и' must map to 'b', got %q", got)
	}
	if got := gitKeyName(tea.KeyPressMsg{Text: "И"}); got != "b" {
		t.Fatalf("Cyrillic 'И' must map to 'b', got %q", got)
	}
	if got := gitKeyName(tea.KeyPressMsg{Code: 'и'}); got != "b" {
		t.Fatalf("Cyrillic Code 'и' must map to 'b', got %q", got)
	}
	// Non-Cyrillic and special keys must come through unchanged.
	if got := gitKeyName(tea.KeyPressMsg{Code: 'x'}); got != "x" {
		t.Fatalf("Latin 'x' must stay 'x', got %q", got)
	}
	if got := gitKeyName(tea.KeyPressMsg{Code: tea.KeyEnter}); got != "enter" {
		t.Fatalf("Enter must stay 'enter', got %q", got)
	}
	if got := gitKeyName(tea.KeyPressMsg{Code: tea.KeyEsc}); got != "esc" {
		t.Fatalf("Esc must stay 'esc', got %q", got)
	}
	// BaseCode wins when present (Windows Console API).
	if got := gitKeyName(tea.KeyPressMsg{Text: "и", BaseCode: 'b'}); got != "b" {
		t.Fatalf("BaseCode 'b' must win, got %q", got)
	}
}

func TestEditorGitInitThenCreateBranch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "code.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	// Open git panel on a non-repo dir: init hint, then init the repo.
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}
	m = press(m, tea.KeyPressMsg{Text: string('i')})
	if m.repo == nil {
		t.Fatal("git init from panel must set the repo")
	}
	if got := m.repo.Branch(); got != "main" {
		t.Fatalf("expected main after init, got %q", got)
	}

	// Branch view, then create a new branch.
	m = press(m, tea.KeyPressMsg{Text: string('b')})
	if m.gitMode != gitModeBranch {
		t.Fatalf("expected branch mode after 'b', got %d", m.gitMode)
	}
	m = press(m, tea.KeyPressMsg{Text: string('n')})
	if !m.gitBranchNew {
		t.Fatal("expected new-branch input after 'n'")
	}
	m = typeStr(m, "feature")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.gitMode != gitModeStatus {
		t.Fatalf("expected to return to status mode after creating branch, got %d", m.gitMode)
	}
	if got := m.repo.Branch(); got != "feature" {
		t.Fatalf("expected branch feature, got %q (msg=%q)", got, m.msg)
	}

	// main is a real branch and can be switched back to from the panel. The
	// current branch (feature) is now part of the navigable list too.
	m = press(m, tea.KeyPressMsg{Text: string('b')})
	if m.gitMode != gitModeBranch {
		t.Fatalf("expected branch mode after 'b', got %d", m.gitMode)
	}
	if !slices.Contains(m.gitBranchList, "main") || !slices.Contains(m.gitBranchList, "feature") {
		t.Fatalf("branch list must contain current and other branches, got %v", m.gitBranchList)
	}
	for i, n := range m.gitBranchList {
		if n == "main" {
			m.gitBranchSel = i
			break
		}
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.repo.Branch(); got != "main" {
		t.Fatalf("expected main after switch back, got %q (msg=%q)", got, m.msg)
	}
}

func TestEditorGitBranchSwitchFlow(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "code.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24

	// Init the repo, then make a real commit on main.
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: string('i')})
	m = press(m, tea.KeyPressMsg{Text: string('a')})
	m = press(m, tea.KeyPressMsg{Text: string('c')})
	m = typeStr(m, "work")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// Create a second branch off main.
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: string('b')})
	if !slices.Equal(m.gitBranchList, []string{"main"}) {
		t.Fatalf("setup: only current branch expected on main, got %v", m.gitBranchList)
	}
	m = press(m, tea.KeyPressMsg{Text: string('n')})
	m = typeStr(m, "feature")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.repo.Branch(); got != "feature" {
		t.Fatalf("expected feature, got %q (msg=%q)", got, m.msg)
	}

	// Branch view lists all branches; switching goes back and forth. The
	// target is selected by index so the test is order-independent.
	switchTo := func(want string) {
		m = press(m, tea.KeyPressMsg{Text: string('b')})
		if !slices.Contains(m.gitBranchList, want) {
			t.Fatalf("expected %s in branch list, got %v", want, m.gitBranchList)
		}
		for i, n := range m.gitBranchList {
			if n == want {
				m.gitBranchSel = i
				break
			}
		}
		m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := m.repo.Branch(); got != want {
			t.Fatalf("expected %s after switch, got %q (msg=%q)", want, got, m.msg)
		}
	}
	switchTo("main")
	switchTo("feature")
	switchTo("main")
}

func TestEditorGitBranchOnRepoWithoutCommits(t *testing.T) {
	// A repo created externally (e.g. plain `git init`) has no commits yet.
	// Creating a branch from the panel must not lose the current branch, and
	// both branches must be switchable afterwards.
	dir := t.TempDir()
	f := filepath.Join(dir, "code.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}
	repoFor := func() *vcs.Repo {
		r, err := vcs.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	orig := repoFor().Branch()
	if orig == "" {
		t.Fatal("expected the unborn HEAD branch name to resolve (e.g. master)")
	}

	m = press(m, tea.KeyPressMsg{Text: string('b')})
	if m.gitMode != gitModeBranch {
		t.Fatalf("expected branch mode after 'b', got %d", m.gitMode)
	}
	m = press(m, tea.KeyPressMsg{Text: string('n')})
	m = typeStr(m, "feature")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := repoFor().Branch(); got != "feature" {
		t.Fatalf("expected feature after create, got %q (msg=%q)", got, m.msg)
	}
	// The original branch must have survived and be listed as switchable.
	m = press(m, tea.KeyPressMsg{Text: string('b')})
	if !slices.Contains(m.gitBranchList, orig) {
		t.Fatalf("expected %s in branch list, got %v", orig, m.gitBranchList)
	}
	for i, n := range m.gitBranchList {
		if n == orig {
			m.gitBranchSel = i
			break
		}
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := repoFor().Branch(); got != orig {
		t.Fatalf("expected %s after switch back, got %q (msg=%q)", orig, got, m.msg)
	}
}

func TestGitBranchViaCyrillicB(t *testing.T) {
	_, f := initTestGitRepo(t)

	m := New(f)
	m.width, m.height = 80, 24

	// Open git panel, then press 'b' as the Cyrillic letter 'и'.
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.gitOpen {
		t.Fatal("setup: git panel must be open")
	}
	m = press(m, tea.KeyPressMsg{Text: "и"})
	if m.gitMode != gitModeBranch {
		t.Fatalf("Cyrillic 'и' (physical b) must open branch mode, got mode %d", m.gitMode)
	}

	// Bottom bar must describe branch controls.
	v := m.View()
	if !strings.Contains(v.Content, "BRANCH") || !strings.Contains(v.Content, "Enter: checkout") {
		t.Fatalf("branch mode bottom bar must be shown:\n%s", v.Content)
	}
}

func TestGitBranchPanelScrollReachesLast(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "x.txt", "")
	m := New(f)
	m.height = 8
	m.gitMode = gitModeBranch

	list := make([]string, 10)
	for i := range list {
		list[i] = fmt.Sprintf("branch-%d", i)
	}
	m.gitBranchList = list
	m.gitBranchSel = len(list) - 1
	m.clampGitBranchScroll()

	h := m.viewHeight()
	if want := len(list) - h; m.gitBranchOffset != want {
		t.Fatalf("offset=%d want %d (list=%d h=%d)", m.gitBranchOffset, want, len(list), h)
	}

	rows := m.branchPanel(h)
	if len(rows) != h {
		t.Fatalf("panel rows=%d want %d", len(rows), h)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "branch-9") {
		t.Fatalf("last branch must be visible after scrolling to it:\n%s", strings.Join(rows, "\n"))
	}
}

func TestGitBranchModeCaseInsensitiveKeys(t *testing.T) {
	_, f := initTestGitRepo(t)
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	// Uppercase 'B' (as hinted) must open branch mode just like lowercase 'b'.
	m = press(m, tea.KeyPressMsg{Text: "B"})
	if m.gitMode != gitModeBranch {
		t.Fatalf("uppercase 'B' must open branch mode, got mode %d", m.gitMode)
	}
	// Uppercase 'N' enters the new-branch input, and 'Q' leaves branch mode.
	m = press(m, tea.KeyPressMsg{Text: "N"})
	if !m.gitBranchNew {
		t.Fatal("uppercase 'N' must start new-branch input")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.gitBranchNew {
		t.Fatal("esc must cancel new-branch input")
	}
	m = press(m, tea.KeyPressMsg{Text: "Q"})
	if m.gitMode == gitModeBranch {
		t.Fatal("uppercase 'Q' must leave branch mode")
	}
}
