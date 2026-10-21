package editor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/lsp"
)

func TestLSPServerFor(t *testing.T) {
	for ext, want := range map[string]string{
		".go":  "gopls",
		".py":  "pyright-langserver",
		".rs":  "rust-analyzer",
		".c":   "clangd",
		".cpp": "clangd",
		".ts":  "typescript-language-server",
		".lua": "lua-language-server",
	} {
		cmd, _, lang := lspServerFor(ext)
		if cmd != want {
			t.Errorf("lspServerFor(%q) cmd=%q want %q", ext, cmd, want)
		}
		if lang == "" {
			t.Errorf("lspServerFor(%q) missing language id", ext)
		}
	}
	if cmd, _, _ := lspServerFor(".txt"); cmd != "" {
		t.Errorf("lspServerFor(.txt) = %q, want empty (unsupported)", cmd)
	}
}

func TestLSPInstallFor(t *testing.T) {
	for bin, want := range map[string]string{
		"gopls":                      "go install golang.org/x/tools/gopls@latest",
		"pyright-langserver":         "npm i -g pyright",
		"typescript-language-server": "npm i -g typescript-language-server typescript",
		"solargraph":                 "gem install solargraph",
		"yaml-language-server":       "npm i -g yaml-language-server",
	} {
		if got := lspInstallFor(bin); got != want {
			t.Errorf("lspInstallFor(%q)=%q want %q", bin, got, want)
		}
	}
	if got := lspInstallFor("no-such-server"); got != "" {
		t.Errorf("lspInstallFor(unknown)=%q want empty", got)
	}
}

func TestLSPMissingHintUnsupported(t *testing.T) {
	// Extensions without an LSP server never produce a hint.
	if got := lspMissingHint("readme.txt"); got != "" {
		t.Fatalf("lspMissingHint(.txt)=%q want empty", got)
	}
	if got := lspMissingHint("noext"); got != "" {
		t.Fatalf("lspMissingHint(noext)=%q want empty", got)
	}
}

func TestMergeLSPCompletion(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "c.txt", "alpha beta\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.cur().buf.SetCursor(0, 3) // after "alp"
	m.complOpen = true
	m.complItems = []string{"alpine"}

	m.mergeLSPCompletion([]lsp.CompletionItem{
		{Label: "alpaca"},
		{Label: "alphabet"},
	})

	joined := strings.Join(m.complItems, ",")
	for _, want := range []string{"alpaca", "alphabet", "alpha"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("merged candidates missing %q: %v", want, m.complItems)
		}
	}
	// LSP items come first.
	if len(m.complItems) < 2 || m.complItems[0] != "alpaca" || m.complItems[1] != "alphabet" {
		t.Fatalf("LSP items should lead: %v", m.complItems)
	}
}

func TestMergeLSPCompletionDedupes(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "d.txt", "goo\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.cur().buf.SetCursor(0, 2) // after "go"
	m.complOpen = true

	m.mergeLSPCompletion([]lsp.CompletionItem{{Label: "good"}, {Label: "good"}})
	seen := map[string]int{}
	for _, it := range m.complItems {
		seen[it]++
	}
	if seen["good"] != 1 {
		t.Fatalf("'good' duplicated: %v", m.complItems)
	}
}

func TestGotoDefinitionJumpsToLocation(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "line0\nline1\nline2\n")
	writeTemp(t, dir, "b.txt", "target line\n")

	m := New(a)
	m.width, m.height = 80, 24
	if len(m.tabs) != 1 || m.cur().path != a {
		t.Fatalf("setup: tabs=%d path=%q want %q", len(m.tabs), m.cur().path, a)
	}

	res, _ := m.Update(lspDefinitionMsg{
		path: a,
		loc:  &lsp.Location{Path: filepath.Join(dir, "b.txt"), Line: 0, Col: 3},
	})
	m = res.(Model)

	if len(m.tabs) != 2 {
		t.Fatalf("definition should open the target file, tabs=%d", len(m.tabs))
	}
	if m.cur().path != filepath.Join(dir, "b.txt") {
		t.Fatalf("active tab = %q, want target file", m.cur().path)
	}
	if l, c := m.cur().buf.CurLine(), m.cur().buf.Col(); l != 0 || c != 3 {
		t.Fatalf("cursor at %d:%d, want 0:3", l, c)
	}
}

