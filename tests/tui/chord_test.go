package tui_test

import (
	"strings"
	"testing"
)

// The static half of the chord table. A control byte and ESC+letter are the
// encodings every terminal agrees on, so the scenarios are built from them;
// the function below derives ctrl+<letter> and alt+<letter> instead of
// spelling out forty map entries.
var chordTable = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"esc":       "\x1b",
	"tab":       "\t",
	"shift+tab": "\x1b[Z",
	"backspace": "\x7f",
	"delete":    "\x1b[3~",
	"space":     " ",

	"up":    "\x1b[A",
	"down":  "\x1b[B",
	"right": "\x1b[C",
	"left":  "\x1b[D",
	"home":  "\x1b[H",
	"end":   "\x1b[F",
	"pgup":  "\x1b[5~",

	"pgdown":      "\x1b[6~",
	"shift+up":    "\x1b[1;2A",
	"shift+down":  "\x1b[1;2B",
	"shift+left":  "\x1b[1;2D",
	"shift+right": "\x1b[1;2C",
}

// encodeChord turns a dmed binding name into the bytes a terminal sends for it.
// Unknown names are refused rather than guessed at: a silently wrong byte
// string would make a scenario pass for the wrong reason.
func encodeChord(name string) ([]byte, bool) {
	if s, ok := chordTable[name]; ok {
		return []byte(s), true
	}
	if name == "ctrl+space" {
		// The editor names NUL ctrl+space; completion is the only consumer.
		return []byte{0}, true
	}
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len(rest) == 1 {
		c := rest[0]
		if c >= 'a' && c <= 'z' {
			return []byte{c - 'a' + 1}, true
		}
		if c >= 'A' && c <= 'Z' {
			// Ctrl+A..Ctrl+Z share one control byte with their lowercase
			// spelling; a terminal cannot tell them apart either.
			return []byte{c - 'A' + 1}, true
		}
		return nil, false
	}
	if rest, ok := strings.CutPrefix(name, "alt+"); ok && len(rest) > 0 {
		r := []rune(rest)
		if len(r) == 1 && r[0] >= 0x20 && r[0] < 0x7f {
			return []byte("\x1b" + rest), true
		}
		return nil, false
	}
	return nil, false
}

func TestEncodeChord(t *testing.T) {
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		// The chords every scenario in this package is built from: control
		// bytes survive a pty unchanged, which is why the harness prefers
		// them over F-keys and three-modifier combinations.
		{"ctrl+q", "\x11", true},
		{"ctrl+s", "\x13", true},
		{"ctrl+p", "\x10", true},
		{"ctrl+o", "\x0f", true},
		{"ctrl+c", "\x03", true},
		{"ctrl+space", "\x00", true},
		{"enter", "\r", true},
		{"esc", "\x1b", true},
		{"alt+t", "\x1bt", true},
		{"shift+tab", "\x1b[Z", true},
		{"down", "\x1b[B", true},
		{"ctrl+shift+f", "", false},
		{"ctrl+1", "", false},
		{"f1", "", false},
		{"nonsense", "", false},
	}
	for _, tc := range cases {
		got, ok := encodeChord(tc.name)
		if ok != tc.ok {
			t.Errorf("encodeChord(%q) ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if ok && string(got) != tc.want {
			t.Errorf("encodeChord(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
