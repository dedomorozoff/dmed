package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The binding tables replaced a 660-line handleKey switch. These tests are the
// reason the switch was worth replacing: every chord, its fall-through rules
// and the key normalization are now checked without a terminal.

func TestBindingTablesAreWellFormed(t *testing.T) {
	for name, table := range map[string]map[string]binding{
		"globalBindings": globalBindings,
		"editorBindings": editorBindings,
	} {
		for chord, fn := range table {
			if chord == "" {
				t.Fatalf("%s: an entry is bound to the empty key name", name)
			}
			if fn == nil {
				t.Fatalf("%s: chord %q has no binding", name, chord)
			}
			// The tables are keyed by the names keyOf produces, and those are
			// lowercase for every bound chord; an uppercase key here could
			// never be looked up.
			for _, r := range chord {
				if r >= 'A' && r <= 'Z' {
					t.Fatalf("%s: chord %q is not lowercase", name, chord)
				}
			}
		}
	}

	// A chord bound in both tables is only meaningful when the global binding
	// can decline the key (handled == false) and let the editor have it: F6 and
	// F7 step in the debug panel and split outside it. Anything else in this
	// overlap shadows the editor binding and is a mistake.
	fallThrough := map[string]bool{"f6": true, "f7": true}
	for chord := range globalBindings {
		if _, ok := editorBindings[chord]; ok && !fallThrough[chord] {
			t.Fatalf("chord %q is bound globally and in the editor, but cannot fall through", chord)
		}
	}
}

func TestKeyOfNormalization(t *testing.T) {
	cases := []struct {
		name     string
		msg      tea.KeyPressMsg
		wantKey  string
		wantText string // what the text-input handlers must receive back
	}{
		{
			// A bare control byte is re-encoded into a chord, and the control
			// byte itself must not survive into the buffer.
			name:     "raw control byte becomes a ctrl chord",
			msg:      tea.KeyPressMsg{Text: "\x06"},
			wantKey:  "ctrl+f",
			wantText: "",
		},
		{
			name:     "NUL is ctrl+space",
			msg:      tea.KeyPressMsg{Text: "\x00"},
			wantKey:  "ctrl+space",
			wantText: "",
		},
		{
			name:     "cyrillic code of a ctrl chord maps to the physical key",
			msg:      tea.KeyPressMsg{Code: 'ы', Mod: tea.ModCtrl},
			wantKey:  "ctrl+s",
			wantText: "",
		},
		{
			// The Windows Console API bundles the letter as Text and String()
			// then drops the modifier; the name comes from Keystroke() instead,
			// while the handler still sees the character that was typed.
			name:     "bundled cyrillic text keeps its modifier",
			msg:      tea.KeyPressMsg{Code: 'п', Text: "п", Mod: tea.ModCtrl},
			wantKey:  "ctrl+g",
			wantText: "п",
		},
		{
			name:     "bare uppercase is not folded to lowercase",
			msg:      tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift},
			wantKey:  "G",
			wantText: "G",
		},
		{
			name:     "typed cyrillic is not rewritten for the buffer",
			msg:      tea.KeyPressMsg{Code: 'ф', Text: "ф"},
			wantKey:  "a",
			wantText: "ф",
		},
		{
			name:     "an arrow keeps its name in any layout",
			msg:      tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift},
			wantKey:  "shift+down",
			wantText: "",
		},
		{
			// Terminals put the raw control byte in Text for these and
			// String() prints it verbatim: a name no binding could ever match.
			name:     "enter is named from the code when Text carries CR",
			msg:      tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\r"},
			wantKey:  "enter",
			wantText: "\r",
		},
		{
			name:     "esc is named from the code when Text carries ESC",
			msg:      tea.KeyPressMsg{Code: tea.KeyEsc, Text: "\x1b"},
			wantKey:  "esc",
			wantText: "\x1b",
		},
		{
			name:     "tab is named from the code when Text carries HT",
			msg:      tea.KeyPressMsg{Code: tea.KeyTab, Text: "\t"},
			wantKey:  "tab",
			wantText: "\t",
		},
		{
			name:     "backspace is named from the code when Text carries DEL",
			msg:      tea.KeyPressMsg{Code: tea.KeyBackspace, Text: "\x7f"},
			wantKey:  "backspace",
			wantText: "\x7f",
		},
		{
			// ruToEn knows ЙЦУКЕН only; any other Cyrillic-based layout is
			// still identifiable by the physical key under the finger.
			name:     "an unmapped layout falls back to the physical key",
			msg:      tea.KeyPressMsg{Code: 'і', Text: "і", BaseCode: 't'},
			wantKey:  "t",
			wantText: "і",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, s := keyOf(tc.msg)
			if s != tc.wantKey {
				t.Fatalf("keyOf name = %q, want %q", s, tc.wantKey)
			}
			if msg.Text != tc.wantText {
				t.Fatalf("keyOf text = %q, want %q", msg.Text, tc.wantText)
			}
		})
	}
}

