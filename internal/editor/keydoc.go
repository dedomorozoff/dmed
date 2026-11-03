package editor

import (
	"fmt"
	"strings"
)

// The F1 cheatsheet is generated from the binding tables, because a hand-copied
// list of chords drifts from the chords themselves: it already had a row for a
// tree command whose i18n key existed in no catalog, and the same chord written
// down in five places that no test compared. A row names actions, so the keys
// column is whatever those actions are bound to *now* — including an alias added
// by a [keys] section — and a rebind cannot make the help lie.

// helpRow is one rendered row: the display spelling of the chords and the i18n
// key of the description. An empty keys is a blank separator.
type helpRow struct {
	keys string
	desc string
}

// helpSpec is one row before it is resolved: the actions it documents, in the
// order they should be read, their description, and — for the handful of rows
// whose shape the tables cannot express — the keys column written by hand.
//
// A row may be an override because it is about the mouse (Ctrl+Click), because
// it groups keys that are not one action (the debug stepping row), or because
// the generated column would run three times wider than the panel. Those are
// the honest cases; everything else names actions.
type helpSpec struct {
	acts []keyAct
	desc string
	keys string // hand-drawn keys column; empty means generate it
}

// helpSpecs is the order of the cheatsheet, in the same blocks the hand-written
// table used so an F1 screen that changed this way is still recognizable. An
// empty spec is a blank separator row.
var helpSpecs = []helpSpec{
	{acts: []keyAct{actFileSave}, desc: "help.save"},
	{},

	{acts: []keyAct{actPaletteOpen}, desc: "help.palette"},
	{acts: []keyAct{actPaletteEverywhere}, desc: "help.everywhere"},
	{keys: "Shift+Arrows", desc: "help.select"},
	{acts: []keyAct{actEditCopy, actEditCut, actEditPaste}, desc: "help.clipboard"},
	{},

	{acts: []keyAct{actSearchOpen}, desc: "help.search"},
	{acts: []keyAct{actProjectSearch}, desc: "help.proj_search"},
	{acts: []keyAct{actProjectReplace}, desc: "help.proj_replace"},
	{acts: []keyAct{actReplaceOpen}, desc: "help.replace"},
	{acts: []keyAct{actGotoLine}, desc: "help.goto_line"},
	{acts: []keyAct{actLspFormat}, desc: "help.format"},
	{acts: []keyAct{actViewWordWrap}, desc: "help.word_wrap"},
	{acts: []keyAct{actGitBlame}, desc: "help.blame"},
	{acts: []keyAct{actViewGit}, desc: "help.git_panel"},
	{acts: []keyAct{actDebugPanel}, desc: "help.debug"},
	{keys: "F4 / F5 / F6 / F7 / S+F5 / S+F7", desc: "help.debug_keys"},
	{keys: "↑↓/PgUp/PgDn/Home/End, +/-", desc: "help.debug_panel"},
	{keys: "F12 / Ctrl+Click", desc: "help.goto_def"},
	{keys: "D (in Git panel)", desc: "help.git_diff"},
	{acts: []keyAct{actGitHunkPrev, actGitHunkNext}, desc: "help.hunk"},
	{keys: "Ctrl+Shift+O / T / B", desc: "help.merge_resolve"},
	{acts: []keyAct{actDelimiterJump}, desc: "help.jump_bracket"},
	{acts: []keyAct{actDelimiterSelectInside}, desc: "help.select_inside"},
	{acts: []keyAct{actJumpBack, actJumpForward}, desc: "help.jump_list"},
	{acts: []keyAct{actEditLastChange}, desc: "help.last_edit"},
	{acts: []keyAct{actBookmarkToggle, actBookmarkNext, actBookmarkPrev}, desc: "help.bookmark"},
	{acts: []keyAct{actFinderOpen}, desc: "help.finder"},
	{acts: []keyAct{actFileOpenPrompt}, desc: "help.open"},
	{acts: []keyAct{actViewTerminal}, desc: "help.terminal"},
	{acts: []keyAct{actViewChat}, desc: "help.chat"},
	{acts: []keyAct{actAiInline}, desc: "help.inline"},
	{acts: []keyAct{actAiGhostDismiss}, desc: "help.ghost"},
	{acts: []keyAct{actViewAgent}, desc: "help.agent"},
	{acts: []keyAct{actViewTree}, desc: "help.tree"},
	{keys: "↑↓/Enter/←→ in tree", desc: "help.tree_nav"},
	{acts: []keyAct{actTabPrev, actTabNext}, desc: "help.tab_switch"},
	{acts: []keyAct{actTabJump}, desc: "help.tab_jump"},
	{acts: []keyAct{actTabMru}, desc: "help.tab_mru"},
	{acts: []keyAct{actSplitVertical}, desc: "help.split_vert"},
	{acts: []keyAct{actSplitHorizontal}, desc: "help.split_horiz"},
	{acts: []keyAct{actPaneFocusOther}, desc: "help.split_focus"},
	{acts: []keyAct{actPaneClose}, desc: "help.split_close"},
	{acts: []keyAct{actTabClose, actEditCut}, desc: "help.tab_close"},
	{},

	{keys: "Arrows/Home/End/PgUp/PgDn", desc: "help.move"},
	{keys: "Enter/Backspace/Delete/Tab", desc: "help.edit"},
	{acts: []keyAct{actEditUndo, actEditRedo}, desc: "help.undo"},
	{acts: []keyAct{actEditDeleteLine, actEditDuplicateLine}, desc: "help.lines"},
	{acts: []keyAct{actEditUppercase}, desc: "help.uppercase"},
	{acts: []keyAct{actEditLowercase}, desc: "help.lowercase"},
	{acts: []keyAct{actEditSortLines}, desc: "help.sort_lines"},
	{acts: []keyAct{actEditTrimTrailing}, desc: "help.trim_lines"},
	{acts: []keyAct{actEditAlignIndent}, desc: "help.align_indent"},
	{acts: []keyAct{actEditNumberLines}, desc: "help.number_lines"},
	{acts: []keyAct{actCursorNextOccurrence}, desc: "help.multicursor_word"},
	{keys: "Alt+Click", desc: "help.multicursor_click"},
	{acts: []keyAct{actCursorClearExtra}, desc: "help.multicursor_esc"},
	{acts: []keyAct{actLineMoveUp, actLineMoveDown}, desc: "help.move_line"},
	{acts: []keyAct{actFoldClose, actFoldOpen}, desc: "help.fold"},
	{},

	{acts: []keyAct{actHelpToggle}, desc: "help.toggle_help"},
	{acts: []keyAct{actAppQuit}, desc: "help.quit"},
}

