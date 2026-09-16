package editor

import (
	"errors"
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
