package editor

import (
	"os/exec"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/lsp"
)

// TestLSPResolveLanguageOverride checks the [lsp] lang.<id> overrides: a
// replacement command wins over the built-in table, an empty value removes
// the language, and a brand-new extension can be added by id.
func TestLSPResolveLanguageOverride(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.cfg.LSP.Languages = map[string]string{
		"go":     "my-gopls --stdio", // replace the built-in
		"python": "",                 // explicitly removed (the built-in id)
		"foo":    "foo-ls",           // a language the table never knew
	}

	cmd, args, lang := m.lspResolve(".go")
	if cmd != "my-gopls" || len(args) != 1 || args[0] != "--stdio" || lang != "go" {
		t.Fatalf("go override: %q %v %q", cmd, args, lang)
	}
	if cmd, _, _ := m.lspResolve(".py"); cmd != "" {
		t.Fatalf("python removal: got %q", cmd)
	}
	if cmd, _, lang := m.lspResolve(".foo"); cmd != "foo-ls" || lang != "foo" {
		t.Fatalf("foo add: %q %q", cmd, lang)
	}
	// An untouched language keeps its built-in server.
	if cmd, _, _ := m.lspResolve(".rs"); cmd != "rust-analyzer" {
		t.Fatalf("rs built-in: %q", cmd)
	}
}

// TestEnsureLSPRespawnsDead checks the auto-restart: a client whose server
// died is dropped by ensureLSP and a fresh one started on the next request.
// Driven by a real (immediately exiting) process so Alive goes false for real.
func TestEnsureLSPRespawnsDead(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "package main\n")
	m.openPath(p)
	m.root = dir

	dead, err := lsp.Start("cmd", []string{"/c", "exit"}, dir, nil)
	if err != nil {
		t.Skipf("cannot spawn a dying process: %v", err)
	}
	m.lspClient = dead
	// Wait for the read loop to hit EOF and mark the client dead.
	for i := 0; i < 50 && dead.Alive(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	_ = dead.Close()
	m.ensureLSP()
	if m.lspClient == dead {
		t.Fatal("dead client survived ensureLSP")
	}
	if m.lspClient != nil && !m.lspClient.Alive() {
		t.Fatal("respawned client is not alive")
	}
	if m.lspClient != nil {
		_ = m.lspClient.Close()
	}
}

// TestHoverAndRefsThroughUpdate drive the two async replies through Update:
// the hover opens its overlay, the refs open theirs and Enter jumps.
func TestHoverAndRefsThroughUpdate(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	next, _ := m.Update(lspHoverMsg{lines: []string{"func F(x int) int"}})
	m = next.(Model)
	if !m.hoverOpen || len(m.hoverText) != 1 {
		t.Fatal("hover reply did not open the overlay")
	}
	// Esc closes it.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.hoverOpen {
		t.Fatal("Esc did not close the hover")
	}

	next, _ = m.Update(lspRefsMsg{refs: []lsp.Location{
		{Path: "a.go", Line: 3},
		{Path: "b.go", Line: 8},
	}})
	m = next.(Model)
	if !m.refsOpen || len(m.refs) != 2 {
		t.Fatal("refs reply did not open the panel")
	}
	// Enter jumps to the first hit: the file opens and the caret lands.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.refsOpen {
		t.Fatal("Enter did not close the refs panel")
	}
	// Empty replies keep the panels closed and say so.
	next, _ = m.Update(lspHoverMsg{})
	m = next.(Model)
	if m.hoverOpen {
		t.Fatal("empty hover opened the overlay")
	}
	next, _ = m.Update(lspRefsMsg{})
	m = next.(Model)
	if m.refsOpen || m.msg == "" {
		t.Fatalf("empty refs: open=%v msg=%q", m.refsOpen, m.msg)
	}
}

// TestOutlinePanelNavigation walks the symbol list and jumps with Enter.
func TestOutlinePanelNavigation(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "one\ntwo\nthree\n")
	m.openPath(p)

	next, _ := m.Update(lspOutlineMsg{syms: []lsp.DocumentSymbol{
		{Name: "first", Kind: 12, Line: 0},
		{Name: "second", Kind: 12, Line: 1},
		{Name: "third", Kind: 5, Line: 2},
	}})
	m = next.(Model)
	if !m.outlineOpen {
		t.Fatal("outline did not open")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.outlineSel != 1 {
		t.Fatalf("outlineSel = %d, want 1", m.outlineSel)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.outlineOpen {
		t.Fatal("Enter did not close the outline")
	}
	if m.cur().buf.CurLine() != 1 {
		t.Fatalf("outline jump landed on line %d, want 1", m.cur().buf.CurLine())
	}
}

// TestDiagHintFor checks the inline hint lookup by line.
func TestDiagHintFor(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "one\ntwo\n")
	m.openPath(p)
	m.diags[p] = []lsp.Diagnostic{{Line: 1, Message: "two is broken"}}
	if got := m.diagHintFor(1); got != "two is broken" {
		t.Fatalf("hint = %q", got)
	}
	if got := m.diagHintFor(0); got != "" {
		t.Fatalf("hint on clean line = %q", got)
	}
}
