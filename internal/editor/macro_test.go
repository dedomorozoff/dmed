package editor

import (
	"testing"

	"charm.land/bubbletea/v2"
)

// driveMacro runs the replay tick chain the way the runtime would, until the
// chain dies. Batched commands are unwrapped (the tick travels in a Batch
// with whatever command the step's key produced).
func driveMacro(m Model, cmd tea.Cmd) Model {
	for i := 0; i < 1000 && cmd != nil; i++ {
		msg := cmd()
		switch mr := msg.(type) {
		case macroReplayMsg:
			next, nextCmd := m.Update(mr)
			m = next.(Model)
			cmd = nextCmd
		case tea.BatchMsg:
			for _, c := range mr {
				if c != nil {
					m = driveMacro(m, c)
				}
			}
			return m
		default:
			return m
		}
	}
	return m
}

func TestMacroRecordReplay(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	// Record: type "hi", duplicate the line.
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if !m.macroRecording {
		t.Fatal("Alt+R did not start recording")
	}
	m = typeStr(m, "hi")
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if m.macroRecording {
		t.Fatal("Alt+R did not stop recording")
	}
	if len(m.macro) == 0 {
		t.Fatal("recording captured nothing")
	}
	if got := m.cur().buf.Text(); got != "hi\nhi\n" {
		t.Fatalf("recorded run produced %q", got)
	}

	// Replay onto a fresh buffer: the same keys do the same work.
	m2 := New()
	m2.width, m2.height = 80, 24
	m2.macro = m.macro
	m2 = driveMacro(m2, m2.replayMacro())
	if m2.replaying {
		t.Fatal("replay did not finish")
	}
	if got := m2.cur().buf.Text(); got != "hi\nhi\n" {
		t.Fatalf("replay produced %q, want %q", got, "hi\nhi\n")
	}
}

func TestMacroIgnoresGlobalAndPaneKeys(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	m = press(m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}) // git panel chord
	if len(m.macroKeys) != 0 {
		t.Fatal("a global chord landed in the macro")
	}
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if len(m.macro) != 0 {
		t.Fatalf("global chord recorded: %d keys", len(m.macro))
	}
}

func TestMacroEmptyRecordingDiscarded(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if len(m.macro) != 0 {
		t.Fatal("empty recording must be discarded")
	}
	if m.msg != m.t("msg.macro_empty") {
		t.Fatalf("status = %q", m.msg)
	}
	// Replaying nothing says so instead of doing nothing silently.
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModShift | tea.ModAlt})
	if m.msg != m.t("msg.macro_none") {
		t.Fatalf("status = %q", m.msg)
	}
}

func TestMacroReplayWhileRecordingStopsThenPlays(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	m = typeStr(m, "x")
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModShift | tea.ModAlt})
	if m.macroRecording {
		t.Fatal("replay did not stop the recording")
	}
	if len(m.macro) == 0 {
		t.Fatal("the stopped recording was lost instead of replayed")
	}
	m = driveMacro(m, macroTick(0))
	if m.replaying {
		t.Fatal("replay did not finish")
	}
	if got := m.cur().buf.Text(); got != "xx\n" {
		t.Fatalf("replay produced %q, want %q", got, "xx\n")
	}
}

func TestMacroMultiCursorRespected(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	// Record: jump to line start, select the word under the caret, add a
	// cursor on the next occurrence, insert "!" into both. Alt+D's first
	// press selects the word, the second one adds the cursor.
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	m = typeStr(m, "ab ab")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyHome})
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	if !m.cur().buf.HasMultipleCursors() {
		t.Fatalf("setup: expected two cursors, got %d", len(m.cur().buf.Cursors()))
	}
	m = typeStr(m, "!")
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	if got := m.cur().buf.Text(); got != "! !\n" {
		t.Fatalf("recorded multi-cursor run: %q", got)
	}
	if len(m.macro) == 0 {
		t.Fatal("nothing recorded")
	}

	// Replay the same macro on a fresh buffer: it carries its own typing.
	m2 := New()
	m2.width, m2.height = 80, 24
	m2.macro = m.macro
	m2 = driveMacro(m2, m2.replayMacro())
	if got := m2.cur().buf.Text(); got != "! !\n" {
		t.Fatalf("replayed multi-cursor run: %q, want %q", got, "! !\n")
	}
}
