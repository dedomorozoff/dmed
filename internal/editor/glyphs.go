package editor

import (
	"os"
	"runtime"

	"dmed/internal/terminal"
)

// glyphSet holds every non-ASCII token the TUI renders, in one table. The
// unicode set is the default look; asciiGlyphs trades box drawing, arrows and
// symbols for plain ASCII so legacy consoles and dumb terminals still render a
// readable UI. Keeping both sets single-width means hit-testing, padding and
// lipgloss.Width arithmetic stay identical in either mode.
type glyphSet struct {
	vline      string // │         continuous vertical rule (splits, diff view)
	hline      string // ─         horizontal rule (pane separator, git header)
	tee        string // ├         tree branch to a deeper level
	corner     string // └         tree branch to the last child
	expand     string // ▸         collapsed folder / variable
	collapse   string // ▾         expanded folder
	stop       string // ▶         execution point / reviewing task
	breakpt    string // ●         verified breakpoint / current branch
	breakptO   string // ○         rejected breakpoint
	bookmark   string // ◆         bookmark marker
	diagInfo   string // •         info-severity gutter marker
	check      string // ✓         done / connected
	cross      string // ×         cancelled / failed
	ellipsis   string // …         truncation indicator
	dotSep     string // ·         separator dot (blame, status headers)
	progFull   string // █         progress bar filled cell
	progEmpty  string // ·         progress bar empty cell
	iconTree   string // ▤         status-bar tree toggle
	iconGit    string // ⎇         status-bar git toggle
	iconChat   string // ✦         status-bar chat toggle
	iconDebug  string // ◉         status-bar debug toggle
	iconTerm   string // ❯         status-bar terminal toggle
	iconSplitV string // ◫ / V    tab-bar vertical-split toggle
	iconSplitH string // ▤ / H    tab-bar horizontal-split toggle
	mask       string // • / *     obscured secret (API key fields)
	iconTool   string // ⛏ / >     tool-call card marker in chat
}

var unicodeGlyphs = glyphSet{
	vline:      "│",
	hline:      "─",
	tee:        "├",
	corner:     "└",
	expand:     "▸",
	collapse:   "▾",
	stop:       "▶",
	breakpt:    "●",
	breakptO:   "○",
	bookmark:   "◆",
	diagInfo:   "•",
	check:      "✓",
	cross:      "×",
	ellipsis:   "…",
	dotSep:     "·",
	progFull:   "█",
	progEmpty:  "·",
	iconTree:   "▤",
	iconGit:    "⎇",
	iconChat:   "✦",
	iconDebug:  "◉",
	iconTerm:   "❯",
	iconSplitV: "◫",
	iconSplitH: "▤",
	mask:       "•",
	iconTool:   "⛏",
}

var asciiGlyphs = glyphSet{
	vline:      "|",
	hline:      "-",
	tee:        "|",
	corner:     "\\",
	expand:     ">",
	collapse:   "v",
	stop:       ">",
	breakpt:    "*",
	breakptO:   "o",
	bookmark:   "*",
	diagInfo:   ".",
	check:      "v",
	cross:      "x",
	ellipsis:   "~",
	dotSep:     ".",
	progFull:   "#",
	progEmpty:  ".",
	iconTree:   "T",
	iconGit:    "G",
	iconChat:   "C",
	iconDebug:  "D",
	iconTerm:   ">",
	iconSplitV: "V",
	iconSplitH: "H",
	mask:       "*",
	iconTool:   ">",
}

// ApplyTerminalCompat switches the render glyph set to ASCII when the running
// terminal cannot be trusted with non-ASCII output (legacy Windows consoles,
// dumb or unknown unix terminals), honoring an explicit [ui] ascii override.
// Modern terminals keep the full Unicode look untouched.
func (m *Model) ApplyTerminalCompat() {
	comp := terminal.DetectCompat(m.cfg.UI.Ascii, terminal.EnvMap(os.Environ()), runtime.GOOS)
	if comp.Ascii {
		m.g = asciiGlyphs
	}
}

// icon returns the status-bar glyph for a panel toggle action.
func (g glyphSet) icon(a statusAction) string {
	switch a {
	case actTree:
		return g.iconTree
	case actGit:
		return g.iconGit
	case actChat:
		return g.iconChat
	case actDebug:
		return g.iconDebug
	case actTerm:
		return g.iconTerm
	}
	return "?"
}

// splitIcon returns the tab-bar glyph for a split action.
func (g glyphSet) splitIcon(a statusAction) string {
	switch a {
	case actSplitV:
		return g.iconSplitV
	case actSplitH:
		return g.iconSplitH
	}
	return "?"
}
