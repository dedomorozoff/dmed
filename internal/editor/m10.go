package editor

import (
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/lsp"
)

// M10 — LSP to the end: diagnostics navigation, hover, references, outline.
//
// The diagnostics pool already reaches the model (lspDiagMsg → m.diags); what
// was missing is walking it. The walk is per active tab, sorted by position so
// F8 reads the file top to bottom; the cursor is remembered across presses and
// wraps, and a jump records a jump-list entry — landing on an error is exactly
// the "cannot walk back by hand" case.
//
// Hover, references and outline are async requests like definition and rename:
// a command fires, the reply lands as a message, the panel opens on the result.
// References reuse the code-actions panel mechanics; the outline is its own
// list but the same shape, and both jump through focusOrOpen + pushJump.

// ---------- diagnostics navigation ----------

// sortedDiags returns the active tab's diagnostics sorted by line, then
// column. The pool is per file; a file the server has not reported yet holds
// none.
func (m *Model) sortedDiags() []lsp.Diagnostic {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	diags, ok := m.diags[t.path]
	if !ok || len(diags) == 0 {
		return nil
	}
	out := append([]lsp.Diagnostic(nil), diags...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// nextDiagnostic walks the active tab's diagnostics: dir is +1 for F8 and -1
// for Shift+F8. The walk wraps, and a caret already parked on a diagnostic is
// counted, so the first press with the cursor on line N shows line N's hint
// rather than skipping to the next.
func (m *Model) nextDiagnostic(dir int) {
	diags := m.sortedDiags()
	if len(diags) == 0 {
		m.msg = m.t("msg.no_diagnostics")
		m.diagCursor = 0
		return
	}
	cur := m.cur()
	line, col := cur.buf.CurLine(), cur.buf.Col()
	// Find the first diagnostic after (or at, first press) the caret.
	idx := -1
	if dir > 0 {
		for i, d := range diags {
			if d.Line > line || (d.Line == line && d.Col >= col && m.diagCursor != i) {
				idx = i
				break
			}
		}
		if idx == -1 {
			idx = 0 // wrap
		}
	} else {
		for i := len(diags) - 1; i >= 0; i-- {
			d := diags[i]
			if d.Line < line || (d.Line == line && d.Col <= col && m.diagCursor != i) {
				idx = i
				break
			}
		}
		if idx == -1 {
			idx = len(diags) - 1 // wrap
		}
	}
	m.diagCursor = idx
	d := diags[idx]
	m.pushJump()
	cur.buf.SetCursor(d.Line, d.Col)
	cur.buf.Deselect()
	m.clampScroll()
	sev := m.t("diag.info")
	switch d.Severity {
	case 1:
		sev = m.t("diag.error")
	case 2:
		sev = m.t("diag.warning")
	}
	m.msg = m.t("msg.diag_nav", idx+1, len(diags), sev, d.Message)
}

// diagHintFor returns the message of the diagnostic the cursor sits on, or ""
// — the inline hint the status line shows while navigating.
func (m Model) diagHintFor(line int) string {
	diags, ok := m.diags[func() string {
		if t := m.cur(); t != nil {
			return t.path
		}
		return ""
	}()]
	if !ok {
		return ""
	}
	for _, d := range diags {
		if d.Line == line {
			return d.Message
		}
	}
	return ""
}

// ---------- hover ----------

// lspHoverMsg carries the async hover reply; text is nil when the server has
// nothing to say about the symbol under the cursor.
type lspHoverMsg struct {
	lines []string
}

// startHoverCmd asks the server for hover info at the cursor and delivers the
// rendered text (markdown fences and emphasis stripped — the overlay draws
// plain lines).
func (m *Model) startHoverCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
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
	c := m.lspClient
	path := t.path
	line, col := t.buf.CurLine(), t.buf.Col()
	text := t.buf.Text()
	return func() tea.Msg {
		_ = c.EnsureOpened(path, lang, text)
		c.DidChange(path, text, 1)
		h, err := c.Hover(path, line, col)
		if err != nil {
			return lspHoverMsg{}
		}
		if h == nil {
			return lspHoverMsg{}
		}
		return lspHoverMsg{lines: hoverLines(h.Contents)}
	}
}

// hoverLines turns the server's markdown into plain overlay lines: code
// fences lose their markers, backtick runs unwrap, double spaces from the
// markdown indentation go away. A line longer than the pane wraps naturally
// because the overlay clips at the pane width per row.
func hoverLines(md string) []string {
	var out []string
	inFence := false
	for _, ln := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		ln = strings.TrimRight(ln, " \t")
		if !inFence {
			ln = strings.ReplaceAll(ln, "`", "")
			ln = strings.TrimRight(ln, " ")
		}
		if ln == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, ln)
	}
	// Trim the trailing blank run.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func (m *Model) handleHover(s string, _ tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("hover", s) {
	case "hover.close":
		m.hoverOpen = false
		m.msg = ""
	}
	return nil
}

// ---------- references ----------

// lspRefsMsg carries the async references reply.
type lspRefsMsg struct {
	refs []lsp.Location
	err  error
}

