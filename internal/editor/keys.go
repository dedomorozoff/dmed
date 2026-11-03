package editor

import (
	"os"
	"path/filepath"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"dmed/internal/buffer"
)

func normalizeKey(r rune) rune {
	if en, ok := ruToEn[r]; ok {
		return unicode.ToLower(en)
	}
	if en, ok := ruToEn[unicode.ToLower(r)]; ok {
		return unicode.ToLower(en)
	}
	return r
}

// physicalLetter reports the Latin letter of the physical key a press arrived
// with (BaseCode/ShiftedCode — the Windows Console API and the Kitty protocol
// both fill them). ruToEn only knows the Russian ЙЦУКЕН layout; for any other
// one the physical key is the only layout-independent name left, so keyOf
// falls back to it rather than handing the binding tables a non-ASCII name
// nothing can match.
func physicalLetter(msg tea.KeyPressMsg) (rune, bool) {
	for _, r := range []rune{msg.BaseCode, msg.ShiftedCode} {
		switch {
		case r >= 'a' && r <= 'z':
			return r, true
		case r >= 'A' && r <= 'Z':
			return unicode.ToLower(r), true
		}
	}
	return 0, false
}

// c0Special reports whether a C0 control code is really one of the control
// keys that ultraviolet delivers as a special key code (Tab, Enter, Escape)
// rather than a Ctrl+letter chord that needs re-encoding.
func c0Special(r rune) bool {
	return r == '\t' || r == tea.KeyEnter || r == tea.KeyEsc
}

