package editor

import (
	"os"
	"path/filepath"
	"strings"
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

// globalBindings answer regardless of which pane or overlay has focus: the
// chords that must always reach the editor (quit, copy, panels, debugging).
var globalBindings = map[string]binding{
	"ctrl+q": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.requestQuit(), true
	},
	"ctrl+c": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			clipboard.WriteAll(m.clipboard)
			m.msg = m.t("msg.copied")
			return nil, true
		}
		// Ctrl+C never quits: an editor that dies on the key a terminal
		// uses to interrupt a program is a trap. Ctrl+Q is the way out.
		m.msg = m.t("msg.quit_hint")
		return nil, true
	},
	"ctrl+w": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.closeActiveTab(), true
	},
	"ctrl+x": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			clipboard.WriteAll(m.clipboard)
			m.cur().buf.DeleteSelection()
			m.msg = m.t("msg.cut")
			return nil, true
		}
		return m.closeActiveTab(), true
	},
	"ctrl+shift+p": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Search everywhere: commands, files and settings. The double Shift
		// does the same, but most terminals never report a bare modifier
		// press, and on legacy terminals Ctrl+Shift+P collapses into the
		// Ctrl+P control byte — Alt+P (ESC+p) stays distinguishable even
		// there, so this is the binding that always works.
		m.startSearchEverywhere()
		return nil, true
	},
	"alt+p": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startSearchEverywhere()
		return nil, true
	},
	"ctrl+p": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPalette()
		return nil, true
	},
	"f2": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPalette()
		return nil, true
	},
	"ctrl+t": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startPrompt()
		return nil, true
	},
	"alt+t": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleTerminal(), true
	},
	"alt+a": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleChat()
		return nil, true
	},
	"alt+l": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleAgentPanel(), true
	},
	"alt+i": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startInlineRequest()
		return nil, true
	},
	"alt+f": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startFixRequest()
		return nil, true
	},
	"alt+g": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.dismissGhost()
		return m.ghostTrigger(), true
	},
	"ctrl+o": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startFinder()
		return nil, true
	},
	"f3": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.updateSearchMatches(true)
		return nil, true
	},
	"shift+f3": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.findPrev()
		return nil, true
	},
	"ctrl+f": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startSearch()
		return nil, true
	},
	"ctrl+shift+f": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+F arrives as Ctrl+F (both are the
		// control byte), so this only fires where the terminal can tell them
		// apart; the palette command covers the rest.
		m.startProjectSearch()
		return nil, true
	},
	"ctrl+shift+r": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+F, with a worse collision: on a legacy
		// terminal this arrives as Ctrl+R, which is redo, so the palette command
		// "Replace in Project" is the portable way in.
		m.startProjectReplace()
		return nil, true
	},
	"ctrl+alt+r": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startLspRename()
		return nil, true
	},
	"ctrl+alt+q": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startLspActions(), true
	},
	"ctrl+alt+l": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// JetBrains-style reformat chord (+ note: alt+l alone toggles the
		// agent panel). Layout is the language server's job, not a reindent:
		// reach textDocument/formatting where the terminal lets ctrl+alt
		// through, palette command covers the rest.
		return m.lspFormatCmd(), true
	},
	"ctrl+alt+f": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same reformat, the pre-JetBrains chord kept as a fallback for
		// terminals that mangle ctrl+alt+L (ctrl+L alone is goto-line).
		return m.lspFormatCmd(), true
	},
	"ctrl+h": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startReplace()
		return nil, true
	},
	"ctrl+g": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.gitOpen {
			m.gitOpen = false
			m.gitFocus = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
		return nil, true
	},
	"f1": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil, true
	},
	"ctrl+e": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.helpOpen = !m.helpOpen
		if m.helpOpen {
			m.helpScroll = 0
		}
		return nil, true
	},
	"ctrl+b": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleTree()
		return nil, true
	},
	"f9": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleTree()
		return nil, true
	},
	"f12": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.gotoDefinition(), true
	},
	"f4": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleDebugBreakpoint(), true
	},
	"alt+m": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleBookmarkAt(m.cur().buf.CurLine())
		return nil, true
	},
	"alt+n": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpBookmark(1)
		return nil, true
	},
	"alt+shift+n": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpBookmark(-1)
		return nil, true
	},
	"f5": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.startDebugging(), true
	},
	"shift+f5": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.stopDebugging(), true
	},
	"f6": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
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
	},
	"f7": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.dapOpen {
			if m.dapRunState == dapStopped {
				return m.dapStepCmd("stepIn"), true
			}
			return nil, true
		}
		return nil, false
	},
	"shift+f7": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.dapOpen {
			if m.dapRunState == dapStopped {
				return m.dapStepCmd("stepOut"), true
			}
			return nil, true
		}
		return nil, false
	},
	"ctrl+alt+d": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleDebugPanel(), true
	},
}

