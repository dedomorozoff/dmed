package syntax

import (
	"strings"
	"testing"
)

func linesEqual(a, b []HighlightedLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !incTokensEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// full is the reference: a from-scratch highlight of the whole text.
func full(h *Highlighter, file, text string) []HighlightedLine {
	return h.HighlightBuffer(file, text)
}

func TestIncrementalMatchesFull(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	orig := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	ic := NewIncremental()
	ic.Update(file, orig, h)

	// Edit the Println line: everything else is unchanged text, so the
	// incremental result must equal a full re-tokenize exactly.
	edited := strings.Replace(orig, "Println", "Printf", 1)
	got, _ := ic.Update(file, edited, h)
	want := full(h, file, edited)
	if !linesEqual(got, want) {
		t.Fatalf("incremental != full after an in-line edit")
	}

	// Insert a new line in the middle: the tail shifts down, the re-sync must
	// still converge.
	edited = orig[:len(orig)-len("\tfmt.Println(\"hi\")\n}\n")] + "\tx := 1\n\ty := 2\n}\n"
	got, _ = ic.Update(file, edited, h)
	want = full(h, file, edited)
	if !linesEqual(got, want) {
		t.Fatalf("incremental != full after inserting lines")
	}

	// Delete a line.
	edited = "package main\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	got, _ = ic.Update(file, edited, h)
	want = full(h, file, edited)
	if !linesEqual(got, want) {
		t.Fatalf("incremental != full after deleting a line")
	}
}

// TestIncrementalMultilineShift is the case the re-sync window exists for: a
// block comment opens on the edited line and swallows what used to be plain
// code below it. Lines whose text is unchanged must still re-tokenize, and
// the result must match a full pass.
func TestIncrementalMultilineShift(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	orig := "package main\n\nfunc main() {\n\ta := 1\n\tb := 2\n\tc := 3\n\td := 4\n\te := 5\n}\n"
	ic := NewIncremental()
	if _, reused := ic.Update(file, orig, h); reused != 0 {
		t.Fatal("cold cache reused")
	}

	// Open a block comment on line 2: lines 3..7 keep their text but must
	// change tokens (comment content), and the comment closes on line 8.
	edited := "package main\n\n/* open\n\ta := 1\n\tb := 2\n\tc := 3\n\td := 4\n\te := 5 */\n}\n"
	got, _ := ic.Update(file, edited, h)
	want := full(h, file, edited)
	if !linesEqual(got, want) {
		t.Fatalf("incremental != full after a block comment shift")
	}
}

// TestIncrementalReuseCounts checks that the cache actually reuses: the
// second update of unchanged text reuses every line, and an edit reuses the
// prefix.
func TestIncrementalReuseCounts(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	text := "package main\n\nfunc main() {\n\ta := 1\n}\n"
	ic := NewIncremental()
	ic.Update(file, text, h)
	_, reused := ic.Update(file, text, h)
	if reused < 5 {
		t.Fatalf("unchanged text reused only %d lines, want >= 5", reused)
	}

	edited := "package main\n\nfunc main() {\n\ta := 2 // changed\n}\n"
	_, reused = ic.Update(file, edited, h)
	if reused == 0 {
		t.Fatal("an edit in the tail reused nothing")
	}
}

// TestIncrementalUnclosedString is the nasty case: an unterminated string
// changes the state of everything below it, and the re-sync window must not
// trust lines that only look the same.
func TestIncrementalUnclosedString(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	orig := "package main\n\nfunc main() {\n\ta := \"ok\"\n\tb := 1\n\tc := 2\n}\n"
	ic := NewIncremental()
	ic.Update(file, orig, h)

	edited := "package main\n\nfunc main() {\n\ta := \"unterminated\n\tb := 1\n\tc := 2\n}\n"
	got, _ := ic.Update(file, edited, h)
	want := full(h, file, edited)
	if !linesEqual(got, want) {
		t.Fatalf("incremental != full after an unterminated string")
	}
}

func TestIncrementalReset(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	ic := NewIncremental()
	ic.Update(file, "package main\n", h)
	ic.Reset()
	_, reused := ic.Update(file, "package main\n", h)
	if reused != 0 {
		t.Fatalf("reset cache reused %d lines", reused)
	}
}

func TestIncrementalEmptyAndTrailing(t *testing.T) {
	h := New("monokai")
	file := "a.go"
	ic := NewIncremental()
	// Empty text: one empty line (strings.Split semantics).
	got, _ := ic.Update(file, "", h)
	if len(got) != 1 {
		t.Fatalf("empty text: %d lines, want 1", len(got))
	}
	// Trailing newline: a final empty line, the shape HighlightBuffer has.
	got, _ = ic.Update(file, "a\n", h)
	if len(got) != 2 || len(got[0]) != 1 {
		t.Fatalf("trailing newline: %d lines, want 2 with a 1-token first line", len(got))
	}
}
