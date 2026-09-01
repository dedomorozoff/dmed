package editor

import (
	"fmt"
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

// lspDiagMsg carries async diagnostics for a file from the language server.
type lspDiagMsg struct {
	path  string
	diags []lsp.Diagnostic
}

// waitForLSPDiag blocks until diagnostics arrive and forwards them as a Msg.
func waitForLSPDiag(ch chan lspDiagMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// lspServerFor maps a file extension to its LSP server command + language id.
// Empty command means no LSP support for that extension. The server is only
// started if the binary is found on PATH, so absent servers degrade gracefully
// to word-based completion.
func lspServerFor(ext string) (cmd string, args []string, langID string) {
	switch ext {
	case ".go":
		return "gopls", nil, "go"
	case ".py":
		return "pyright-langserver", []string{"--stdio"}, "python"
	case ".ts", ".mts", ".cts", ".tsx", ".js", ".mjs", ".jsx":
		return "typescript-language-server", []string{"--stdio"}, "typescript"
	case ".rs":
		return "rust-analyzer", nil, "rust"
	case ".c", ".h":
		return "clangd", nil, "c"
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx":
		return "clangd", nil, "cpp"
	case ".lua":
		return "lua-language-server", nil, "lua"
	case ".rb":
		return "solargraph", []string{"stdio"}, "ruby"
	case ".php":
		return "intelephense", []string{"--stdio"}, "php"
	case ".zig":
		return "zls", nil, "zig"
	case ".json":
		return "vscode-json-languageserver", []string{"--stdio"}, "json"
	case ".yaml", ".yml":
		return "yaml-language-server", []string{"--stdio"}, "yaml"
	case ".css", ".scss", ".less":
		return "vscode-css-languageserver", []string{"--stdio"}, "css"
	case ".html":
		return "vscode-html-languageserver", []string{"--stdio"}, "html"
	}
	return "", nil, ""
}

// lspInstallFor returns the command that installs the given LSP server binary,
// or "" if there's no known way to install it.
func lspInstallFor(bin string) string {
	switch bin {
	case "gopls":
		return "go install golang.org/x/tools/gopls@latest"
	case "pyright-langserver":
		return "npm i -g pyright"
	case "typescript-language-server":
		return "npm i -g typescript-language-server typescript"
	case "rust-analyzer":
		return "rustup component add rust-analyzer"
	case "solargraph":
		return "gem install solargraph"
	case "intelephense":
		return "npm i -g intelephense"
	case "vscode-json-languageserver", "vscode-css-languageserver", "vscode-html-languageserver":
		return "npm i -g vscode-langservers-extracted"
	case "yaml-language-server":
		return "npm i -g yaml-language-server"
	}
	return ""
}

// lspMissingHint returns an install hint for path if its language uses an LSP
// server that isn't on PATH, or "" otherwise. When a known install command
// exists it is included so the user can run it (e.g. in the built-in terminal).
func lspMissingHint(path string) string {
	cmd, _, _ := lspServerFor(strings.ToLower(filepath.Ext(path)))
	if cmd == "" {
		return ""
	}
	if _, err := exec.LookPath(cmd); err == nil {
		return ""
	}
	if inst := lspInstallFor(cmd); inst != "" {
		return fmt.Sprintf("%s — %s", cmd, inst)
	}
	return cmd
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
	c, err := lsp.Start(cmd, args, root, func(path string, diags []lsp.Diagnostic) {
		select {
		case m.diagCh <- lspDiagMsg{path: path, diags: diags}:
		default: // drop if the UI is backed up
		}
	})
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