// controlByteKey converts a raw control byte (0x01–0x1f, NUL = Ctrl+Space)
// into the matching ctrl+key message, preserving any other modifiers reported
// by the terminal (notably Alt for Ctrl+Alt combos sent as ESC + a control
// byte on some terminals).
func controlByteKey(r rune, mod tea.KeyMod) tea.KeyPressMsg {
	if r == 0 {
		return tea.KeyPressMsg{Code: tea.KeySpace, Mod: mod | tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: r + 96, Mod: mod | tea.ModCtrl}
}

// isBareShift reports whether a key press event represents a bare Shift key
// (without other character codes).
func isBareShift(msg tea.KeyPressMsg, s string) bool {
	if msg.Code == tea.KeyLeftShift || msg.Code == tea.KeyRightShift {
		return true
	}
	if s == "leftshift" || s == "rightshift" || s == "shift" {
		return true
	}
	if msg.Code == 0 && msg.Text == "" && msg.Mod == tea.ModShift {
		return true
	}
	return false
}

// keyOf turns a raw key press into the pair every binding lookup works from:
// the message text-input handlers should see (with the original typed text
// restored) and the layout-independent key name the binding tables are keyed
// by. It is the single place that knows how a terminal chord differs from the
// chord the editor binds.
func keyOf(msg tea.KeyPressMsg) (tea.KeyPressMsg, string) {
	// Some terminal stacks send bare control bytes (Ctrl+O as 0x0f, Ctrl+C as
	// 0x03, Ctrl+Space as NUL, bare Ctrl as NUL). Normalize them into proper
	// ctrl+key messages so keybindings work and stray control bytes never reach
	// the buffer. Keep any modifiers already reported (e.g. Ctrl+Alt+letter
	// arrives as ESC + a control byte on some terminals) instead of dropping
	// them as before.
	if len(msg.Text) == 1 {
		if r := rune(msg.Text[0]); r < 32 && !c0Special(r) {
			msg = controlByteKey(r, msg.Mod)
		}
	} else if msg.Text == "" && msg.Code < 32 && !c0Special(msg.Code) {
		if !(msg.Code == 0 && msg.Mod == tea.ModShift) {
			msg = controlByteKey(msg.Code, msg.Mod)
		}
	}
	// Normalize Russian ЙЦУКЕН → English QWERTY for layout-independent keys.
	// Use []rune so multi-byte UTF-8 (Cyrillic) inputs don't leave trailing NULs.
	origText := msg.Text
	if src := []rune(msg.Text); len(src) > 0 {
		nr := make([]rune, len(src))
		for i, r := range src {
			nr[i] = normalizeKey(r)
		}
		if nr[0] >= 0x80 {
			if phys, ok := physicalLetter(msg); ok {
				nr[0] = phys
			}
		}
		// BaseCode/ShiftedCode stay out of here on purpose: Keystroke() prefers
		// the physical fields, so carrying them would let the physical US key
		// override a chord the layout already translated correctly. They only
		// matter as the fallback above, for a layout ruToEn cannot translate.
		msg = tea.KeyPressMsg{Code: nr[0], Text: string(nr), Mod: msg.Mod}
	} else if msg.Code != 0 {
		// Key combos (Ctrl/Alt+letter) arrive with empty Text on many
		// terminals; map their Cyrillic code to the physical QWERTY key too so
		// "ctrl+в" (Russian layout) still matches "ctrl+d".
		msg.Code = normalizeKey(msg.Code)
		if msg.Code >= 0x80 {
			if phys, ok := physicalLetter(msg); ok {
				msg.Code = phys
			}
		}
	}
	s := msg.String()
	// String() prints Text verbatim, and terminals put the raw control byte
	// there for Enter/Esc/Tab/Backspace ("\r", "\x1b", "\t", "\x7f") — names no
	// binding can ever match. Name those from the key code instead.
	if len(s) == 1 && (s[0] < 32 || s[0] == 0x7f) {
		s = msg.Keystroke()
	}
	// Some terminal stacks bundle the letter as Text even for Ctrl/Alt chords
	// (notably the Windows Console API on non-US keyboard layouts, where a
	// pressed Ctrl+<physical key> arrives with the Cyrillic Code/Text). String()
	// returns that Text verbatim and drops the modifier, so such a chord would
	// match the plain letter — and fall through to the buffer, typing "g" after
	// Ctrl+G. Rebuild the key name from the modifiers instead (uv's Keystroke)
	// whenever Ctrl or Alt is involved; Shift-only events keep their Text form
	// so the bare "G" ⇄ "g" top/bottom distinction is preserved.
	if (msg.Mod&(tea.ModCtrl|tea.ModAlt)) != 0 && msg.Text != "" {
		s = msg.Keystroke()
	}
	// Restore original text so text-input handlers (chat, search, prompt,
	// etc.) receive the actual typed characters instead of the normalized
	// English equivalents used only for keybinding matching.
	msg.Text = origText
	return msg, s
}

// binding is one entry of a key table: it runs the action and reports whether
// the key is consumed. A binding that returns handled == false lets the key
// fall through to the next stage, which is how a chord that means different
// things in different states (F6 is step-over in the debug panel and a split
// outside it) stays expressible in a table.
type binding func(m *Model, msg tea.KeyPressMsg) (tea.Cmd, bool)

// keyAct names what a binding *does*, independently of the chord that triggers
// it. The chord is the map key and the name is inside the value, so an action
// has one identity shared by every chord bound to it: an alias in the table
// (f1 and ctrl+e are help.toggle) and an alias in a config file address the
// same thing, and the help screen, the palette and [keys] all read that one name
// instead of each keeping its own copy of the chord.
type keyAct string

// bindFlags carries what a binding needs beyond the function: whether the chord
// is refused to a rebind.
type bindFlags uint8

const (
	// protected chords are the ones other machinery depends on: the escape
	// hatch, the exit, the letters a mouse-free user cannot click for, and the
	// y/n that the agent review is documented to answer. They show up in help
	// and refuse to be rebound.
	protected bindFlags = 1 << iota
)

// bindingDef is one table entry: the action it runs, the closure that runs it
// and the flags describing it.
type bindingDef struct {
	act   keyAct
	fn    binding
	flags bindFlags
}

// Actions, grouped by the subsystem that owns them. The name is the id a
// [keys] section writes, so it is data, not a Go identifier: lower case,
// dotted, and stable across a rename of the handler.
const (
	// ai
	actAiFix          keyAct = "ai.fix"
	actAiGhostDismiss keyAct = "ai.ghost-dismiss"
	actAiInline       keyAct = "ai.inline"

	// app
	actAppQuit keyAct = "app.quit"

	// bookmark
	actBookmarkNext   keyAct = "bookmark.next"
	actBookmarkPrev   keyAct = "bookmark.prev"
	actBookmarkToggle keyAct = "bookmark.toggle"

	// complete
	actCompleteTrigger keyAct = "complete.trigger"

	// cursor
	actCursorClearExtra     keyAct = "cursor.clear-extra"
	actCursorNextOccurrence keyAct = "cursor.next-occurrence"

	// debug
	actDebugBreakpoint keyAct = "debug.breakpoint"
	actDebugPanel      keyAct = "debug.panel"
	actDebugRun        keyAct = "debug.run"
	actDebugStepIn     keyAct = "debug.step-in"
	actDebugStepOut    keyAct = "debug.step-out"
	actDebugStepOver   keyAct = "debug.step-over"
	actDebugStop       keyAct = "debug.stop"

	// delimiter
	actDelimiterJump         keyAct = "delimiter.jump"
	actDelimiterSelectInside keyAct = "delimiter.select-inside"

	// diagnostic
	actDiagnosticNext keyAct = "diagnostic.next"
	actDiagnosticPrev keyAct = "diagnostic.prev"

	// edit
	actEditAlignIndent   keyAct = "edit.align-indent"
	actEditBackspace     keyAct = "edit.backspace"
	actEditCopy          keyAct = "edit.copy"
	actEditCut           keyAct = "edit.cut"
	actEditDelete        keyAct = "edit.delete"
	actEditDeleteLine    keyAct = "edit.delete-line"
	actEditDuplicateLine keyAct = "edit.duplicate-line"
	actEditIndent        keyAct = "edit.indent"
	actEditLastChange    keyAct = "edit.last-change"
	actEditLowercase     keyAct = "edit.lowercase"
	actEditNewline       keyAct = "edit.newline"
	actEditNumberLines   keyAct = "edit.number-lines"
	actEditPaste         keyAct = "edit.paste"
	actEditRedo          keyAct = "edit.redo"
	actEditSortLines     keyAct = "edit.sort-lines"
	actEditToggleComment keyAct = "edit.toggle-comment"
	actEditTrimTrailing  keyAct = "edit.trim-trailing"
	actEditUndo          keyAct = "edit.undo"
	actEditUnindent      keyAct = "edit.unindent"
	actEditUppercase     keyAct = "edit.uppercase"

	// file
	actFileOpenPrompt keyAct = "file.open-prompt"
	actFileSave       keyAct = "file.save"

	// finder
	actFinderOpen keyAct = "finder.open"

	// fold
	actFoldClose keyAct = "fold.close"
	actFoldOpen  keyAct = "fold.open"

	// git
	actGitBlame    keyAct = "git.blame"
	actGitHunkNext keyAct = "git.hunk-next"
	actGitHunkPrev keyAct = "git.hunk-prev"

	// goto
	actGotoLine keyAct = "goto.line"

	// help
	actHelpToggle keyAct = "help.toggle"

	// jump
	actJumpBack    keyAct = "jump.back"
	actJumpForward keyAct = "jump.forward"

	// line
	actLineDuplicateUp keyAct = "line.duplicate-up"
	actLineMoveDown    keyAct = "line.move-down"
	actLineMoveUp      keyAct = "line.move-up"

	// lsp
	actLspActions    keyAct = "lsp.actions"
	actLspDefinition keyAct = "lsp.definition"
	actLspFormat     keyAct = "lsp.format"
	actLspHover      keyAct = "lsp.hover"
	actLspOutline    keyAct = "lsp.outline"
	actLspReferences keyAct = "lsp.references"
	actLspRename     keyAct = "lsp.rename"

	// merge
	actMergeBoth   keyAct = "merge.both"
	actMergeOurs   keyAct = "merge.ours"
	actMergeTheirs keyAct = "merge.theirs"

	// nav
	actNavDown      keyAct = "nav.down"
	actNavLeft      keyAct = "nav.left"
	actNavLineEnd   keyAct = "nav.line-end"
	actNavLineStart keyAct = "nav.line-start"
	actNavPageDown  keyAct = "nav.page-down"
	actNavPageUp    keyAct = "nav.page-up"
	actNavRight     keyAct = "nav.right"
	actNavUp        keyAct = "nav.up"

	// palette
	actPaletteEverywhere keyAct = "palette.everywhere"
	actPaletteOpen       keyAct = "palette.open"

	// pane
	actPaneClose      keyAct = "pane.close"
	actPaneFocusOther keyAct = "pane.focus-other"

	// project
	actProjectReplace keyAct = "project.replace"
	actProjectSearch  keyAct = "project.search"

	// replace
	actReplaceOpen keyAct = "replace.open"

	// search
	actSearchNext keyAct = "search.next"
	actSearchOpen keyAct = "search.open"
	actSearchPrev keyAct = "search.prev"

	// select
	actSelectCharLeft    keyAct = "select.char-left"
	actSelectCharRight   keyAct = "select.char-right"
	actSelectColumnDown  keyAct = "select.column-down"
	actSelectColumnLeft  keyAct = "select.column-left"
	actSelectColumnRight keyAct = "select.column-right"
	actSelectColumnUp    keyAct = "select.column-up"
	actSelectLineDown    keyAct = "select.line-down"
	actSelectLineEnd     keyAct = "select.line-end"
	actSelectLineStart   keyAct = "select.line-start"
	actSelectLineUp      keyAct = "select.line-up"

	// split
	actSplitHorizontal keyAct = "split.horizontal"
	actSplitVertical   keyAct = "split.vertical"

	// tab
	actTabClose keyAct = "tab.close"
	actTabJump  keyAct = "tab.jump"
	actTabMru   keyAct = "tab.mru"
	actTabNext  keyAct = "tab.next"
	actTabPrev  keyAct = "tab.prev"

	// view
	actViewAgent    keyAct = "view.agent"
	actViewChat     keyAct = "view.chat"
	actViewGit      keyAct = "view.git"
	actViewTerminal keyAct = "view.terminal"
	actViewTree     keyAct = "view.tree"
	actViewWordWrap keyAct = "view.word-wrap"
)

// globalBindings answer regardless of which pane or overlay has focus: the
// chords that must always reach the editor (quit, copy, panels, debugging).
var globalBindings = map[string]bindingDef{
	"ctrl+q": {actAppQuit, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.requestQuit(), true
	}, protected},
	"ctrl+c": {actEditCopy, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasSelection() {
			m.copySelection()
			return nil, true
		}
		// Ctrl+C never quits: an editor that dies on the key a terminal
		// uses to interrupt a program is a trap. Ctrl+Q is the way out.
		m.msg = m.t("msg.quit_hint")
		return nil, true
	}, 0},
	"ctrl+w": {actTabClose, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.closeActiveTab(), true
	}, 0},
	"ctrl+x": {actEditCut, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasSelection() {
			m.cutSelection()
			return nil, true
		}
		return m.closeActiveTab(), true
	}, 0},
	"ctrl+shift+p": {actPaletteEverywhere, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Search everywhere: commands, files and settings. The double Shift
		// does the same, but most terminals never report a bare modifier
		// press, and on legacy terminals Ctrl+Shift+P collapses into the
		// Ctrl+P control byte — Alt+P (ESC+p) stays distinguishable even
		// there, so this is the binding that always works.
		m.startSearchEverywhere()
		return nil, true
	}, 0},
	"alt+p": {actPaletteEverywhere, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startSearchEverywhere()
		return nil, true
	}, 0},
	"ctrl+p": {actPaletteOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPalette()
		return nil, true
	}, 0},
	"f2": {actPaletteOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPalette()
		return nil, true
	}, 0},
	"ctrl+t": {actFileOpenPrompt, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPrompt()
		return nil, true
	}, 0},
	"alt+t": {actViewTerminal, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleTerminal(), true
	}, 0},
	"alt+a": {actViewChat, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleChat()
		return nil, true
	}, 0},
	"alt+l": {actViewAgent, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleAgentPanel(), true
	}, 0},
	"alt+i": {actAiInline, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startInlineRequest()
		return nil, true
	}, 0},
	"alt+f": {actAiFix, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startFixRequest()
		return nil, true
	}, 0},
	"alt+g": {actAiGhostDismiss, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.dismissGhost()
		return m.ghostTrigger(), true
	}, 0},
	"ctrl+o": {actFinderOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startFinder()
		return nil, true
	}, 0},
	"f3": {actSearchNext, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.updateSearchMatches(true)
		return nil, true
	}, 0},
	"shift+f3": {actSearchPrev, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.findPrev()
		return nil, true
	}, 0},
	"ctrl+f": {actSearchOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startSearch()
		return nil, true
	}, 0},
	"ctrl+shift+f": {actProjectSearch, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+F arrives as Ctrl+F (both are the
		// control byte), so this only fires where the terminal can tell them
		// apart; the palette command covers the rest.
		m.startProjectSearch()
		return nil, true
	}, 0},
	"ctrl+shift+r": {actProjectReplace, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+F, with a worse collision: on a legacy
		// terminal this arrives as Ctrl+R, which is redo, so the palette command
		// "Replace in Project" is the portable way in.
		m.startProjectReplace()
		return nil, true
	}, 0},
	"ctrl+alt+r": {actLspRename, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startLspRename()
		return nil, true
	}, 0},
	"ctrl+alt+q": {actLspActions, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startLspActions(), true
	}, 0},
	"ctrl+alt+l": {actLspFormat, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// JetBrains-style reformat chord (+ note: alt+l alone toggles the
		// agent panel). Layout is the language server's job, not a reindent:
		// reach textDocument/formatting where the terminal lets ctrl+alt
		// through, palette command covers the rest.
		return m.lspFormatCmd(), true
	}, 0},
	"ctrl+alt+f": {actLspFormat, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same reformat, the pre-JetBrains chord kept as a fallback for
		// terminals that mangle ctrl+alt+L (ctrl+L alone is goto-line).
		return m.lspFormatCmd(), true
	}, 0},
	"ctrl+h": {actReplaceOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startReplace()
		return nil, true
	}, 0},
	"ctrl+g": {actViewGit, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.gitOpen {
			m.gitOpen = false
			m.gitFocus = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
		return nil, true
	}, 0},
	"f1": {actHelpToggle, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil, true
	}, 0},
	"ctrl+e": {actHelpToggle, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil, true
	}, 0},
	"ctrl+b": {actViewTree, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleTree()
		return nil, true
	}, 0},
	"f9": {actViewTree, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleTree()
		return nil, true
	}, 0},
	"f12": {actLspDefinition, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.gotoDefinition(), true
	}, 0},
	"f4": {actDebugBreakpoint, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleDebugBreakpoint(), true
	}, 0},
	"alt+m": {actBookmarkToggle, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleBookmarkAt(m.cur().buf.CurLine())
		return nil, true
	}, 0},
	"alt+n": {actBookmarkNext, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpBookmark(1)
		return nil, true
	}, 0},
	"alt+shift+n": {actBookmarkPrev, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpBookmark(-1)
		return nil, true
	}, 0},
	"f5": {actDebugRun, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startDebugging(), true
	}, 0},
	"shift+f5": {actDebugStop, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.stopDebugging(), true
	}, 0},
	"f6": {actDebugStepOver, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// With the debug panel open the F-keys step (F10/F11 are far from the
		// home row); with the panel closed F6 falls through to the vertical
		// split binding.
		if m.dapOpen {
			if m.dapRunState == dapStopped {
				return m.dapStepCmd("next"), true
			}
			return nil, true
		}
		return nil, false
	}, 0},
	"f7": {actDebugStepIn, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.dapOpen {
			if m.dapRunState == dapStopped {
				return m.dapStepCmd("stepIn"), true
			}
			return nil, true
		}
		return nil, false
	}, 0},
	"shift+f7": {actDebugStepOut, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.dapOpen {
			if m.dapRunState == dapStopped {
				return m.dapStepCmd("stepOut"), true
			}
			return nil, true
		}
		return nil, false
	}, 0},
	"ctrl+alt+d": {actDebugPanel, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleDebugPanel(), true
	}, 0},
}