// startRefsCmd asks for the references of the symbol under the cursor
// (declaration included: "Find Usages" reads better with the definition in
// the list).
func (m *Model) startRefsCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
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
	c := m.lspClient
	path := t.path
	line, col := t.buf.CurLine(), t.buf.Col()
	text := t.buf.Text()
	return func() tea.Msg {
		_ = c.EnsureOpened(path, lang, text)
		c.DidChange(path, text, 1)
		refs, err := c.References(path, line, col, true)
		return lspRefsMsg{refs: refs, err: err}
	}
}

func (m *Model) handleRefsMsg(msg lspRefsMsg) {
	if msg.err != nil {
		m.msg = m.t("msg.refs_err", msg.err.Error())
		return
	}
	if len(msg.refs) == 0 {
		m.msg = m.t("msg.no_refs")
		return
	}
	m.refs = msg.refs
	m.refsSel = 0
	m.refsOff = 0
	m.refsOpen = true
}

// handleRefs drives the references panel: the code-actions mechanics with
// Enter jumping to the hit (pushJump first — a server-found usage is not a
// place the user can retrace).
func (m *Model) handleRefs(s string, _ tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("refs", s) {
	case "refs.close":
		m.refsOpen = false
		m.msg = ""
	case "refs.up":
		if n := len(m.refs); n > 0 {
			m.refsSel = (m.refsSel - 1 + n) % n
			m.clampRefsWindow()
		}
	case "refs.down":
		if n := len(m.refs); n > 0 {
			m.refsSel = (m.refsSel + 1) % n
			m.clampRefsWindow()
		}
	case "refs.open":
		if i := m.refsSel; len(m.refs) > 0 && i < len(m.refs) {
			loc := m.refs[i]
			m.refsOpen = false
			m.pushJump()
			m.focusOrOpen(loc.Path)
			if t := m.cur(); t != nil {
				t.buf.SetCursor(loc.Line, loc.Col)
				t.buf.Deselect()
			}
			m.clampScroll()
		}
	}
	return nil
}

// clampRefsWindow keeps the selected reference visible, the way
// jumpLspActionsSel does for the quick fixes.
func (m *Model) clampRefsWindow() {
	const visible = 10
	n := len(m.refs)
	if n <= visible {
		m.refsOff = 0
		return
	}
	if m.refsSel < m.refsOff {
		m.refsOff = m.refsSel
	}
	if m.refsSel >= m.refsOff+visible {
		m.refsOff = m.refsSel - visible + 1
	}
	if m.refsOff > n-visible {
		m.refsOff = n - visible
	}
}

// ---------- outline ----------

// lspOutlineMsg carries the async document-symbol reply.
type lspOutlineMsg struct {
	syms []lsp.DocumentSymbol
	err  error
}

// startOutlineCmd asks for the document symbols of the active tab.
func (m *Model) startOutlineCmd() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	cmd, _, lang := m.lspResolve(strings.ToLower(filepath.Ext(t.path)))
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
	c := m.lspClient
	path := t.path
	text := t.buf.Text()
	return func() tea.Msg {
		_ = c.EnsureOpened(path, lang, text)
		syms, err := c.DocumentSymbols(path)
		return lspOutlineMsg{syms: syms, err: err}
	}
}

func (m *Model) handleOutlineMsg(msg lspOutlineMsg) {
	if msg.err != nil {
		m.msg = m.t("msg.outline_err", msg.err.Error())
		return
	}
	if len(msg.syms) == 0 {
		m.msg = m.t("msg.no_symbols")
		return
	}
	m.outline = msg.syms
	m.outlineSel = 0
	m.outlineOff = 0
	m.outlineOpen = true
}

// handleOutline drives the symbol list; Enter jumps to the symbol's line.
func (m *Model) handleOutline(s string, _ tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("outline", s) {
	case "outline.close":
		m.outlineOpen = false
		m.msg = ""
	case "outline.up":
		if n := len(m.outline); n > 0 {
			m.outlineSel = (m.outlineSel - 1 + n) % n
			m.clampOutlineWindow()
		}
	case "outline.down":
		if n := len(m.outline); n > 0 {
			m.outlineSel = (m.outlineSel + 1) % n
			m.clampOutlineWindow()
		}
	case "outline.open":
		if i := m.outlineSel; len(m.outline) > 0 && i < len(m.outline) {
			sym := m.outline[i]
			m.outlineOpen = false
			m.pushJump()
			if t := m.cur(); t != nil {
				t.buf.SetCursor(sym.Line, sym.Col)
				t.buf.Deselect()
			}
			m.clampScroll()
		}
	}
	return nil
}

func (m *Model) clampOutlineWindow() {
	const visible = 12
	n := len(m.outline)
	if n <= visible {
		m.outlineOff = 0
		return
	}
	if m.outlineSel < m.outlineOff {
		m.outlineOff = m.outlineSel
	}
	if m.outlineSel >= m.outlineOff+visible {
		m.outlineOff = m.outlineSel - visible + 1
	}
	if m.outlineOff > n-visible {
		m.outlineOff = n - visible
	}
}