// codeSpan fences a keys cell for Markdown. Alt+` is a real chord, and a
// backtick inside a single-backtick span closes it where it was meant to be
// printed; CommonMark lets a longer fence hold a shorter run, and wants the
// padding spaces when the text itself starts or ends with a backtick.
func codeSpan(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// DumpKeys renders the same cheatsheet F1 shows, for a document that wants the
// chord list rather than a terminal. It exists because the tables are the only
// place a chord is decided: a key reference written by hand in a README is a
// copy that nothing checks, and this makes the copy comparable.
//
// format is "md" (a Markdown table) or "roff" (a man page tag list).
func DumpKeys(format string) (string, error) {
	m := New()
	rows := m.helpRows()
	var b strings.Builder
	switch format {
	case "md":
		b.WriteString("| Keys | Action |\n| --- | --- |\n")
		for _, r := range rows {
			if r.keys == "" {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s |\n", codeSpan(r.keys), m.t(r.desc))
		}
	case "roff":
		b.WriteString(".Bl -tag -width \"Ctrl+Shift+Backspace\"\n")
		for _, r := range rows {
			if r.keys == "" {
				continue
			}
			fmt.Fprintf(&b, ".It Sy %s\n%s\n", r.keys, m.t(r.desc))
		}
		b.WriteString(".El\n")
	default:
		return "", fmt.Errorf("unknown key dump format %q (want md or roff)", format)
	}
	return b.String(), nil
}

// helpRows resolves the cheatsheet against this model's key map, so a rebind is
// reflected the moment the config reload repaints the screen.
func (m Model) helpRows() []helpRow {
	rows := make([]helpRow, 0, len(helpSpecs))
	for _, s := range helpSpecs {
		if len(s.acts) == 0 && s.keys == "" {
			rows = append(rows, helpRow{}) // separator
			continue
		}
		keys := s.keys
		if keys == "" {
			keys = m.chordColumn(s.acts)
		}
		if keys == "" {
			// An action nothing is bound to has no row: the unbind removed the
			// chord, and a blank keys column would advertise a key that does
			// nothing.
			continue
		}
		rows = append(rows, helpRow{keys: keys, desc: s.desc})
	}
	return rows
}

// chordColumn lists the chords of the given actions, in the order the row
// declares them, with every alias of one action next to it.
func (m Model) chordColumn(acts []keyAct) string {
	var out []string
	for _, a := range acts {
		out = append(out, collapseRun(m.keys.chords(a))...)
	}
	parts := make([]string, 0, len(out))
	for _, c := range out {
		parts = append(parts, displayChord(c))
	}
	return strings.Join(parts, " / ")
}

// collapseRun turns a run of digit chords (alt+1 … alt+9) into one range, so a
// family of nine entries reads as the nine-finger gesture it is instead of
// filling the column.
func collapseRun(chords []string) []string {
	if len(chords) < 3 {
		return chords
	}
	head := chords[0][:len(chords[0])-1]
	first := chords[0][len(chords[0])-1]
	last := chords[len(chords)-1][len(chords[len(chords)-1])-1]
	if first < '1' || last > '9' {
		return chords
	}
	for _, c := range chords {
		if len(c) != len(head)+1 || c[:len(c)-1] != head {
			return chords
		}
	}
	for i := 1; i < len(chords); i++ {
		if chords[i][len(chords[i])-1] != chords[i-1][len(chords[i-1])-1]+1 {
			return chords
		}
	}
	return []string{fmt.Sprintf("%s%d..%d", head, first-'0', last-'0')}
}

// keyWords spells the keys that are not a printable character the way the help
// screen has always spelled them.
var keyWords = map[string]string{
	"esc":       "Esc",
	"enter":     "Enter",
	"tab":       "Tab",
	"backspace": "Backspace",
	"delete":    "Del",
	"space":     "Space",
	"up":        "↑",
	"down":      "↓",
	"left":      "←",
	"right":     "→",
	"pgup":      "PgUp",
	"pgdown":    "PgDn",
	"home":      "Home",
	"end":       "End",
}

// keyMods are the modifier tokens, in the order a chord writes them.
var keyMods = map[string]string{"ctrl": "Ctrl", "alt": "Alt", "shift": "Shift"}

// displayChord turns one table spelling into the help-screen spelling: the
// inverse of what keyOf normalizes ("ctrl+shift+f5" → "Ctrl+Shift+F5",
// "alt+down" → "Alt+↓"). The chord is the key of the map, so it arrives exactly
// as the table wrote it.
func displayChord(chord string) string {
	// A chord that ends in the plus sign itself ("ctrl++") must keep it: the
	// join below would eat the separator it looks like.
	if strings.HasSuffix(chord, "+") {
		return displayChord(strings.TrimSuffix(chord, "+")) + "+"
	}
	tokens := strings.Split(chord, "+")
	last := tokens[len(tokens)-1]
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens[:len(tokens)-1] {
		if name, ok := keyMods[t]; ok {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, capitalize(t))
	}
	if name, ok := keyWords[last]; ok {
		parts = append(parts, name)
	} else {
		parts = append(parts, capitalize(last))
	}
	return strings.Join(parts, "+")
}

// capitalize upper-cases only the first rune, so a function key becomes "F12",
// a digit run stays "1..9" and a symbol like "]" is left alone.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}