// editorBindings are the keys of the text pane itself: they are reached only
// when no pane claimed the key.
var editorBindings = map[string]bindingDef{
	"ctrl+space": {actCompleteTrigger, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.triggerCompletion(true), true
	}, 0},
	"alt+d": {actCursorNextOccurrence, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		b := m.cur().buf
		if !b.AddNextOccurrence() {
			m.msg = m.t("msg.no_more_occurrences")
		} else if !b.HasMultipleCursors() && b.HasSelection() {
			m.msg = m.t("msg.selected")
		} else {
			m.msg = ""
		}
		return nil, true
	}, 0},
	"esc": {actCursorClearExtra, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.ClearCursors()
		m.msg = ""
		return nil, true
	}, protected},
	"ctrl+s": {actFileSave, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		if t.path == "" {
			m.startSavePrompt()
		} else {
			m.saveActive()
		}
		return nil, true
	}, 0},
	"ctrl+l": {actGotoLine, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startGotoPrompt()
		return nil, true
	}, 0},
	"ctrl+/": {actEditToggleComment, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleComment()
		return nil, true
	}, 0},
	"ctrl+_": {actEditToggleComment, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleComment()
		return nil, true
	}, 0},
	"alt+z": {actViewWordWrap, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleWordWrap()
		return nil, true
	}, 0},
	"alt+b": {actGitBlame, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleBlame(), true
	}, 0},
	"alt+c": {actFoldClose, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.foldCloseAtCursor()
		return nil, true
	}, 0},
	"alt+o": {actFoldOpen, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.foldOpenAtCursor()
		return nil, true
	}, 0},
	"ctrl+z": {actEditUndo, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.Undo() {
			m.msg = ""
		}
		return nil, true
	}, 0},
	"ctrl+y": {actEditDeleteLine, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DeleteLine()
		m.msg = ""
		return nil, true
	}, 0},
	"ctrl+d": {actEditDuplicateLine, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLine()
		m.msg = ""
		return nil, true
	}, 0},
	"ctrl+u": {actEditUppercase, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.uppercaseActive()
		return nil, true
	}, 0},
	"ctrl+shift+u": {actEditLowercase, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+U arrives as Ctrl+U (both are the
		// control byte), so there it uppercases instead of lowercasing — the
		// same family, nothing destroyed. The palette command "Edit: Lowercase"
		// is the portable route.
		m.lowercaseActive()
		return nil, true
	}, 0},
	"alt+shift+s": {actEditSortLines, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.sortLinesActive()
		return nil, true
	}, 0},
	"alt+shift+t": {actEditTrimTrailing, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// A terminal that cannot separate the chord from Alt+T toggles the
		// built-in terminal instead; "Edit: Trim Trailing Whitespace" in the
		// palette does the job everywhere.
		m.trimTrailingActive()
		return nil, true
	}, 0},
	"ctrl+alt+j": {actEditAlignIndent, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.alignIndentActive()
		return nil, true
	}, 0},
	"ctrl+alt+n": {actEditNumberLines, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.numberLinesActive()
		return nil, true
	}, 0},
	"ctrl+r": {actEditRedo, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.Redo() {
			m.msg = ""
		}
		return nil, true
	}, 0},
	"ctrl+shift+o": {actMergeOurs, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+O arrives as Ctrl+O (both are the
		// control byte), so this only fires where the terminal can tell them
		// apart; the palette command "Merge: Take Ours" covers the rest.
		m.resolveMergeConflict(conflictOurs)
		return nil, true
	}, 0},
	"ctrl+shift+t": {actMergeTheirs, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+O; the colliding Ctrl+T opens a file prompt.
		m.resolveMergeConflict(conflictTheirs)
		return nil, true
	}, 0},
	"ctrl+shift+b": {actMergeBoth, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+O; the colliding Ctrl+B shows the tree.
		m.resolveMergeConflict(conflictBoth)
		return nil, true
	}, 0},
	"alt+[": {actGitHunkPrev, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpChange(-1)
		return nil, true
	}, 0},
	"alt+]": {actGitHunkNext, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpChange(1)
		return nil, true
	}, 0},
	"ctrl+]": {actDelimiterJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// No backward chord next to it on purpose: Ctrl+[ *is* Escape, the two
		// are indistinguishable on every terminal. The jump always lands on a
		// delimiter instead, so pressing this one chord twice walks the pair.
		m.jumpToMatchingDelimiterActive()
		return nil, true
	}, 0},
	"ctrl+}": {actDelimiterJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Ctrl+] arrives as the control byte 0x1d on legacy terminals, and
		// controlByteKey re-encodes that as ctrl+} — ] and } share the byte. The
		// alias keeps the chord from falling through unclaimed; the palette
		// command "Edit: Select Inside Brackets" is the portable route for
		// the shifted chord below.
		m.jumpToMatchingDelimiterActive()
		return nil, true
	}, 0},
	"ctrl+shift+]": {actDelimiterSelectInside, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.selectInsideBracketsActive()
		return nil, true
	}, 0},
	"ctrl+shift+}": {actDelimiterSelectInside, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same chord on terminals that report the shifted glyph (}) as the key
		// instead of the physical one (]).
		m.selectInsideBracketsActive()
		return nil, true
	}, 0},
	"ctrl+\\": {actSplitVertical, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitVert()
		return nil, true
	}, 0},
	"f6": {actSplitVertical, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitVert()
		return nil, true
	}, 0},
	"ctrl+alt+h": {actSplitHorizontal, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitHoriz()
		return nil, true
	}, 0},
	"f7": {actSplitHorizontal, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitHoriz()
		return nil, true
	}, 0},
	"ctrl+alt+p": {actPaneFocusOther, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.focusOtherPane()
		return nil, true
	}, 0},
	"f8": {actPaneFocusOther, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.focusOtherPane()
		return nil, true
	}, 0},
	"alt+e": {actDiagnosticNext, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.nextDiagnostic(1)
		return nil, true
	}, 0},
	"alt+shift+e": {actDiagnosticPrev, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.nextDiagnostic(-1)
		return nil, true
	}, 0},
	"ctrl+k": {actLspHover, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startHoverCmd(), true
	}, 0},
	"shift+f12": {actLspReferences, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startRefsCmd(), true
	}, 0},
	"ctrl+alt+o": {actLspOutline, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startOutlineCmd(), true
	}, 0},
	"ctrl+alt+w": {actPaneClose, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.closePane()
		return nil, true
	}, 0},
	"ctrl+v": {actEditPaste, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if sysClip, err := clipboard.ReadAll(); err == nil && sysClip != "" {
			sysClip = normalizePaste(sysClip)
			// External text took the clipboard over: it cannot be a block this
			// editor copied, so the unfold rule does not apply to it.
			if sysClip != m.clipboard {
				m.clipboardBlock = false
			}
			m.clipboard = sysClip
		}
		m.pasteClipboard(m.clipboard)
		m.msg = ""
		return nil, true
	}, 0},
	"alt+left": {actTabPrev, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.switchTab(-1)
		return nil, true
	}, 0},
	"alt+right": {actTabNext, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.switchTab(1)
		return nil, true
	}, 0},
	// Alt+digit is a family, not a pattern: the nine chords are nine entries that
	// share one action, so the tab jump is a rebindable named command like every
	// other tab command, and the dispatch chain never parses the chord itself.
	"alt+1": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(0)
		return nil, true
	}, 0},
	"alt+2": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(1)
		return nil, true
	}, 0},
	"alt+3": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(2)
		return nil, true
	}, 0},
	"alt+4": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(3)
		return nil, true
	}, 0},
	"alt+5": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(4)
		return nil, true
	}, 0},
	"alt+6": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(5)
		return nil, true
	}, 0},
	"alt+7": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(6)
		return nil, true
	}, 0},
	"alt+8": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(7)
		return nil, true
	}, 0},
	"alt+9": {actTabJump, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpTab(8)
		return nil, true
	}, 0},
	"ctrl+alt+left": {actJumpBack, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// The jump list. A terminal that cannot tell this chord from Alt+←
		// hands the key to the tab switch above, so the palette's
		// "Jump Back" / "Jump Forward" are the portable route.
		m.jumpBack()
		return nil, true
	}, 0},
	"ctrl+alt+right": {actJumpForward, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpForward()
		return nil, true
	}, 0},
	"ctrl+tab": {actTabMru, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// The MRU tab cycle. A terminal that cannot tell this chord from Tab
		// hands the key to the indent below, and Windows Terminal sends Ctrl+Tab
		// to its own tab bar, so Alt+` is the alias that always arrives (ESC +
		// backquote) and the palette command the portable route.
		m.mruTab()
		return nil, true
	}, 0},
	"alt+`": {actTabMru, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.mruTab()
		return nil, true
	}, 0},
	"ctrl+shift+backspace": {actEditLastChange, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Go to the last change. On a terminal that cannot report the chord the
		// control byte arrives as Ctrl+H — Find & Replace, an overlay Esc
		// closes. The palette command is the route that never collapses.
		m.goToLastEdit()
		return nil, true
	}, 0},
	"alt+up": {actLineMoveUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLineUp()
		return nil, true
	}, 0},
	"alt+down": {actLineMoveDown, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLineDown()
		return nil, true
	}, 0},
	"alt+shift+down": {actEditDuplicateLine, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLine()
		return nil, true
	}, 0},
	"alt+shift+up": {actLineDuplicateUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLineUp()
		return nil, true
	}, 0},
	"shift+up": {actSelectLineUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveUpWithSelect()
		return nil, true
	}, 0},
	"shift+down": {actSelectLineDown, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveDownWithSelect()
		return nil, true
	}, 0},
	"shift+left": {actSelectCharLeft, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLeftWithSelect()
		return nil, true
	}, 0},
	"shift+right": {actSelectCharRight, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveRightWithSelect()
		return nil, true
	}, 0},
	"ctrl+alt+shift+up": {actSelectColumnUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.growColumnSelection(-1, 0)
		return nil, true
	}, 0},
	"ctrl+alt+shift+down": {actSelectColumnDown, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.growColumnSelection(1, 0)
		return nil, true
	}, 0},
	"ctrl+alt+shift+left": {actSelectColumnLeft, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.growColumnSelection(0, -1)
		return nil, true
	}, 0},
	"ctrl+alt+shift+right": {actSelectColumnRight, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.growColumnSelection(0, 1)
		return nil, true
	}, 0},
	"shift+home": {actSelectLineStart, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineStartWithSelect()
		return nil, true
	}, 0},
	"shift+end": {actSelectLineEnd, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineEndWithSelect()
		return nil, true
	}, 0},
	"up": {actNavUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllUp()
		} else {
			m.cur().buf.MoveUp()
		}
		m.foldSkip(-1)
		return nil, true
	}, protected},
	"down": {actNavDown, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllDown()
		} else {
			m.cur().buf.MoveDown()
		}
		m.foldSkip(1)
		return nil, true
	}, protected},
	"left": {actNavLeft, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllLeft()
		} else {
			m.cur().buf.MoveLeft()
		}
		return nil, true
	}, protected},
	"right": {actNavRight, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllRight()
		} else {
			m.cur().buf.MoveRight()
		}
		return nil, true
	}, protected},
	"home": {actNavLineStart, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineStart()
		return nil, true
	}, protected},
	"end": {actNavLineEnd, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineEnd()
		return nil, true
	}, protected},
	"pgup": {actNavPageUp, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() > 0; i++ {
			t.buf.MoveUp()
			m.foldSkip(-1)
		}
		return nil, true
	}, protected},
	"pgdown": {actNavPageDown, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() < t.buf.LineCount()-1; i++ {
			t.buf.MoveDown()
			m.foldSkip(1)
		}
		return nil, true
	}, protected},
	"enter": {actEditNewline, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiNewline()
		} else {
			m.cur().buf.IndentedNewline(m.smartIndent())
		}
		m.msg = ""
		if m.ai != nil {
			return m.ghostTrigger(), true
		}
		return nil, true
	}, protected},
	"backspace": {actEditBackspace, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiBackspace()
		} else if !deletePair(m.cur().buf) {
			m.cur().buf.Backspace()
		}
		m.msg = ""
		return m.triggerCompletion(false), true
	}, protected},
	"delete": {actEditDelete, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiDelete()
		} else {
			m.cur().buf.Delete()
		}
		m.msg = ""
		return nil, true
	}, protected},
	"tab": {actEditIndent, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiInsertRune('\t')
		} else if m.cur().buf.HasSelection() {
			// Indent the selected lines as a block, like every editor.
			m.cur().buf.IndentLines("\t")
		} else {
			m.cur().buf.Insert('\t')
		}
		m.msg = ""
		return nil, true
	}, protected},
	"shift+tab": {actEditUnindent, func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if !m.cur().buf.HasMultipleCursors() {
			m.cur().buf.UnindentLines("\t")
		}
		m.msg = ""
		return nil, true
	}, protected},
}