// editorBindings are the keys of the text pane itself: they are reached only
// when no pane claimed the key.
var editorBindings = map[string]binding{
	"ctrl+space": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.triggerCompletion(true), true
	},
	"alt+d": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		b := m.cur().buf
		if !b.AddNextOccurrence() {
			m.msg = m.t("msg.no_more_occurrences")
		} else if !b.HasMultipleCursors() && b.HasSelection() {
			m.msg = m.t("msg.selected")
		} else {
			m.msg = ""
		}
		return nil, true
	},
	"esc": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.ClearCursors()
		m.msg = ""
		return nil, true
	},
	"ctrl+s": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		if t.path == "" {
			m.startSavePrompt()
		} else {
			m.saveActive()
		}
		return nil, true
	},
	"ctrl+l": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.startGotoPrompt()
		return nil, true
	},
	"ctrl+/": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleComment()
		return nil, true
	},
	"ctrl+_": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleComment()
		return nil, true
	},
	"alt+z": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleWordWrap()
		return nil, true
	},
	"alt+b": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		return m.toggleBlame(), true
	},
	"alt+c": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.foldCloseAtCursor()
		return nil, true
	},
	"alt+o": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.foldOpenAtCursor()
		return nil, true
	},
	"ctrl+z": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.Undo() {
			m.msg = ""
		}
		return nil, true
	},
	"ctrl+y": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DeleteLine()
		m.msg = ""
		return nil, true
	},
	"ctrl+d": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLine()
		m.msg = ""
		return nil, true
	},
	"ctrl+u": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.uppercaseActive()
		return nil, true
	},
	"ctrl+shift+u": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+U arrives as Ctrl+U (both are the
		// control byte), so there it uppercases instead of lowercasing — the
		// same family, nothing destroyed. The palette command "Edit: Lowercase"
		// is the portable route.
		m.lowercaseActive()
		return nil, true
	},
	"alt+shift+s": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.sortLinesActive()
		return nil, true
	},
	"alt+shift+t": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// A terminal that cannot separate the chord from Alt+T toggles the
		// built-in terminal instead; "Edit: Trim Trailing Whitespace" in the
		// palette does the job everywhere.
		m.trimTrailingActive()
		return nil, true
	},
	"ctrl+alt+j": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.alignIndentActive()
		return nil, true
	},
	"ctrl+alt+n": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.numberLinesActive()
		return nil, true
	},
	"ctrl+r": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.Redo() {
			m.msg = ""
		}
		return nil, true
	},
	"ctrl+shift+o": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// On legacy terminals Ctrl+Shift+O arrives as Ctrl+O (both are the
		// control byte), so this only fires where the terminal can tell them
		// apart; the palette command "Merge: Take Ours" covers the rest.
		m.resolveMergeConflict(conflictOurs)
		return nil, true
	},
	"ctrl+shift+t": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+O; the colliding Ctrl+T opens a file prompt.
		m.resolveMergeConflict(conflictTheirs)
		return nil, true
	},
	"ctrl+shift+b": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same caveat as Ctrl+Shift+O; the colliding Ctrl+B shows the tree.
		m.resolveMergeConflict(conflictBoth)
		return nil, true
	},
	"alt+[": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpChange(-1)
		return nil, true
	},
	"alt+]": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpChange(1)
		return nil, true
	},
	"ctrl+]": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// No backward chord next to it on purpose: Ctrl+[ *is* Escape, the two
		// are indistinguishable on every terminal. The jump always lands on a
		// delimiter instead, so pressing this one chord twice walks the pair.
		m.jumpToMatchingDelimiterActive()
		return nil, true
	},
	"ctrl+}": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Ctrl+] arrives as the control byte 0x1d on legacy terminals, and
		// controlByteKey re-encodes that as ctrl+} — ] and } share the byte. The
		// alias keeps the chord from falling through unclaimed; the palette
		// command "Edit: Select Inside Brackets" is the portable route for
		// the shifted chord below.
		m.jumpToMatchingDelimiterActive()
		return nil, true
	},
	"ctrl+shift+]": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.selectInsideBracketsActive()
		return nil, true
	},
	"ctrl+shift+}": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// Same chord on terminals that report the shifted glyph (}) as the key
		// instead of the physical one (]).
		m.selectInsideBracketsActive()
		return nil, true
	},
	"ctrl+\\": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitVert()
		return nil, true
	},
	"f6": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitVert()
		return nil, true
	},
	"ctrl+alt+h": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitHoriz()
		return nil, true
	},
	"f7": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.toggleSplitHoriz()
		return nil, true
	},
	"ctrl+alt+p": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.focusOtherPane()
		return nil, true
	},
	"f8": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.focusOtherPane()
		return nil, true
	},
	"ctrl+alt+w": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.closePane()
		return nil, true
	},
	"ctrl+v": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if sysClip, err := clipboard.ReadAll(); err == nil && sysClip != "" {
			m.clipboard = normalizePaste(sysClip)
		}
		if m.clipboard != "" {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(m.clipboard)
			} else {
				m.cur().buf.InsertText(m.clipboard)
			}
			m.msg = ""
		}
		return nil, true
	},
	"alt+left": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.switchTab(-1)
		return nil, true
	},
	"alt+right": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.switchTab(1)
		return nil, true
	},
	"ctrl+alt+left": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		// The jump list. A terminal that cannot tell this chord from Alt+←
		// hands the key to the tab switch above, so the palette's
		// "Jump Back" / "Jump Forward" are the portable route.
		m.jumpBack()
		return nil, true
	},
	"ctrl+alt+right": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.jumpForward()
		return nil, true
	},
	"alt+up": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLineUp()
		return nil, true
	},
	"alt+down": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLineDown()
		return nil, true
	},
	"alt+shift+down": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLine()
		return nil, true
	},
	"alt+shift+up": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.DuplicateLineUp()
		return nil, true
	},
	"shift+up": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveUpWithSelect()
		return nil, true
	},
	"shift+down": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveDownWithSelect()
		return nil, true
	},
	"shift+left": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveLeftWithSelect()
		return nil, true
	},
	"shift+right": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.MoveRightWithSelect()
		return nil, true
	},
	"shift+home": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineStartWithSelect()
		return nil, true
	},
	"shift+end": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineEndWithSelect()
		return nil, true
	},
	"up": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllUp()
		} else {
			m.cur().buf.MoveUp()
		}
		m.foldSkip(-1)
		return nil, true
	},
	"down": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllDown()
		} else {
			m.cur().buf.MoveDown()
		}
		m.foldSkip(1)
		return nil, true
	},
	"left": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllLeft()
		} else {
			m.cur().buf.MoveLeft()
		}
		return nil, true
	},
	"right": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllRight()
		} else {
			m.cur().buf.MoveRight()
		}
		return nil, true
	},
	"home": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineStart()
		return nil, true
	},
	"end": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		m.cur().buf.LineEnd()
		return nil, true
	},
	"pgup": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() > 0; i++ {
			t.buf.MoveUp()
			m.foldSkip(-1)
		}
		return nil, true
	},
	"pgdown": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() < t.buf.LineCount()-1; i++ {
			t.buf.MoveDown()
			m.foldSkip(1)
		}
		return nil, true
	},
	"enter": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
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
	},
	"backspace": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiBackspace()
		} else if !deletePair(m.cur().buf) {
			m.cur().buf.Backspace()
		}
		m.msg = ""
		return m.triggerCompletion(false), true
	},
	"delete": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiDelete()
		} else {
			m.cur().buf.Delete()
		}
		m.msg = ""
		return nil, true
	},
	"tab": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
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
	},
	"shift+tab": func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
		if !m.cur().buf.HasMultipleCursors() {
			m.cur().buf.UnindentLines("\t")
		}
		m.msg = ""
		return nil, true
	},
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

// runBinding looks a key up in a table and runs it when the key is claimed.
func (m *Model) runBinding(table map[string]binding, s string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	fn, ok := table[s]
	if !ok {
		return nil, false
	}
	return fn(m, msg)
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

	// While the completion popup is open, navigation keys control it.
	if m.complOpen && m.handleCompletionKey(s) {
		return nil
	}

	if cmd, handled := m.runBinding(globalBindings, s, msg); handled {
		return cmd
	}
	if cmd, handled := m.dispatchPanes(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.handleTailBindings(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.runBinding(editorBindings, s, msg); handled {
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
	if len(s) == 5 && strings.HasPrefix(s, "alt+") && s[4] >= '1' && s[4] <= '9' {
		m.jumpTab(int(s[4] - '1'))
		return nil, true
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
