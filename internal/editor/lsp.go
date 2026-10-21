package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
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

// lspDefinitionMsg carries an async Go-to-Definition result back into the
// editor model. loc is nil when the server found no target.
type lspDefinitionMsg struct {
	path string
	loc  *lsp.Location
	err  error
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

// lspResolve is the config-aware variant of lspServerFor: the master [lsp]
// switch and per-language opt-out toggles can shut a server off even when the
// binary is installed. Empty command means no LSP for that extension.
func (m Model) lspResolve(ext string) (cmd string, args []string, langID string) {
	cmd, args, lang := lspServerFor(ext)
	if cmd == "" || lang == "" {
		return "", nil, ""
	}
	if !m.cfg.LSP.Enabled || m.cfg.LSP.Disabled[lang] {
		return "", nil, ""
	}
	return cmd, args, lang
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
	cmd, args, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
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
	path := t.path
	text := t.buf.Text()
	// didOpen is sent from a background goroutine because it waits for the
	// server's initialize handshake, and gopls must not receive the document
	// before it (that hangs the first completion). The spawn itself stays on
	// the UI thread so the editor keeps rendering while gopls loads.
	go debug.CapturePanicReport(func() {
		_ = c.EnsureOpened(path, lang, text)
	})
}

// lspCompletionCmd fires an async completion request for the current cursor and
// returns a command that delivers an lspCompletionMsg when results arrive.
func (m *Model) lspCompletionCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
	if cmd == "" || lang == "" {
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
		_ = c.EnsureOpened(path, lang, text)
		c.DidChange(path, text, 1)
		items, err := c.Completion(path, line, col)
		if err != nil {
			return lspCompletionMsg{path: path, err: err}
		}
		return lspCompletionMsg{path: path, items: items}
	}
}

// gotoDefinition fires an async Go-to-Definition request for the current
// cursor and returns a command that delivers an lspDefinitionMsg when the
// result arrives. It is used by F12.
func (m *Model) gotoDefinition() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}
	return m.gotoDefinitionAt(t.path, t.buf.CurLine(), t.buf.Col())
}