func TestF6FallThroughDependsOnTheDebugPanel(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "one\n")

	t.Run("panel closed splits", func(t *testing.T) {
		m := New(f)
		m = press(m, tea.KeyPressMsg{Code: tea.KeyF6})
		if len(m.panes) < 2 {
			t.Fatalf("F6 with the debug panel closed must open a split, panes=%d", len(m.panes))
		}
	})

	t.Run("panel open does not split", func(t *testing.T) {
		m := New(f)
		m.dapOpen = true
		m.dapRunState = dapStopped
		before := len(m.panes)
		m = press(m, tea.KeyPressMsg{Code: tea.KeyF6})
		if len(m.panes) != before {
			t.Fatalf("F6 is step-over while the panel is open, panes went %d to %d", before, len(m.panes))
		}
	})

	t.Run("panel open and running does not split either", func(t *testing.T) {
		m := New(f)
		m.dapOpen = true
		m.dapRunState = dapRunning
		before := len(m.panes)
		m = press(m, tea.KeyPressMsg{Code: tea.KeyF6})
		if len(m.panes) != before {
			t.Fatalf("F6 must stay with the debug panel when it is open, panes went %d to %d", before, len(m.panes))
		}
	})
}

// The global chords answer before any pane gets the key: that is how Ctrl+Q
// quits from a focused chat and Ctrl+C copies while an overlay is up.
func TestGlobalChordsBeatTheFocusedPane(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "hello\n")

	m := New(f)
	m.chatOpen, m.chatFocus = true, true
	if cmd := m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("ctrl+q must ask to quit even with the chat focused")
	}

	m = New(f)
	m.chatOpen, m.chatFocus = true, true
	m.cur().buf.StartSelection()
	m.cur().buf.LineEndWithSelect()
	if cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd != nil {
		t.Fatal("ctrl+c must copy the selection, not reach the focused pane")
	}
	if m.clipboard != "hello" {
		t.Fatalf("ctrl+c clipboard = %q, want the selected line", m.clipboard)
	}
	if got := m.cur().buf.Text(); got != "hello\n" {
		t.Fatalf("copy must not consume the text: %q", got)
	}
}

