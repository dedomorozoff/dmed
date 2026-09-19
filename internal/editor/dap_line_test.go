package editor

import (
	"strings"
	"testing"
)

// dapExecLineBg is the ANSI background escape for the current debug line. It is
// checked against individual rendered screen rows so the status bar's own
// 236-colour background does not produce a false positive.
const dapExecLineBg = "\x1b[48;5;236m"

func TestDapCurrentLineHighlight(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "s.txt", "one\ntwo\nthree\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.dapOpen = false
	m.dapCurPath = m.cur().path
	m.dapRunState = dapStopped
	m.dapCurLine = 2 // 1-based -> renders 0-based ln 1 ("two").

	v := m.View()
	if row := debugLineRow(v.Content, "two"); !strings.Contains(row, dapExecLineBg) {
		t.Fatalf("stopped line must carry the execution-point background:\n%q", row)
	}
	if row := debugLineRow(v.Content, "three"); row != "" && strings.Contains(row, dapExecLineBg) {
		t.Fatalf("non-stopped line must not be highlighted:\n%q", row)
	}

	// No highlight while running or after the session ends.
	for _, state := range []string{dapRunning, dapEnded} {
		m.dapRunState = state
		v = m.View()
		if row := debugLineRow(v.Content, "two"); row != "" && strings.Contains(row, dapExecLineBg) {
			t.Fatalf("no highlight in state %q:\n%q", state, row)
		}
	}
}

// debugLineRow returns the rendered screen row that contains target (ANSI
// escapes are stripped for the match), or "" when no row holds it.
func debugLineRow(content, target string) string {
	for _, row := range strings.Split(content, "\n") {
		if strings.Contains(ansiRe.ReplaceAllString(row, ""), target) {
			return row
		}
	}
	return ""
}