// gotoDefinitionAt is like gotoDefinition but targets an explicit buffer
// position; it backs both the F12 binding and Ctrl+Click navigation.
func (m *Model) gotoDefinitionAt(path string, line, col int) tea.Cmd {
	if path == "" {
		return nil
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(path)))
	if cmd == "" || lang == "" {
		return nil
	}
	if m.lspClient == nil {
		m.ensureLSP()
	}
	if m.lspClient == nil {
		return nil
	}
	text := m.cur().buf.Text()
	c := m.lspClient
	return func() tea.Msg {
		_ = c.EnsureOpened(path, lang, text)
		c.DidChange(path, text, 1)
		loc, err := c.Definition(path, line, col)
		return lspDefinitionMsg{path: path, loc: loc, err: err}
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

// lspMissingHintFor is the config-aware variant of lspMissingHint: it stays
// silent when the [lsp] switch is off or the file's language is disabled.
func (m Model) lspMissingHintFor(path string) string {
	cmd, _, _ := m.lspResolve(strings.ToLower(filepath.Ext(path)))
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

// lspStatusMsg sets the status-bar line describing the current file's LSP
// situation: an active server, a known-but-missing one (with install hint),
// or the reason it is shut off. It is the "LSP: Server Status" palette action.
func (m *Model) lspStatusMsg() {
	t := m.cur()
	if t == nil || t.path == "" {
		m.msg = m.t("msg.lsp_none")
		return
	}
	ext := strings.ToLower(filepath.Ext(t.path))
	cmd, _, lang := m.lspResolve(ext)
	if cmd == "" {
		if !m.cfg.LSP.Enabled {
			m.msg = m.t("msg.lsp_disabled_all")
			return
		}
		if lang != "" && m.cfg.LSP.Disabled[lang] {
			m.msg = fmt.Sprintf("%s", m.t("msg.lsp_disabled_lang", lang))
			return
		}
		m.msg = m.t("msg.lsp_none")
		return
	}
	if m.lspClient == nil {
		if hint := lspInstallFor(cmd); hint != "" {
			m.msg = m.t("msg.lsp_missing", cmd+" ("+hint+")")
		} else {
			m.msg = fmt.Sprintf("%s", cmd)
		}
		return
	}
	m.msg = m.t("msg.lsp_active", cmd)
}

// restartLSP tears down the current language server (if any) so the next
// completion or go-to-definition lazily spawns a fresh one. Useful after a
// server update or a wedged gopls process.
func (m *Model) restartLSP() {
	if m.lspClient != nil {
		_ = m.lspClient.Close()
		m.lspClient = nil
		m.msg = m.t("msg.lsp_restarted")
		return
	}
	m.msg = m.t("msg.lsp_none")
}

// ---------- rename + code actions ----------

// lspActionsVisible is the top of the quick-fix list shown at once.
const lspActionsVisible = 8

// lspRenameMsg carries the async rename result: the workspace edit is nil when
// the server said there is nothing to rename (or the request failed with
// err).
type lspRenameMsg struct {
	edit *lsp.WorkspaceEdit
	err  error
}

// lspActionsMsg carries the code actions offered at a position.
type lspActionsMsg struct {
	items []lsp.CodeAction
	err   error
}

// wordRunesAt returns the [start, end) word span around col on line: the word
// the caret sits inside, or — at its very end or on the space after it — the
// word just left of it. Empty span when the caret is not next to any word
// (it becomes Rename's input text).
func wordRunesAt(line []rune, col int) (int, int) {
	if col < 0 || col > len(line) {
		return 0, 0
	}
	start := col
	for start > 0 && isWordRune(line[start-1]) {
		start--
	}
	end := col
	for end < len(line) && isWordRune(line[end]) {
		end++
	}
	return start, end
}

// startLspRename opens the rename input pre-filled with the word under the
// cursor. It refuses to run when no language server covers the file.
func (m *Model) startLspRename() {
	t := m.cur()
	if t == nil || t.path == "" {
		return
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
	if cmd == "" {
		m.msg = m.t("msg.no_lsp")
		return
	}
	if m.lspClient == nil {
		m.ensureLSP()
	}
	if m.lspClient == nil {
		m.msg = m.t("msg.no_lsp")
		return
	}
	line := t.buf.LineAt(t.buf.CurLine())
	col := t.buf.Col()
	s, e := wordRunesAt(line, col)
	if e > s && !(col >= s && col <= e) {
		// Caret next to (not inside) a word: jump into it.
		t.buf.SetCursor(t.buf.CurLine(), s)
	}
	m.lspRenameIn = append([]rune(nil), line[s:e]...)
	m.lspRenameOpen = true
	_ = lang
}

// startLspRenameCmd sends the rename request off the UI thread: gopls may
// take its time verifying the new name.
func (m *Model) startLspRenameCmd() tea.Cmd {
	name := strings.TrimSpace(string(m.lspRenameIn))
	if name == "" {
		return nil
	}
	m.lspRenameOpen = false
	t := m.cur()
	if t == nil || m.lspClient == nil {
		return nil
	}
	c := m.lspClient
	path := t.path
	line, col := t.buf.CurLine(), t.buf.Col()
	return func() tea.Msg {
		edit, err := c.Rename(path, line, col, name)
		return lspRenameMsg{edit: edit, err: err}
	}
}

func (m *Model) handleLspRenameMsg(msg lspRenameMsg) {
	if msg.err != nil {
		m.msg = m.t("msg.rename_err", msg.err.Error())
		return
	}
	n := m.applyWorkspaceEdit(msg.edit)
	if n == 0 {
		m.msg = m.t("msg.no_rename")
		return
	}
	m.msg = m.t("msg.rename_ok", n)
}

// applyWorkspaceEdit applies the edits of a WorkspaceEdit across files: open
// tabs are edited in-buffer (staying dirty, and the server is notified via
// didChange), paths without a tab go straight to disk. Returns the number of
// files touched. Edits carry LSP UTF-16 columns, converted per line here.
func (m *Model) applyWorkspaceEdit(edit *lsp.WorkspaceEdit) int {
	if edit == nil {
		return 0
	}
	n := 0
	for path, edits := range edit.Files {
		sorted := lsp.SortedEdits(edits)
		applied := false
		for i := range m.tabs {
			if m.tabs[i].path != path {
				continue
			}
			buf := m.tabs[i].buf
			for _, e := range sorted {
				if e.StartLine < 0 || e.EndLine >= buf.LineCount() {
					continue
				}
				sc := lsp.ColToRune(buf.LineAt(e.StartLine), e.StartCol)
				ec := lsp.ColToRune(buf.LineAt(e.EndLine), e.EndCol)
				buf.ReplaceSpan(e.StartLine, sc, e.EndLine, ec, e.NewText)
			}
			m.lspClient.DidChange(path, buf.Text(), 0)
			applied = true
			break
		}
		if applied {
			n++
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue // unreadable: skip quietly, rename stays partial
		}
		text := lsp.ApplyEditsToText(string(data), sorted)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			continue
		}
		n++
	}
	return n
}

// startLspActionsCmd asks the server for code actions at the cursor.
func (m *Model) startLspActionsCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, _ := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
	if cmd == "" {
		m.msg = m.t("msg.no_lsp")
		return nil
	}
	if m.lspClient == nil {
		m.ensureLSP()
	}
	if m.lspClient == nil {
		m.msg = m.t("msg.no_lsp")
		return nil
	}
	// Make sure the server sees the current buffer text: quick fixes are all
	// about the diagnostics of exactly what is on screen.
	_, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
	c := m.lspClient
	path := t.path
	line, col := t.buf.CurLine(), t.buf.Col()
	text := t.buf.Text()
	return func() tea.Msg {
		_ = c.EnsureOpened(path, lang, text)
		items, err := c.CodeActions(path, line, col)
		return lspActionsMsg{items: items, err: err}
	}
}

func (m *Model) handleLspActionsMsg(msg lspActionsMsg) {
	if msg.err != nil {
		m.msg = "code actions: " + msg.err.Error()
		return
	}
	if len(msg.items) == 0 {
		m.msg = m.t("msg.no_actions")
		return
	}
	m.lspActions = msg.items
	m.lspActionsSel = 0
	m.lspActionsOff = 0
	m.lspActionsOpen = true
}

// startLspActions fires the request; used by the binding and the palette.
func (m *Model) startLspActions() tea.Cmd {
	return m.startLspActionsCmd()
}

func (m *Model) handleLspRename(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.lspRenameOpen = false
		m.msg = ""
	case "enter":
		return m.startLspRenameCmd()
	case "backspace":
		if n := len(m.lspRenameIn); n > 0 {
			m.lspRenameIn = m.lspRenameIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.lspRenameIn = append(m.lspRenameIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// handleLspActions drives the quick-fix list overlay: plain cursor keys,
// Enter applies the edit of the selected action, Esc closes.
func (m *Model) handleLspActions(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.lspActionsOpen = false
		m.msg = ""
	case "up", "k", "ctrl+p":
		if n := len(m.lspActions); n > 0 {
			m.jumpLspActionsSel((m.lspActionsSel - 1 + n) % n)
		}
	case "down", "j", "ctrl+n":
		if n := len(m.lspActions); n > 0 {
			m.jumpLspActionsSel((m.lspActionsSel + 1) % n)
		}
	case "pgup":
		m.jumpLspActionsSel(m.lspActionsSel - lspActionsVisible)
	case "pgdown":
		m.jumpLspActionsSel(m.lspActionsSel + lspActionsVisible)
	case "enter":
		if i := m.lspActionsSel; len(m.lspActions) > 0 && i < len(m.lspActions) {
			m.lspActionsOpen = false
			act := m.lspActions[i]
			if act.Edit == nil {
				m.msg = m.t("msg.action_no_edit")
			} else if n := m.applyWorkspaceEdit(act.Edit); n == 0 {
				m.msg = m.t("msg.applied_count", 0)
			} else {
				m.msg = m.t("msg.applied_count", n)
			}
		}
	default:
		// Typing closes to the editor: the list is answer-only.
	}
	return nil
}

// jumpLspActionsSel keeps the quick-fix list window scrolled so the selection
// stays visible.
func (m *Model) jumpLspActionsSel(sel int) {
	n := len(m.lspActions)
	if sel < 0 {
		sel = 0
	} else if sel >= n {
		sel = n - 1
	}
	m.lspActionsSel = sel
	if n <= lspActionsVisible {
		m.lspActionsOff = 0
		return
	}
	if m.lspActionsSel < m.lspActionsOff {
		m.lspActionsOff = m.lspActionsSel
	}
	if m.lspActionsSel >= m.lspActionsOff+lspActionsVisible {
		m.lspActionsOff = m.lspActionsSel - lspActionsVisible + 1
	}
	if m.lspActionsOff > n-lspActionsVisible {
		m.lspActionsOff = n - lspActionsVisible
	}
}
