package editor

import (
	"strings"

	"dmed/internal/buffer"
	"dmed/internal/config"
)

// fileOptsOf returns the per-file settings that apply to one tab: what its
// .editorconfig says, with dmed's own answers filling in whatever the file left
// unset.
//
// The lookup walks directories, so it is cached on the tab the same way the
// language tag is (tab.lang) and for the same reason: the view asks on every
// frame. The cache key is the path rather than a "known" flag because saving
// under a new name moves the file into a different directory, and the settings
// of the old one must not follow it.
func (m Model) fileOptsOf(t *tab) config.FileOpts {
	def := m.cfg.FileOpts()
	if t == nil || t.path == "" {
		return def
	}
	if t.ecPath != t.path {
		t.ecOpts = config.LoadEditorConfig(m.baseDir(), t.path, def)
		t.ecPath = t.path
	}
	return t.ecOpts
}

// tabStop is how many columns a tab character occupies in this tab, which is
// what every column calculation — expansion, wrapping, folds, the click to a
// column — has to agree on.
func (m Model) tabStop(t *tab) int {
	return m.fileOptsOf(t).TabWidth
}

// indentUnit is the string one Tab press inserts in this tab: a tab character,
// or the indentation level in spaces. The buffer takes a string for exactly
// this reason, so the difference never reaches internal/buffer.
func (m Model) indentUnit(t *tab) string {
	return m.fileOptsOf(t).TypedUnit()
}

// textForDisk renders the buffer as the bytes this file is saved with: the
// .editorconfig save policy first, then the line endings the file already uses.
// Ctrl+S and Save As both run it, so renaming a file cannot quietly drop a rule
// saving it in place obeys.
func (m *Model) textForDisk(t *tab) string {
	opts := m.fileOptsOf(t)
	if opts.TrimTrailingWS {
		// The very method behind the Alt+Shift+T command. A save that trims is
		// one edit with one undo step: that is what the file asked for, and an
		// undo that puts the spaces back afterwards is the honest answer.
		t.buf.TrimTrailing(0, t.buf.LineCount()-1)
	}
	text := t.buf.Text()
	if !opts.InsertFinalNewline {
		// The buffer always carries its final newline; whether the file on disk
		// does is a property of the bytes being written, so it is decided here
		// and not in the document.
		text = stripFinalNewline(text)
	}
	if t.lineEnding == "crlf" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// dropFileOptsCaches forgets the cached settings of every tab. A changed
// .editorconfig is the reason: the next read walks again. The wrap and fold
// caches need no explicit invalidation, because both are keyed on the tab stop
// they were built with.
func (m *Model) dropFileOptsCaches() {
	for i := range m.tabs {
		m.tabs[i].ecPath = ""
	}
}

// fileFinisher is the save policy as a function of the path alone, for the
// callers that have no tab and sometimes no model: the chat's tools, a
// background sub-agent, Replace in Project. It snapshots the root and the
// defaults instead of holding a *Model, because a tool body must never read
// editor state from a worker goroutine.
func fileFinisher(root string, def config.FileOpts) func(path, content string) string {
	return func(path, content string) string {
		return applyFileSave(config.LoadEditorConfig(root, path, def), content)
	}
}

// applyFileSave is what a file of this policy looks like on disk. Two rules
// only, and that boundary is deliberate: converting the indent unit would
// rewrite every line of a whole-file proposal, including the lines the model did
// not touch, so the diff a human approved would no longer be the change that
// lands. Trailing whitespace and the end of the file cannot do that — and the
// buffer's own TrimTrailing answers "what is trailing whitespace" here rather
// than a second time, in a version that could drift from the human's save path.
func applyFileSave(o config.FileOpts, text string) string {
	if o.TrimTrailingWS {
		b := buffer.Load(text)
		b.TrimTrailing(0, b.LineCount()-1)
		text = b.Text()
	}
	if o.InsertFinalNewline {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text
	}
	return stripFinalNewline(text)
}

// stripFinalNewline drops the end of the file's last line whatever the line
// endings are. A tool reads a file as the bytes on disk, so a CRLF file arrives
// ending in "\r\n" and trimming only the "\n" would leave a bare carriage
// return as the final byte. The buffer never shows that face — it stores
// newlines as "\n" and re-writes CRLF on the way out — but it is the same rule
// either way, so it is one function.
func stripFinalNewline(text string) string {
	return strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
}