// typingFallback is the `default:` arm of the old editor switch: anything that
// is not bound and carries text goes into the buffer.
func (m *Model) typingFallback(msg tea.KeyPressMsg) tea.Cmd {
	if len(msg.Text) == 0 {
		return nil
	}
	if m.cur().buf.HasMultipleCursors() {
		m.cur().buf.MultiInsertText(msg.Text)
	} else {
		b := m.cur().buf
		for _, r := range msg.Text {
			if b.HasSelection() {
				// Typing over a selection replaces it; auto-close
				// resumes on the first keystroke after the replace.
				b.Insert(r)
			} else {
				insertTyped(b, r)
			}
		}
	}
	m.msg = ""
	return m.triggerCompletion(false)
}

// runBinding looks a key up in a resolved table and runs it when the key is
// claimed. The table comes from the model's own KeyMap, never from the package
// defaults, so a rebind that reached this model also reaches every stage.
func (m *Model) runBinding(table map[string]bindingDef, s string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	d, ok := table[s]
	if !ok {
		return nil, false
	}
	return d.fn(m, msg)
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	msg, s := keyOf(msg)

	// The PTY owns input while the panel is focused. Alt+T closes the panel
	// from anywhere; clicking the editor or the chat rail takes focus back,
	// so the editor and chat keep working while the terminal stays open.
	if m.termOpen {
		if s == "alt+t" {
			m.termOpen = false
			return nil
		}
		if m.termFocus {
			return m.handleTerm(s, msg)
		}
	}

	// JetBrains-style double Shift opens "search everywhere": commands,
	// project files and settings in one list.
	// Bare modifier presses are only reported by terminals with the Kitty
	// keyboard protocol or Win32 Input Mode; elsewhere this is a no-op.
	if isBareShift(msg, s) {
		if !msg.IsRepeat {
			now := time.Now()
			if now.Sub(m.lastShiftTime) <= doubleShiftInterval && !m.lastShiftTime.IsZero() {
				m.lastShiftTime = time.Time{}
				m.startSearchEverywhere()
				return nil
			}
			m.lastShiftTime = now
		}
		return nil
	}
	// Any non-Shift key resets double-shift detection so chords or typing
	// between Shift taps don't accidentally trigger the palette.
	m.lastShiftTime = time.Time{}

	// The macro chords answer before anything else, so a recording can be
	// stopped (and a macro replayed) no matter what the text pane binds.
	if s == "alt+r" {
		m.toggleMacroRecording()
		return nil
	}
	if s == "alt+shift+r" {
		return m.replayMacro()
	}
	// While recording, every key that reaches the text pane lands in the
	// macro; the recording-aware dispatch records as it runs.
	if m.macroRecording {
		return m.macroDispatch(s, msg)
	}

	// While the completion popup is open, navigation keys control it.
	if m.complOpen && m.handleCompletionKey(s) {
		return nil
	}

	return m.dispatchKey(s, msg)
}