func TestGotoDefinitionNone(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "hi\n")
	m := New(f)
	m.width, m.height = 80, 24

	res, _ := m.Update(lspDefinitionMsg{path: f})
	m = res.(Model)
	if m.msg != "no definition found" {
		t.Fatalf("status = %q, want no-definition message", m.msg)
	}
}

func TestGotoDefinitionError(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "hi\n")
	m := New(f)
	m.width, m.height = 80, 24

	res, _ := m.Update(lspDefinitionMsg{path: f, err: errors.New("boom")})
	m = res.(Model)
	if !strings.Contains(m.msg, "boom") {
		t.Fatalf("status = %q, want error detail", m.msg)
	}
}

func TestF12BindingReturnsGotoCommand(t *testing.T) {
	// F12 must dispatch gotoDefinition without panicking even when no LSP
	// server is installed (gotoDefinitionAt returns nil in that case).
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "hi\n")
	m := New(f)
	m.width, m.height = 80, 24

	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyF12})
	if cmd == nil {
		return // expected when gopls is absent; just must not panic
	}
	t.Fatalf("unexpected non-nil command from F12 without LSP")
}

// TestWordRunesAt covers the prefill span of rename: the word under the
// caret, and the word just left of it when the caret sits at its end.
func TestWordRunesAt(t *testing.T) {
	line := []rune("func omega() {}")
	cases := []struct {
		col      int
		s, e     int
		wantWord string
	}{
		{col: 0, s: 0, e: 4, wantWord: "func"},    // first rune of the word
		{col: 4, s: 0, e: 4, wantWord: "func"},    // space right after a word
		{col: 5, s: 5, e: 10, wantWord: "omega"},  // first rune
		{col: 7, s: 5, e: 10, wantWord: "omega"},  // middle
		{col: 10, s: 5, e: 10, wantWord: "omega"}, // exactly past the end
		{col: 11, s: 11, e: 11, wantWord: ""},     // on '('
		{col: 12, s: 12, e: 12, wantWord: ""},     // space before {}
	}
	for _, tc := range cases {
		s, e := wordRunesAt(line, tc.col)
		if s != tc.s || e != tc.e {
			t.Errorf("col %d: span %d:%d, want %d:%d", tc.col, s, e, tc.s, tc.e)
		}
		if got := string(line[s:e]); got != tc.wantWord {
			t.Errorf("col %d: word %q, want %q", tc.col, got, tc.wantWord)
		}
	}
}

// TestStartLspRenamePrefill opens the overlay with the symbol under the
// cursor; Escape and empty input behave.
func TestStartLspRenamePrefill(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "sym.go", "func omega() {}\n")
	m := New(f)
	m.lspClient = &lsp.Client{} // stub: never spawn gopls in tests

	m.cur().buf.SetCursor(0, 7)
	m.startLspRename()
	if !m.lspRenameOpen {
		t.Fatal("rename overlay did not open")
	}
	if got := string(m.lspRenameIn); got != "omega" {
		t.Fatalf("prefill = %q, want %q", got, "omega")
	}

	// Enter closes the overlay and fires the request (as a tea.Cmd).
	closed := m
	closed.lspRenameIn = []rune("newname")
	if cmd := closed.startLspRenameCmd(); cmd == nil {
		t.Fatal("Enter with a name produced no request")
	}
	if closed.lspRenameOpen {
		t.Fatal("overlay stayed open after Enter")
	}

	// Esc closes without firing.
	m.handleLspRename(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.lspRenameOpen {
		t.Fatal("Escape did not close the overlay")
	}

	// Empty input on Enter does not fire a rename.
	m.startLspRename()
	m.lspRenameIn = nil
	if m.startLspRenameCmd() != nil {
		t.Fatal("empty name produced a rename request")
	}
}

// TestStartLspRenameNoLSP refuses to open for files without a language server.
func TestStartLspRenameNoLSP(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "notes.txt", "some word\n")
	m := New(f)
	m.startLspRename()
	if m.lspRenameOpen {
		t.Fatal("rename opened without an LSP server")
	}
	if m.msg == "" {
		t.Fatal("the user should be told why rename did not open")
	}
}

