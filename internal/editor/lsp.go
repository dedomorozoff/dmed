package editor

import (
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/lsp"
)

// lspCompletionMsg carries async completion results from the language server
// back into the editor model.
type lspCompletionMsg struct {
	path  string
	items []lsp.CompletionItem
	err   error
}

// lspServerFor maps a file extension to its LSP server command + language id.
// Empty command means no LSP support for that extension.
func lspServerFor(ext string) (cmd string, args []string, langID string) {
	switch ext {
	case ".go":
		return "gopls", nil, "go"
	case ".py":
		return "pyright-langserver", []string{"--stdio"}, "python"
	}
	return "", nil, ""
}

// ensureLSP starts a language server for the current file if supported and the
// server binary is installed. It is lazy: only invoked when completion is used.
func (m *Model) ensureLSP() {
	if m.lspClient != nil {
		return
	}
	t := m.cur()
	if t == nil || t.path == "" {
		return
	}
	cmd, args, lang := lspServerFor(strings.ToLower(filepath.Ext(t.path)))
	if cmd == "" {
		return
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return
	}
	root := m.root
	if root == "" {
		root = filepath.Dir(t.path)
	}
	c, err := lsp.Start(cmd, args, root, nil)
	if err != nil {
		return
	}
	m.lspClient = c
	m.lspClient.DidOpen(t.path, lang, t.buf.Text())
}

// lspCompletionCmd fires an async completion request for the current cursor and
// returns a command that delivers an lspCompletionMsg when results arrive.
func (m *Model) lspCompletionCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, _ := lspServerFor(strings.ToLower(filepath.Ext(t.path)))
	if cmd == "" {
		return nil
	}
	if m.lspClient == nil {
		m.ensureLSP()
	}
	if m.lspClient == nil {
		return nil
	}
	path := t.path
	line, col := t.buf.CurLine(), t.buf.Col()
	text := t.buf.Text()
	c := m.lspClient
	return func() tea.Msg {
		c.DidChange(path, text, 1)
		items, err := c.Completion(path, line, col)
		if err != nil {
			return lspCompletionMsg{path: path, err: err}
		}
		return lspCompletionMsg{path: path, items: items}
	}
}

// mergeLSPCompletion folds server-provided items into the open popup, keeping
// LSP candidates first and falling back to buffer words for the rest.
func (m *Model) mergeLSPCompletion(items []lsp.CompletionItem) {
	_, prefix := m.complPrefix()
	seen := map[string]bool{}
	var combined []string
	for _, it := range items {
		if it.Label != "" && strings.HasPrefix(it.Label, prefix) && !seen[it.Label] {
			combined = append(combined, it.Label)
			seen[it.Label] = true
		}
	}
	for _, w := range m.complWordCandidates(prefix) {
		if !seen[w] {
			combined = append(combined, w)
			seen[w] = true
		}
	}
	m.complItems = combined
	if m.complSel >= len(combined) {
		m.complSel = 0
		m.complOffset = 0
	}
}
