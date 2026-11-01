package editor

import (
	"strings"
	"testing"
	"time"

	"dmed/internal/vcs"
)

// Pure geometry tests for the view layer: these run without a terminal and
// guard the math every frame relies on (wrap segments, column mapping,
// padding and truncation helpers). The TUI itself stays verified by hand —
// see AGENTS.md.

func TestWrapLine(t *testing.T) {
	runs := []struct {
		name string
		ln   int
		exp  []rune
		w    int
		want []wrapSeg // want.line == -1 marks "any line" (only offsets checked)
	}{
		{"short line fits", 0, []rune("hi"), 10,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 2}}},
		{"exact fit", 0, []rune("hello"), 5,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 5}}},
		{"hard chunk without spaces", 0, []rune("abcdefghij"), 4,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 4},
				{line: -1, expStart: 4, expEnd: 8},
				{line: -1, expStart: 8, expEnd: 10}}},
		{"prefer breaking after a space", 0, []rune("hello world"), 8,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 6},
				{line: -1, expStart: 6, expEnd: 11}}},
		{"long first word then space", 0, []rune("abcdefgh def"), 8,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 8},
				{line: -1, expStart: 8, expEnd: 12}}},
		{"consecutive spaces", 0, []rune("a  b cdef"), 4,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 3},
				{line: -1, expStart: 3, expEnd: 5},
				{line: -1, expStart: 5, expEnd: 9}}},
		{"width clamped to one", 0, []rune("abc"), 0,
			[]wrapSeg{{line: -1, expStart: 0, expEnd: 1},
				{line: -1, expStart: 1, expEnd: 2},
				{line: -1, expStart: 2, expEnd: 3}}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			got := wrapLine(r.ln, r.exp, r.w)
			if len(got) != len(r.want) {
				t.Fatalf("wrapLine(%q, w=%d): got %d segs %+v, want %d",
					string(r.exp), r.w, len(got), got, len(r.want))
			}
			for i, want := range r.want {
				g := got[i]
				if want.line != -1 && g.line != want.line {
					t.Fatalf("seg %d line = %d, want %d", i, g.line, want.line)
				}
				if g.expStart != want.expStart || g.expEnd != want.expEnd {
					t.Fatalf("seg %d = %d..%d, want %d..%d (%+v)",
						i, g.expStart, g.expEnd, want.expStart, want.expEnd, g)
				}
				// Every segment must map back to real text: start < end and
				// never past the expanded line.
				if g.expStart >= g.expEnd || g.expEnd > len(r.exp) {
					t.Fatalf("seg %d out of bounds: %+v", i, g)
				}
			}
		})
	}
}

func TestWrapLineCarriesLineNumbers(t *testing.T) {
	// tabWrap concatenates the segments of several buffer lines; wrapLine
	// must tag them so segRowForCol can tell which screen row belongs to
	// which buffer line.
	got := wrapLine(7, []rune("aa bb cc dd"), 5)
	if len(got) < 2 {
		t.Fatalf("expected several segments, got %+v", got)
	}
	for _, s := range got {
		if s.line != 7 {
			t.Fatalf("segment carries line %d, want 7: %+v", s.line, s)
		}
	}
}

func TestSegRowForCol(t *testing.T) {
	segs := []wrapSeg{
		{line: 0, expStart: 0, expEnd: 5},
		{line: 0, expStart: 5, expEnd: 9},
		{line: 1, expStart: 0, expEnd: 4},
		{line: 1, expStart: 4, expEnd: 7},
	}
	tests := []struct {
		name            string
		line, expCol    int
		wantRow, wantSt int
	}{
		{"first column of the line", 0, 0, 0, 0},
		{"last segment boundary is exclusive", 0, 5, 1, 5},
		{"middle of the tail segment", 0, 7, 1, 5},
		{"past the end of a mid line falls to its last segment", 0, 9, 1, 5},
		{"another line hits its own segment", 1, 0, 2, 0},
		{"far past the end falls to the same line's last segment", 1, 100, 3, 4},
		{"unknown line is the zero vector", 9, 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, st := segRowForCol(segs, tc.line, tc.expCol)
			if row != tc.wantRow || st != tc.wantSt {
				t.Fatalf("segRowForCol(line=%d, col=%d) = (%d, %d), want (%d, %d)",
					tc.line, tc.expCol, row, st, tc.wantRow, tc.wantSt)
			}
		})
	}
}

func TestExpandTabs(t *testing.T) {
	// Mixed tabs and text: each tab becomes exactly tw spaces.
	if got := string(expandTabs([]rune("a\tb"), 4)); got != "a    b" {
		t.Fatalf("expandTabs single = %q", got)
	}
	if got := string(expandTabs([]rune("\t\t"), 2)); got != "    " {
		t.Fatalf("expandTabs double = %q", got)
	}
	if got := string(expandTabs([]rune("plain"), 8)); got != "plain" {
		t.Fatalf("expandTabs passthrough = %q", got)
	}
	if got := expandTabs([]rune(""), 4); len(got) != 0 {
		t.Fatalf("expandTabs empty = %q", string(got))
	}
}

