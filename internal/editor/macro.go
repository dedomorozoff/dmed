package editor

import (
	"charm.land/bubbletea/v2"
)

// Macros: record a run of text-pane keys and play it back.
//
// Alt+R starts and stops the recording, Alt+Shift+R replays. Only the keys
// that reach the *text pane* are recorded — the editor bindings and the
// typing fallback. The global chords (quit, copy, panels) and the pane/overlay
// keys are run as usual while recording but never land in the macro: a macro
// that swallowed Ctrl+Q would be a trap, and one recorded over the chat panel
// would replay into whatever has focus at the time.
//
// Playback feeds every recorded key through dispatchKey — the very chain the
// live keys ran through — so a replay respects multi-cursor, smart indent and
// the column edits by construction, not by re-implementation. One key runs per
// UI step (a tick chain, like the GIF frames), which keeps a long macro from
// freezing a frame.
//
// The recording lives in the Model and is not persisted: a macro is a
// keyboard memory of a few seconds, not a session artifact.

// macroKey is one recorded key: the normalized name plus the original message
// (typing replays through msg.Text, so the character recorded is the
// character replayed).
type macroKey struct {
	s   string
	msg tea.KeyPressMsg
}

// macroReplayMsg ticks the replay one key per step.
type macroReplayMsg struct {
	idx int
}

// toggleMacroRecording starts or stops the recording. Stopping an empty
// recording (a stray Alt+R) discards it rather than replaying nothing.
func (m *Model) toggleMacroRecording() {
	if m.macroRecording {
		m.macroRecording = false
		if len(m.macroKeys) == 0 {
			m.macro = nil
			m.msg = m.t("msg.macro_empty")
			return
		}
		m.macro = append([]macroKey(nil), m.macroKeys...)
		m.macroKeys = nil
		m.msg = m.t("msg.macro_stopped")
		return
	}
	m.macroRecording = true
	m.macroKeys = nil
	m.macro = nil
	m.msg = m.t("msg.macro_recording")
}

// replayMacro replays the recorded keys. A replay pressed while recording is
// the stop-then-replay the user almost certainly means; without a macro there
// is nothing to do and the status line says so. The returned command arms the
// first step of the tick chain.
func (m *Model) replayMacro() tea.Cmd {
	if m.macroRecording {
		m.toggleMacroRecording()
	}
	if len(m.macro) == 0 {
		m.msg = m.t("msg.macro_none")
		return nil
	}
	m.msg = ""
	m.complOpen = false
	m.replaying = true
	return macroTick(0)
}

// macroDispatch is the recording-aware dispatch for a key that survived the
// macro chords: the panes and overlays answer first as they always do (a
// prompt opened mid-recording must work) but are not recorded; only keys the
// text pane consumes land in the macro.
func (m *Model) macroDispatch(s string, msg tea.KeyPressMsg) tea.Cmd {
	if cmd, handled := m.dispatchPanes(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.handleTailBindings(s, msg); handled {
		return cmd
	}
	if cmd, handled := m.runBinding(editorBindings, s, msg); handled {
		m.macroKeys = append(m.macroKeys, macroKey{s: s, msg: msg})
		return cmd
	}
	if len(msg.Text) > 0 {
		cmd := m.typingFallback(msg)
		m.macroKeys = append(m.macroKeys, macroKey{s: s, msg: msg})
		return cmd
	}
	return nil
}

// replayStep runs one recorded key and arms the next tick. Repeats collapse
// at record time (macroRecord skips them), so a replay never needs the
// repeat flag.
func (m *Model) replayStep(i int) tea.Cmd {
	if !m.replaying {
		return nil
	}
	if i >= len(m.macro) {
		m.replaying = false
		return nil
	}
	k := m.macro[i]
	cmd := m.dispatchKey(k.s, k.msg)
	if i+1 < len(m.macro) {
		return tea.Batch(cmd, macroTick(i+1))
	}
	m.replaying = false
	return cmd
}

func macroTick(i int) tea.Cmd {
	return func() tea.Msg { return macroReplayMsg{idx: i} }
}