// TestHandleLspRenameTyping drives the input: letters append, Backspace
// erases, Ctrl+C closes.
func TestHandleLspRenameTyping(t *testing.T) {
	m := Model{}
	m.lspRenameOpen = true
	m.lspRenameIn = []rune("ab")

	m.handleLspRename(tea.KeyPressMsg{Text: "c"})
	if got := string(m.lspRenameIn); got != "abc" {
		t.Fatalf("after typing: %q", got)
	}
	m.handleLspRename(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := string(m.lspRenameIn); got != "ab" {
		t.Fatalf("after backspace: %q", got)
	}
	m.handleLspRename(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.lspRenameOpen {
		t.Fatal("Esc did not close the overlay")
	}
}

// TestHandleLspActionsSelectAndApply drives the quick-fix list: cursor keys,
// command-only actions announce themselves, edits apply.
func TestHandleLspActionsSelectAndApply(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "sym.go", "func omega() {}\n")
	m := New(f)
	m.lspClient = &lsp.Client{}
	fp, _ := filepath.Abs(f)
	m.lspActionsOpen = true
	m.lspActions = []lsp.CodeAction{
		{Title: "ask me something else"}, // command-only: no Edit
		{Title: "fix it", Edit: &lsp.WorkspaceEdit{Files: map[string][]lsp.TextEdit{
			fp: {{StartLine: 0, StartCol: 5, EndLine: 0, EndCol: 10, NewText: "beta"}},
		}}},
	}

	// First action is command-only: Enter reports and closes.
	m.handleLspActions(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.lspActionsOpen {
		t.Fatal("overlay stayed open after applying")
	}
	if !strings.Contains(m.msg, "command-only") {
		t.Fatalf("msg = %q, want a command-only complaint", m.msg)
	}

	// Move down to the editable action and apply it: the buffer changes.
	m.lspActionsOpen = true
	m.handleLspActions(tea.KeyPressMsg{Code: tea.KeyDown}) // sel → 1
	m.handleLspActions(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.cur().buf.Text(); got != "func beta() {}\n" {
		t.Fatalf("edit not applied: %q", got)
	}
	if !strings.Contains(m.msg, "1 file") {
		t.Fatalf("msg = %q, want an applied-file count", m.msg)
	}

	// Escape closes the list without applying anything.
	m.lspActionsOpen = true
	m.lspActionsSel = 1
	m.handleLspActions(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.lspActionsOpen {
		t.Fatal("Escape did not close the list")
	}
}

// TestApplyWorkspaceEditOpenAndDisk edits an open tab in-buffer and a closed
// file on disk; unreadable paths are skipped rather than aborting the series.
func TestApplyWorkspaceEditOpenAndDisk(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "open.go", "alpha omega\n")
	f2 := writeTemp(t, dir, "closed.txt", "beta omega\n")
	m := New(f1)
	m.lspClient = &lsp.Client{}

	absF1, _ := filepath.Abs(f1)
	absF2, _ := filepath.Abs(f2)
	absF3, _ := filepath.Abs(filepath.Join(dir, "missing.txt"))

	n := m.applyWorkspaceEdit(&lsp.WorkspaceEdit{Files: map[string][]lsp.TextEdit{
		absF1: {{StartLine: 0, StartCol: 6, EndLine: 0, EndCol: 11, NewText: "beta"}},
		absF2: {{StartLine: 0, StartCol: 5, EndLine: 0, EndCol: 10, NewText: "alpha"}},
		absF3: {{StartLine: 0, StartCol: 0, EndLine: 0, EndCol: 5, NewText: "x"}},
	}})
	if n != 2 {
		t.Fatalf("touched %d files, want 2", n)
	}
	if got := m.cur().buf.Text(); got != "alpha beta\n" {
		t.Fatalf("open buffer = %q, want %q", got, "alpha beta\n")
	}
	if !m.cur().buf.Dirty() {
		t.Fatal("an applied workspace edit must leave the tab dirty")
	}
	data, _ := os.ReadFile(f2)
	if string(data) != "beta alpha\n" {
		t.Fatalf("closed file on disk = %q", string(data))
	}

	// A nil edit set touches nothing.
	if n := m.applyWorkspaceEdit(nil); n != 0 {
		t.Fatalf("nil edit touched %d files", n)
	}
}

// TestHandleLspActionsMsgEmpty records the "no actions" case so the next
// request does not open an empty list.
func TestHandleLspActionsMsgEmpty(t *testing.T) {
	m := Model{}
	m.handleLspActionsMsg(lspActionsMsg{})
	if m.lspActionsOpen {
		t.Fatal("an empty action list must not open the overlay")
	}
	if m.msg == "" {
		t.Fatal("the user should be told there were no actions")
	}
}