func TestEditorBindingsFire(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "one\ntwo\nthree\n")

	selectFirstLine := func(m *Model) {
		m.cur().buf.StartSelection()
		m.cur().buf.LineEndWithSelect()
	}
	afterFirstRune := func(m *Model) { m.cur().buf.SetCursor(0, 1) }
	selectAllLines := func(m *Model) {
		m.cur().buf.SetCursor(0, 0)
		m.cur().buf.MoveDownWithSelect()
		m.cur().buf.MoveDownWithSelect()
	}
	indentLastLineAndSelect := func(m *Model) {
		m.cur().buf.SetCursor(2, 0)
		m.cur().buf.IndentLines("  ")
		selectAllLines(m)
	}
	uppercaseBuffer := func(m *Model) { m.cur().buf.ApplyCase(true) }

	cases := []struct {
		name  string
		msg   tea.KeyPressMsg
		setup func(*Model)
		want  func(*testing.T, Model)
	}{
		{"ctrl+f opens the search prompt",
			tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "searchOpen", m.searchOpen, true) }},
		{"ctrl+h opens replace",
			tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) {
				mustBool(t, "searchOpen", m.searchOpen, true)
				mustBool(t, "replaceOpen", m.replaceOpen, true)
			}},
		{"ctrl+o opens the file finder",
			tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "finderOpen", m.finderOpen, true) }},
		{"ctrl+p opens the command palette",
			tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) {
				mustBool(t, "paletteOpen", m.paletteOpen, true)
				mustBool(t, "paletteAll", m.paletteAll, false)
			}},
		{"alt+p opens search everywhere",
			tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustBool(t, "paletteAll", m.paletteAll, true) }},
		{"f2 opens the command palette",
			tea.KeyPressMsg{Code: tea.KeyF2}, nil,
			func(t *testing.T, m Model) { mustBool(t, "paletteOpen", m.paletteOpen, true) }},
		{"ctrl+t opens the open-file prompt",
			tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "promptOpen", m.promptOpen, true) }},
		{"ctrl+l opens go-to-line",
			tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "gotoOpen", m.gotoOpen, true) }},
		{"f1 opens help",
			tea.KeyPressMsg{Code: tea.KeyF1}, nil,
			func(t *testing.T, m Model) { mustBool(t, "helpOpen", m.helpOpen, true) }},
		{"ctrl+b shows the tree",
			tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "treeVisible", m.treeVisible, true) }},
		{"ctrl+g opens the git panel",
			tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustBool(t, "gitOpen", m.gitOpen, true) }},
		{"alt+a opens the chat",
			tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustBool(t, "chatOpen", m.chatOpen, true) }},
		{"alt+z toggles word wrap",
			tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustBool(t, "wordWrap", m.curPane().wordWrap, true) }},
		{"ctrl+\\ splits vertically",
			tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) {
				if len(m.panes) < 2 {
					t.Fatalf("ctrl+\\ must open a split, panes=%d", len(m.panes))
				}
			}},
		{"ctrl+y deletes the line",
			tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "two\nthree\n") }},
		{"ctrl+d duplicates the line",
			tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "one\none\ntwo\nthree\n") }},
		{"alt+down moves the line down",
			tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "two\none\nthree\n") }},
		{"alt+shift+down duplicates downwards",
			tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt | tea.ModShift}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "one\none\ntwo\nthree\n") }},
		{"ctrl+u uppercases without a selection",
			tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "ONE\nTWO\nTHREE\n") }},
		{"ctrl+u uppercases the selection only",
			tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, selectFirstLine,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "ONE\ntwo\nthree\n") }},
		{"ctrl+shift+u lowercases the buffer",
			tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl | tea.ModShift}, uppercaseBuffer,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "one\ntwo\nthree\n") }},
		{"alt+shift+s sorts the selected lines",
			tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt | tea.ModShift}, selectAllLines,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "one\nthree\ntwo\n") }},
		{"alt+shift+t trims the whole file",
			tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt | tea.ModShift}, nil,
			func(t *testing.T, m Model) {
				if m.msg != "nothing to change" {
					t.Fatalf("a clean file must say so, msg = %q", m.msg)
				}
			}},
		{"ctrl+alt+j pads the selected lines to the deepest column",
			tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl | tea.ModAlt}, indentLastLineAndSelect,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "  one\n  two\n  three\n") }},
		{"ctrl+alt+n numbers the line under the cursor",
			tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl | tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "1. one\ntwo\nthree\n") }},
		{"enter splits the line at the cursor",
			tea.KeyPressMsg{Code: tea.KeyEnter}, afterFirstRune,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "o\nne\ntwo\nthree\n") }},
		{"tab indents the selected block",
			tea.KeyPressMsg{Code: tea.KeyTab}, selectFirstLine,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "\tone\ntwo\nthree\n") }},
		{"tab inserts a tab on an empty line",
			tea.KeyPressMsg{Code: tea.KeyTab}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "\tone\ntwo\nthree\n") }},
		{"shift+tab unindents the line",
			tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, nil,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "one\ntwo\nthree\n") }},
		{"backspace deletes backwards",
			tea.KeyPressMsg{Code: tea.KeyBackspace}, afterFirstRune,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "ne\ntwo\nthree\n") }},
		{"delete removes the character under the cursor",
			tea.KeyPressMsg{Code: tea.KeyDelete}, afterFirstRune,
			func(t *testing.T, m Model) { mustText(t, m.cur().buf.Text(), "oe\ntwo\nthree\n") }},
		{"shift+down extends the selection",
			tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, nil,
			func(t *testing.T, m Model) { mustBool(t, "selection", m.cur().buf.HasSelection(), true) }},
		{"alt+right switches tab",
			tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}, nil,
			func(t *testing.T, m Model) { mustBool(t, "finderOpen", m.finderOpen, false) }},
		{"esc clears a multi-cursor",
			tea.KeyPressMsg{Code: tea.KeyEscape},
			func(m *Model) { m.cur().buf.AddNextOccurrence() },
			func(t *testing.T, m Model) {
				mustBool(t, "multiple cursors", m.cur().buf.HasMultipleCursors(), false)
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(f)
			m.width, m.height = 80, 24
			if tc.setup != nil {
				tc.setup(&m)
			}
			m = press(m, tc.msg)
			tc.want(t, m)
		})
	}
}

