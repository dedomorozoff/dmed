package editor

import (
	"strings"
	"testing"

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