func TestVisCol(t *testing.T) {
	// "a\tbc": the tab costs tw cells, everything else one.
	line := []rune("a\tbc")
	if got := visCol(line, 1, 4); got != 1 {
		t.Fatalf("visCol col=1 = %d", got)
	}
	if got := visCol(line, 2, 4); got != 5 {
		t.Fatalf("visCol col=2 (past tab) = %d", got)
	}
	if got := visCol(line, 4, 4); got != 7 {
		t.Fatalf("visCol col=4 (whole line) = %d", got)
	}
	// Column mapping is by rune, not by byte.
	line = []rune("é\t")
	if got := visCol(line, 2, 4); got != 5 {
		t.Fatalf("visCol rune col=2 = %d", got)
	}
}

func TestPadTo(t *testing.T) {
	if got := padTo("ab", 5); got != "ab   " {
		t.Fatalf("padTo short = %q", got)
	}
	if got := padTo("abcdef", 3); got != "abcdef" {
		t.Fatalf("padTo long = %q", got)
	}
	if got := padTo("", 2); got != "  " {
		t.Fatalf("padTo empty = %q", got)
	}
	if got := padTo("ab", 2); got != "ab" {
		t.Fatalf("padTo exact = %q", got)
	}
}

func TestFitPath(t *testing.T) {
	m := Model{g: unicodeGlyphs}
	if got := m.fitPath("short.go", 20); got != "short.go" {
		t.Fatalf("within width: %q", got)
	}
	if got := m.fitPath("short", len("short")); got != "short" {
		t.Fatalf("exact fit: %q", got)
	}
	const path = "very/long/directory/structure/main.go"
	got := m.fitPath(path, 12)
	if len([]rune(got)) != 12 {
		t.Fatalf("fitPath width = %d runes, want 12: %q", len([]rune(got)), got)
	}
	if !strings.HasPrefix(got, m.g.ellipsis) {
		t.Fatalf("fitPath must start with the ellipsis: %q", got)
	}
	// The readable tail survives: the file name and its extension.
	if !strings.HasSuffix(got, "ain.go") {
		t.Fatalf("fitPath tail = %q, want the readable end", got)
	}
	if got := m.fitPath(path, 0); got != "" {
		t.Fatalf("fitPath w=0 = %q, want empty", got)
	}
}

func TestBlameLabel(t *testing.T) {
	m := Model{g: unicodeGlyphs}
	d := time.Now().Add(-48 * time.Hour)
	if got, want := m.blameLabel(vcs.BlameLine{Author: "Alice <alice@example.com>", Date: d}),
		"Alice · 2d"; got != want {
		t.Fatalf("blameLabel author = %q, want %q", got, want)
	}
	if got, want := m.blameLabel(vcs.BlameLine{Author: "", Date: d}), "? · 2d"; got != want {
		t.Fatalf("blameLabel empty author = %q, want %q", got, want)
	}
	long := "Averyveryverylongauthorname"
	if got := m.blameLabel(vcs.BlameLine{Author: long + " <a@b>", Date: d}); !strings.HasPrefix(got, long[:12]) {
		t.Fatalf("blameLabel truncation = %q, want a %d-rune author", got, 12)
	}
}

func TestRelWhen(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want string
	}{
		{"future is now", -time.Hour, "now"},
		{"under a minute", 40 * time.Second, "now"},
		{"minutes", 2*time.Minute + 10*time.Second, "2m"},
		{"hours", 3 * time.Hour, "3h"},
		{"days", 48 * time.Hour, "2d"},
		{"months", 45 * 24 * time.Hour, "1mo"},
		{"years", 400 * 24 * time.Hour, "1y"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := relWhen(time.Now().Add(-tc.age)); got != tc.want {
				t.Fatalf("relWhen(-%v) = %q, want %q", tc.age, got, tc.want)
			}
		})
	}
}

func TestViewHeightBaseMath(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "v.txt", "line\n")
	m := New(f)

	// The base contract: the viewport is the window minus the gutter
	// status rows, with no docked or overlay panels open.
	m.width, m.height = 80, 24
	if got, want := m.viewHeight(), 22; got != want {
		t.Fatalf("viewHeight(80x24) = %d, want %d", got, want)
	}

	// Docked panels subtract their rows from the viewport.
	m.finderOpen = true
	m.finderHits = []string{"a.go", "b.go", "c.go"}
	m.height = 12
	if got, want := m.viewHeight(), 12-2-(3+2); got != want {
		t.Fatalf("viewHeight with finder = %d, want %d", got, want)
	}

	// The height never goes below one line, however small the terminal.
	m.height = 1
	if got := m.viewHeight(); got != 1 {
		t.Fatalf("viewHeight(1) = %d, want the 1-line clamp", got)
	}
}
