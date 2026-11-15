package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestLspAddPromptStart(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "main.go", "package main\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.startLspAddPrompt()
	if !m.lspAddOpen {
		t.Fatal("lsp-add prompt did not open")
	}
	if m.lspAddStep != 0 {
		t.Fatalf("step = %d, want 0", m.lspAddStep)
	}
	if got := string(m.lspAddIn); got != "go" {
		t.Fatalf("prefill = %q, want %q", got, "go")
	}
}

func TestLspAddPromptFlow(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(confPath, []byte("[editor]\ntab_width = 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New("")
	m.root = dir
	m.width, m.height = 80, 24

	// Start prompt
	m.startLspAddPrompt()
	if !m.lspAddOpen {
		t.Fatal("prompt should be open")
	}

	// Step 0: type "zig"
	m.lspAddIn = nil
	for _, r := range "zig" {
		m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}
	if got := string(m.lspAddIn); got != "zig" {
		t.Fatalf("typed = %q, want %q", got, "zig")
	}

	// Press Enter to advance to step 1
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if m.lspAddStep != 1 {
		t.Fatalf("expected step 1, got %d", m.lspAddStep)
	}
	if m.lspAddLang != "zig" {
		t.Fatalf("lspAddLang = %q, want %q", m.lspAddLang, "zig")
	}
	// "zig" is known in lspServerFor as "zls", so it should be prefilled
	if got := string(m.lspAddIn); got != "zls" {
		t.Fatalf("command prefill = %q, want %q", got, "zls")
	}

	// Append " --stdio"
	for _, r := range " --stdio" {
		m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}

	// Press Enter to commit and save
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if m.lspAddOpen {
		t.Fatal("prompt should close after submit")
	}
	if m.cfg.LSP.Languages["zig"] != "zls --stdio" {
		t.Fatalf("in-memory config for zig = %q, want %q", m.cfg.LSP.Languages["zig"], "zls --stdio")
	}

	// Verify disk file
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "lang.zig = zls --stdio") {
		t.Fatalf("config file missing lang.zig:\n%s", s)
	}
}

func TestLspAddPromptCancel(t *testing.T) {
	m := New("")
	m.width, m.height = 80, 24
	m.startLspAddPrompt()
	if !m.lspAddOpen {
		t.Fatal("prompt should be open")
	}

	// Esc cancels
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if m.lspAddOpen {
		t.Fatal("prompt should close on Esc")
	}
}

func TestLspAddPromptValidation(t *testing.T) {
	m := New("")
	m.width, m.height = 80, 24
	m.startLspAddPrompt()

	// Empty language on Enter does not advance
	m.lspAddIn = nil
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if m.lspAddStep != 0 {
		t.Fatalf("should stay on step 0, got %d", m.lspAddStep)
	}
	if m.msg == "" {
		t.Fatal("expected error message when language is empty")
	}

	// Advance with a language
	m.lspAddIn = []rune("foo")
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if m.lspAddStep != 1 {
		t.Fatalf("expected step 1, got %d", m.lspAddStep)
	}

	// Empty command on Enter does not close
	m.lspAddIn = nil
	m.handleLspAddPrompt(keyPair(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !m.lspAddOpen {
		t.Fatal("prompt should stay open when command is empty")
	}
	if m.msg == "" {
		t.Fatal("expected error message when command is empty")
	}
}

func TestLspAddPaletteCommand(t *testing.T) {
	m := New("")
	cmds := m.getPaletteCommands()
	var found bool
	for _, c := range cmds {
		if c.id == "lsp_add" {
			found = true
			c.action(&m)
			if !m.lspAddOpen {
				t.Fatal("lsp_add palette command did not open prompt")
			}
			break
		}
	}
	if !found {
		t.Fatal("lsp_add command not found in palette")
	}
}

func TestLspAddLineRender(t *testing.T) {
	m := New("")
	m.width, m.height = 80, 24
	m.lspAddOpen = true
	m.lspAddStep = 0
	m.lspAddIn = []rune("rust")

	line1 := m.lspAddLine()
	if !strings.Contains(line1, "rust") {
		t.Fatalf("line1 did not contain 'rust': %q", line1)
	}

	m.lspAddStep = 1
	m.lspAddLang = "rust"
	m.lspAddIn = []rune("rust-analyzer")
	line2 := m.lspAddLine()
	if !strings.Contains(line2, "rust-analyzer") {
		t.Fatalf("line2 did not contain 'rust-analyzer': %q", line2)
	}
}