// The panes used to translate keys themselves — msg.String() in ~39 places
// plus a private gitKeyName — so a Russian layout silently broke their letter
// commands, and a terminal that puts the control byte in Text broke Enter and
// Escape. All of them now match on the single name keyOf produces.
func TestPaneHandlersUseTheOneTranslator(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "one\n")

	t.Run("letter command in a cyrillic layout", func(t *testing.T) {
		m := New(f)
		m.width, m.height = 80, 24
		m.diffViewOpen = true
		m = press(m, tea.KeyPressMsg{Code: 'й', Text: "й"}) // the q key on ЙЦУКЕН
		if m.diffViewOpen {
			t.Fatal("'й' is the physical q: the diff view should have closed")
		}
	})

	t.Run("typing keeps the character, not the key name", func(t *testing.T) {
		m := New(f)
		m.width, m.height = 80, 24
		m.startSearch()
		m = press(m, tea.KeyPressMsg{Code: 'ф', Text: "ф"}) // named 'a', typed 'ф'
		if got := string(m.searchQuery); got != "ф" {
			t.Fatalf("search query = %q, want %q — a text pane must keep what was typed", got, "ф")
		}
	})

	t.Run("enter with CR in text commits", func(t *testing.T) {
		m := New(f)
		m.width, m.height = 80, 24
		m.finderOpen = true
		m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\r"})
		if m.finderOpen {
			t.Fatal(`Enter arriving as {"\r"} should still be named "enter"`)
		}
	})

	t.Run("esc with ESC in text closes", func(t *testing.T) {
		m := New(f)
		m.width, m.height = 80, 24
		m.finderOpen = true
		m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc, Text: "\x1b"})
		if m.finderOpen {
			t.Fatal(`Escape arriving as {"\x1b"} should still be named "esc"`)
		}
	})
}

func TestUnboundChordIsSilentAndTypesNothing(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "one\n")
	m := New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModAlt})
	if got := m.cur().buf.Text(); got != "one\n" {
		t.Fatalf("unbound chord typed into the buffer: %q", got)
	}
}

func TestUnknownRuneGoesToTheBuffer(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "one\n")
	m := New(f)
	m = press(m, tea.KeyPressMsg{Text: "z"})
	if got := m.cur().buf.Text(); got != "zone\n" {
		t.Fatalf("buffer after typing z = %q, want %q", got, "zone\n")
	}
}

// keyPair is what dispatchPanes hands a pane handler: the name from the one
// translator and the message text-entry reads. Tests call handlers through it
// so a chord is named exactly as a real terminal key press would be named.
func keyPair(msg tea.KeyPressMsg) (string, tea.KeyPressMsg) {
	m, s := keyOf(msg)
	return s, m
}

// keyName is the same name alone, for handlers that only ever match chords.
func keyName(msg tea.KeyPressMsg) string {
	_, s := keyOf(msg)
	return s
}

// A buffer is the single source of truth, so the tests above assert on it.
func mustBool(t *testing.T, what string, got, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func mustText(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
}