// dispatchKey runs one already-normalized key through the binding chain:
// the panes/overlay that own the keyboard, the tail tables, the editor's
// bindings, and finally the typing fallback. Both the live key handling and
// the macro playback feed the very same chain, so a replayed macro is by
// construction the same work the recorded keys did.
func (m *Model) dispatchKey(s string, msg tea.KeyPressMsg) tea.Cmd {
	// While the completion popup is open, navigation keys control it.
	if m.complOpen && m.handleCompletionKey(s) {
		return nil
	}
	if cmd, handled := m.runBinding(m.keys.global, s, msg); handled {
		return cmd
	}
	if cmd, handled := m.dispatchPanes(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.handleTailBindings(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.runBinding(m.keys.editor, s, msg); handled {
		return cmd
	}
	return m.typingFallback(msg)
}

// dispatchPanes gives the pane or overlay that currently owns the keyboard
// first claim on the key, before the editor's own bindings are considered.
func (m *Model) dispatchPanes(s string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.conflictOpen {
		return m.handleConflict(s), true
	}
	if m.diffViewOpen {
		return m.handleDiffView(s), true
	}
	if m.sqlOpen {
		return m.handleSqlite(s, msg), true
	}
	if m.agentReviewMode {
		return m.handleAgentReview(s, msg), true
	}
	if m.agentPrompt {
		return m.handleAgentPrompt(s, msg), true
	}
	// A question from ASK_USER grabs the input wherever focus happens to be:
	// the model is waiting for an answer and any other key would be swallowed
	// by whatever pane happens to be focused.
	if m.askReq != nil {
		return m.handleAskKey(s, msg), true
	}
	if m.agentOpen && m.agentFocus {
		return m.handleAgent(s, msg), true
	}
	if m.chatReviewMode {
		return m.handleChatReview(s), true
	}
	if m.chatOpen && m.chatFocus {
		return m.handleChat(s, msg), true
	}
	if m.gitOpen && m.gitFocus {
		return m.handleGit(s, msg), true
	}
	if m.paletteOpen {
		return m.handlePalette(s, msg), true
	}
	if m.folderOpen {
		return m.handleFolderBrowser(s), true
	}
	if m.aiCfgOpen {
		return m.handleAISettings(s, msg), true
	}
	if m.dapCfgOpen {
		return m.handleDAPCfg(s, msg), true
	}
	if m.helpOpen {
		return m.handleHelp(s), true
	}
	if m.langChooserOpen {
		return m.handleLangChooser(s), true
	}
	if m.pluginStoreOpen {
		return m.handlePluginStore(s), true
	}
	if m.promptOpen {
		return m.handlePrompt(s, msg), true
	}
	if m.promptSave {
		return m.handleSavePrompt(s, msg), true
	}
	if m.finderOpen {
		return m.handleFinder(s, msg), true
	}
	if m.treeFocus {
		return m.handleTree(s, msg), true
	}
	// The LSP rename input and the quick-fix list own their keys (bottom
	// overlays like the search prompt).
	if m.lspRenameOpen {
		return m.handleLspRename(s, msg), true
	}
	if m.lspActionsOpen {
		return m.handleLspActions(s, msg), true
	}
	// Hover, references and outline: bottom overlays like the quick fixes.
	if m.hoverOpen {
		return m.handleHover(s, msg), true
	}
	if m.refsOpen {
		return m.handleRefs(s, msg), true
	}
	if m.outlineOpen {
		return m.handleOutline(s, msg), true
	}
	// Find in Project owns its keys first: it is a bottom overlay like the
	// finder, and its Enter must not jump the file-search cursor underneath.
	// The replace review answers before the panel, since both are open while a
	// previewed file is being reviewed.
	if m.projReplReview {
		return m.handleProjReplReview(s), true
	}
	if m.projSearchOpen {
		return m.handleProjSearch(s, msg), true
	}
	if m.dapOpen {
		return m.handleDap(s, msg), true
	}
	if m.searchOpen {
		if m.replaceOpen {
			return m.handleReplace(s, msg), true
		}
		return m.handleSearch(s, msg), true
	}
	if m.gotoOpen {
		return m.handleGoto(s, msg), true
	}
	if m.aiReviewMode {
		return m.handleInlineReview(s, msg), true
	}
	if m.aiFixReviewMode {
		return m.handleFixReview(s, msg), true
	}
	if m.aiFixOpen {
		return m.handleFixRequest(s, msg), true
	}
	if m.aiInlineOpen {
		return m.handleInlineRequest(s, msg), true
	}
	// While the inline AI streams a rewrite, swallow all keys; Esc/Ctrl+C
	// abort the request.
	if m.aiInlineBusy {
		if s == "esc" || s == "ctrl+c" {
			m.cancelInlineRequest()
		}
		return nil, true
	}
	// While the fix AI streams, swallow all keys; Esc aborts.
	if m.aiFixBusy {
		if s == "esc" || s == "ctrl+c" {
			m.cancelFixRequest()
		}
		return nil, true
	}
	return nil, false
}

// handleTailBindings is the stretch between the panes and the text pane: the
// confirmations and the overlays that sit above normal editing.
func (m *Model) handleTailBindings(s string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.quitConfirm {
		return m.handleQuitConfirm(s, msg), true
	}
	if m.keys.isTabJump(s) {
		// The tab commands are about the workspace, so they answer here rather
		// than at the text-pane stage: a focused picture tab claims every key it
		// is handed, and alt+digit must not be one of the keys it swallows.
		return m.runBinding(m.keys.editor, s, msg)
	}
	// Plugins get first crack at unhandled keys so they can override built-ins.
	if m.plugins != nil && m.plugins.HasBinding(s) {
		if m.plugins.RunBinding(m, s) {
			return nil, true
		}
	}
	// Ghost text overlay: Tab accepts, Esc dismisses; any other key dismisses
	// and falls through to normal editing.
	if m.ghostVisible && !m.chatOpen && !m.complOpen {
		switch s {
		case "tab":
			m.applyGhost()
			return nil, true
		case "esc", "ctrl+c":
			m.dismissGhost()
			return nil, true
		default:
			m.dismissGhost()
		}
	}
	// A picture tab owns its keys first: nothing may reach the buffer, because a
	// keypress that did would type into an empty document the user cannot see,
	// and the first Ctrl+S would then replace the picture with what they typed.
	// handleImageKey answers what is about the workspace (tab switching,
	// scrolling, hunk jumps) and drops the rest.
	if t := m.cur(); t != nil && t.img != nil {
		return m.handleImageKey(s), true
	}
	return nil, false
}

// handleConflict drives the external-change reload prompt: the watched file
// changed on disk while its buffer was dirty. Merge conflicts are a different
// thing and are resolved in the buffer itself (see mergeconflict.go).
func (m *Model) handleConflict(s string) tea.Cmd {
	switch s {
	case "r", "R":
		for i := range m.tabs {
			t := &m.tabs[i]
			absT, _ := filepath.Abs(t.path)
			if absT == m.conflictPath || t.path == m.conflictPath {
				if data, err := os.ReadFile(t.path); err == nil {
					t.buf = buffer.Load(normalizePaste(string(data)))
					t.syntaxCached = nil
					t.diffText = ""
					m.msg = m.t("msg.reloaded")
				}
				break
			}
		}
		m.conflictOpen = false
		m.conflictRows = nil
		return nil
	case "i", "I", "esc":
		m.conflictOpen = false
		m.conflictRows = nil
		m.msg = m.t("msg.kept")
		return nil
	case "up", "k":
		if m.conflictOffY > 0 {
			m.conflictOffY--
		}
	case "down", "j":
		if m.conflictOffY < len(m.conflictRows)-1 {
			m.conflictOffY++
		}
	case "pgup":
		m.conflictOffY -= m.paneViewHeight(m.activePane) / 2
		if m.conflictOffY < 0 {
			m.conflictOffY = 0
		}
	case "pgdown":
		m.conflictOffY += m.paneViewHeight(m.activePane) / 2
		maxOff := len(m.conflictRows) - 1
		if m.conflictOffY > maxOff {
			m.conflictOffY = maxOff
		}
	case "home", "g":
		m.conflictOffY = 0
	case "end", "G":
		m.conflictOffY = len(m.conflictRows) - 1
		if m.conflictOffY < 0 {
			m.conflictOffY = 0
		}
	case "left", "h":
		m.conflictOffX -= 8
		if m.conflictOffX < 0 {
			m.conflictOffX = 0
		}
	case "right", "l":
		m.conflictOffX += 8
	}
	return nil
}
