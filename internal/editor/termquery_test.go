package editor

import (
	"strings"
	"testing"
)

// cursorAt is a fixed caret position for the cursor-position report.
func cursorAt(row, col int) func() (int, int) {
	return func() (int, int) { return row, col }
}

func TestTermResponderAnswersCapabilityQueries(t *testing.T) {
	tests := []struct {
		name  string
		chunk string
		want  string
	}{
		// fish sends this before its first prompt and waits ten seconds for it.
		{"primary DA", "\x1b[c", "\x1b[?1;2c"},
		{"primary DA with zero", "\x1b[0c", "\x1b[?1;2c"},
		{"secondary DA", "\x1b[>c", "\x1b[>0;0;0c"},
		{"secondary DA with zero", "\x1b[>0c", "\x1b[>0;0;0c"},
		{"device status", "\x1b[5n", "\x1b[0n"},
		{"cursor position", "\x1b[6n", "\x1b[3;5R"},
		{"kitty keyboard flags", "\x1b[?u", "\x1b[?0u"},
		{"terminal version", "\x1b[>0q", "\x1bP>|dmed\x1b\\"},
		{"synchronised output mode", "\x1b[?2026$p", "\x1b[?2026;0$y"},
		{"background colour", "\x1b]11;?\x1b\\", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
		{"background colour, BEL", "\x1b]11;?\x07", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
		{"foreground colour", "\x1b]10;?\x07", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"},
		{"capability lookup", "\x1bP+q696e646e\x1b\\", "\x1bP0+r696e646e\x1b\\"},
		{"status string", "\x1bP$qm\x1b\\", "\x1bP0$rm\x1b\\"},
		// The same query after a full fish startup burst: the screen control
		// sequences around it must not hide it.
		{"fish startup burst",
			"\x1b[?u\x1b[>0q\x1b]11;?\x1b\\\x1b[?1049h\x1bP+q696e646e\x1b\\\x1b[?1049l\x1b[0c",
			"\x1b[?0u\x1bP>|dmed\x1b\\\x1b]11;rgb:0000/0000/0000\x1b\\\x1bP0+r696e646e\x1b\\\x1b[?1;2c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r termResponder
			if got := string(r.feed([]byte(tt.chunk), cursorAt(2, 4))); got != tt.want {
				t.Fatalf("feed(%q) = %q, want %q", tt.chunk, got, tt.want)
			}
		})
	}
}

func TestTermResponderIgnoresOrdinaryOutput(t *testing.T) {
	// Text, colours, cursor moves and a window title are not queries: a
	// terminal replies to none of them, and a spurious reply would be typed
	// into whatever program owns the screen.
	chunks := []string{
		"plain text",
		"\x1b[31mred\x1b[0m",
		"\x1b[2;3Hmoved",
		"\x1b]0;window title\x07",
		"\x1b[?1049h",
		"\x1b[1;1R", // a cursor report the child itself printed
		"\x1b[>4;2m",
		"\x1b(B",
	}
	var r termResponder
	for _, c := range chunks {
		if got := r.feed([]byte(c), cursorAt(0, 0)); len(got) != 0 {
			t.Fatalf("feed(%q) = %q, want no reply", c, got)
		}
	}
}

func TestTermResponderJoinsQueriesSplitAcrossReads(t *testing.T) {
	var r termResponder
	if got := r.feed([]byte("\x1b[0"), cursorAt(0, 0)); len(got) != 0 {
		t.Fatalf("incomplete query answered early: %q", got)
	}
	if got := string(r.feed([]byte("c"), cursorAt(0, 0))); got != "\x1b[?1;2c" {
		t.Fatalf("split query = %q, want the primary DA reply", got)
	}
	// Back-to-back queries in one read are all answered, in order.
	if got := string(r.feed([]byte("\x1b[5n\x1b[6n"), cursorAt(0, 0))); got != "\x1b[0n\x1b[1;1R" {
		t.Fatalf("two queries = %q", got)
	}
}

func TestTermResponderDropsOverlongTail(t *testing.T) {
	var r termResponder
	// A huge unfinished OSC is not a query; holding it forever would keep the
	// responder from ever resynchronising.
	huge := "\x1b]" + strings.Repeat("x", termQueryTail+10)
	if got := r.feed([]byte(huge), cursorAt(0, 0)); len(got) != 0 {
		t.Fatalf("overlong tail answered: %q", got)
	}
	if len(r.tail) != 0 {
		t.Fatalf("kept %d bytes of an overlong tail", len(r.tail))
	}
}
