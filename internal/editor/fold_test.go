package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
)

const foldSrc = "func main() {\n" + // start of block 0..3
	"\tif x {\n" + // start of the nested block 1..2
	"\t\tdo()\n" + // hidden while block 0 is folded
	"\t}\n" + // still inside block 0
	"}\n" + // closes block 0
	"\n" + // blank: nothing foldable
	"after()\n" // outside any block

func foldModel(t *testing.T) Model {
	t.Helper()
	m := New()
	m.width, m.height = 60, 20
	m.tabs = []tab{{buf: buffer.Load(foldSrc)}}
	return m
}

func foldPlain(m Model) string {
	return strings.Join(plainRows(m.View().Content), "\n")
}

func TestFoldRanges(t *testing.T) {
	m := foldModel(t)
	b := &m.tabs[0]
	rs := b.foldRanges(4)
	if len(rs) != 2 || rs[0] != (foldRange{start: 0, end: 3}) || rs[1] != (foldRange{start: 1, end: 2}) {
		t.Fatalf("foldRanges = %+v, want [{0 3} {1 2}]", rs)
	}

	// Folding the outer block hides its inner lines, including the nested one.
	b.folds = map[int]bool{0: true, 1: true}
	if st, en, hid := b.foldHidden(2, 4); !hid || st != 0 || en != 3 {
		t.Fatalf("line 2 must be hidden by the outer block, got %d..%d hid=%v", st, en, hid)
	}

	// The fold start itself stays visible; the line below the block too.
	if _, _, hid := b.foldHidden(0, 4); hid {
		t.Fatal("fold start must stay visible")
	}
	if _, _, hid := b.foldHidden(4, 4); hid {
		t.Fatal("the line after the block must stay visible")
	}

	// Blank lines never start a fold.
	if _, ok := b.foldAtStart(5, 4); ok {
		t.Fatal("a blank line must not start a fold")
	}
}

// TestFoldRangesBlocks pins the block scan: siblings, blank lines inside a
// block and nested blocks all have to come out with the same ranges.
func TestFoldRangesBlocks(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []foldRange
	}{
		{
			name:  "siblings",
			lines: []string{"a", "  b", "  c", "d"},
			want:  []foldRange{{start: 0, end: 2}},
		},
		{
			name:  "blank lines inside the block",
			lines: []string{"a", "  b", "", "  c", "d"},
			want:  []foldRange{{start: 0, end: 3}},
		},
		{
			name:  "nested blocks",
			lines: []string{"if x {", "\tif y {", "\t\tz", "\t}", "}"},
			want:  []foldRange{{start: 0, end: 3}, {start: 1, end: 2}},
		},
		{
			name:  "a block without a body is not foldable",
			lines: []string{"a", "b", "  c"},
			want:  []foldRange{{start: 1, end: 2}},
		},
		{
			name:  "no block at all",
			lines: []string{"a", "b", "c"},
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			m.tabs = []tab{{buf: buffer.Load(strings.Join(tc.lines, "\n") + "\n")}}
			got := m.tabs[0].foldRanges(4)
			if len(got) != len(tc.want) {
				t.Fatalf("foldRanges = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("foldRanges = %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}

// TestFoldMarkerHealsItself: when the buffer is edited so that a folded
// block stops being a block, the folded marker must not survive it — it would
// hide nothing while making the folded count lie.
func TestFoldMarkerHealsItself(t *testing.T) {
	m := New()
	m.width, m.height = 60, 20
	m.tabs = []tab{{buf: buffer.Load("func main() {\n\tdo()\n}\n")}}
	b := &m.tabs[0]
	b.folds = map[int]bool{0: true}
	if _, _, hid := b.foldHidden(1, 4); !hid {
		t.Fatal("the block must be folded to start with")
	}
	// Pull the body line back to the outer level: the block is gone.
	b.buf.ReplaceRange(1, 0, 1, []rune(""))
	if rs := b.foldRanges(4); len(rs) != 0 {
		t.Fatalf("the block must be gone, ranges=%+v", rs)
	}
	if len(b.folds) != 0 {
		t.Fatalf("the stale folded marker must be dropped, folds=%v", b.folds)
	}
}

func TestFoldRenderToggleAndChevron(t *testing.T) {
	m := foldModel(t)
	plain := foldPlain(m)
	if !strings.Contains(plain, "do()") {
		t.Fatalf("unfolded view must show the buffer:\n%s", plain)
	}
	if !strings.Contains(plain, "▾") {
		t.Fatalf("unfolded foldable blocks must carry the ▾ chevron:\n%s", plain)
	}

	m.tabs[0].folds = map[int]bool{0: true}
	plain = foldPlain(m)
	if strings.Contains(plain, "do()") {
		t.Fatalf("folded lines must not be rendered:\n%s", plain)
	}
	if !strings.Contains(plain, "3 lines hidden") {
		t.Fatalf("folded block must render a hidden-lines summary:\n%s", plain)
	}
	if !strings.Contains(plain, "func main() {") {
		t.Fatal("fold start stays visible")
	}
	if !strings.Contains(plain, "after()") {
		t.Fatalf("lines after the block must stay visible:\n%s", plain)
	}

	m.unfoldAllTabsForTest()
	if plain := foldPlain(m); !strings.Contains(plain, "do()") {
		t.Fatalf("unfolding must bring the lines back:\n%s", plain)
	}
}

// unfoldAllTabsForTest is a helper: fold.go offers it through toggleFoldAll
// on the model, but a plain reset keeps the render test focused.
func (m *Model) unfoldAllTabsForTest() {
	for i := range m.tabs {
		m.tabs[i].unfoldAll()
	}
}

func TestFoldSkipOnArrows(t *testing.T) {
	m := foldModel(t)
	m.tabs[0].folds = map[int]bool{0: true}

	// Down from the block's start stays out of hidden lines.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.tabs[0].buf.CurLine(); got != 4 {
		t.Fatalf("down over a folded block must land at line 4, got %d", got)
	}

	// Up again must not re-enter the block.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.tabs[0].buf.CurLine(); got != 0 {
		t.Fatalf("up over a folded block must land back at line 0, got %d", got)
	}
}

// TestFoldPgdownOverFold walks a page down and must not stop in the middle of
// a folded block.
func TestFoldPgdownOverFold(t *testing.T) {
	m := foldModel(t)
	m.tabs[0].folds = map[int]bool{0: true}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := m.tabs[0].buf.CurLine(); got != 4 && got != 6 {
		t.Fatalf("pgdown over a folded block must end at the fold edge, got %d", got)
	}
}

func TestFoldToggleKeysAndGutterClicks(t *testing.T) {
	m := foldModel(t)

	// Alt+C folds the block under the cursor (vim zc), Alt+O opens it (zo).
	m = press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !m.tabs[0].folds[0] {
		t.Fatal("alt+c must fold the block on the cursor line")
	}
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt})
	if len(m.tabs[0].folds) != 0 {
		t.Fatalf("alt+o must unfold, folds = %v", m.tabs[0].folds)
	}

	// Nothing to fold on a plain line: an explicit status hint, no fold.
	m.tabs[0].buf.SetCursor(6, 0)
	m = press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !strings.Contains(m.msg, "nothing to fold") {
		t.Fatalf("alt+c on a non-fold line must hint, msg = %q", m.msg)
	}
}

// TestFoldChevronClickToggles pins the mouse path: the painted chevron cell is
// the gutter's last cell, so a left click on it folds and unfolds the block.
// The gutter must be exactly gutterWidthForTab cells wide — one cell wider and
// the click lands on the cell left of the glyph, which used to flip a
// breakpoint instead of folding.
func TestFoldChevronClickToggles(t *testing.T) {
	m := foldModel(t)
	gw := m.gutterWidthForTab(&m.tabs[0])
	rows := plainRows(m.View().Content)
	// Row 1 is "func main() {": its gutter, including the ▾, must end at gw.
	if prefix := string([]rune(rows[1])[:gw]); !strings.HasSuffix(prefix, "▾") {
		t.Fatalf("the chevron must be the gutter's last cell, gutter=%q", prefix)
	}
	if runeAt(rows[1], gw) != 'f' {
		t.Fatalf("content must start right after the gutter, got %q", runeAt(rows[1], gw))
	}

	m.Update(tea.MouseClickMsg{X: gw - 1, Y: 1, Button: tea.MouseLeft})
	if !m.tabs[0].folds[0] {
		t.Fatalf("clicking the ▾ cell must fold the block, folds = %v", m.tabs[0].folds)
	}
	if got := m.tabs[0].buf.CurLine(); got != 0 {
		t.Fatalf("a gutter click must not move the cursor, got line %d", got)
	}

	// The folded block still shows its start line with a ▸ in the same cell.
	rows = plainRows(m.View().Content)
	if prefix := string([]rune(rows[1])[:gw]); !strings.HasSuffix(prefix, "▸") {
		t.Fatalf("a folded block must paint ▸ in the fold cell, gutter=%q", prefix)
	}
	// The summary row the folded block collapses to: blank cells up to the
	// fold column, then the ▸.
	if prefix := string([]rune(rows[2])[:gw]); !strings.HasSuffix(prefix, "▸") {
		t.Fatalf("the summary row must paint ▸ in the fold cell, gutter=%q", prefix)
	}
	m.Update(tea.MouseClickMsg{X: gw - 1, Y: 1, Button: tea.MouseLeft})
	if len(m.tabs[0].folds) != 0 {
		t.Fatalf("a second click must unfold, folds = %v", m.tabs[0].folds)
	}

	// Any gutter cell of the summary row expands it too: the row stands for
	// the hidden block and has no marker column to protect.
	m.tabs[0].folds = map[int]bool{0: true}
	m.Update(tea.MouseClickMsg{X: 1, Y: 2, Button: tea.MouseLeft})
	if len(m.tabs[0].folds) != 0 {
		t.Fatalf("clicking the summary row's gutter must unfold, folds = %v", m.tabs[0].folds)
	}
}

// TestFoldGutterKeepsColumnAlignment: with folds present the gutter grows one
// column, and a content click must still put the cursor on the character under
// the pointer instead of one to its right.
func TestFoldGutterKeepsColumnAlignment(t *testing.T) {
	m := foldModel(t)
	gw := m.gutterWidthForTab(&m.tabs[0])
	// Column gw is the first content cell, so clicking it means column 0.
	m.Update(tea.MouseClickMsg{X: gw, Y: 1, Button: tea.MouseLeft})
	if got := m.tabs[0].buf.CurLine(); got != 0 {
		t.Fatalf("click on row 1 must select line 0, got %d", got)
	}
	if got := m.tabs[0].buf.Col(); got != 0 {
		t.Fatalf("click on the first content cell must select column 0, got %d", got)
	}
	// The next cell is column 1: 'u' in "func".
	m.Update(tea.MouseClickMsg{X: gw + 1, Y: 1, Button: tea.MouseLeft})
	if got := m.tabs[0].buf.Col(); got != 1 {
		t.Fatalf("click one cell in must select column 1, got %d", got)
	}
}

// runeAt returns the rune at 0-based cell x of a plain rendered row.
func runeAt(row string, x int) rune {
	rs := []rune(row)
	if x < 0 || x >= len(rs) {
		return 0
	}
	return rs[x]
}

func TestFoldSummaryClickUnfolds(t *testing.T) {
	m := foldModel(t)
	m.tabs[0].folds = map[int]bool{0: true}
	// rows: 0 tab bar, 1 "func main() {", 2 summary
	m.Update(tea.MouseClickMsg{X: m.gutterWidthForTab(&m.tabs[0]) + 2, Y: 2, Button: tea.MouseLeft})
	if len(m.tabs[0].folds) != 0 {
		t.Fatalf("clicking the summary row must unfold, folds = %v", m.tabs[0].folds)
	}
	if got := m.tabs[0].buf.CurLine(); got != 0 {
		t.Fatalf("after unfolding the cursor sits on the fold start, got %d", got)
	}
}

func TestFoldClampScrollUnfoldsUnderCursor(t *testing.T) {
	m := foldModel(t)
	m.tabs[0].folds = map[int]bool{0: true}
	// Something re-enters the buffer between folds (undo, an AI edit, a jump):
	// the cursor on a hidden line forces the block open.
	m.tabs[0].buf.SetCursor(2, 0)
	m.clampScroll()
	if _, _, hid := m.tabs[0].foldHidden(2, m.cfg.Editor.TabWidth); hid {
		t.Fatal("a folded block hiding the cursor must be unfolded by clampScroll")
	}
}

func TestFoldWithWordWrap(t *testing.T) {
	m := foldModel(t)
	m.panes[0].wordWrap = true
	m.tabs[0].folds = map[int]bool{0: true}
	plain := foldPlain(m)
	if strings.Contains(plain, "do()") || !strings.Contains(plain, "3 lines hidden") {
		t.Fatalf("wrapped pane must respect folds:\n%s", plain)
	}
}

func TestGutterGrowsOnlyWhenFoldable(t *testing.T) {
	m := foldModel(t)
	withFolds := m.gutterWidthForTab(&m.tabs[0])
	if w := withFolds; w < 6 {
		t.Fatalf("gutter too narrow: %d", w)
	}
	plainTab := New()
	plainTab.tabs = []tab{{buf: buffer.Load("one line\n")}}
	if m.gutterWidthForTab(&plainTab.tabs[0]) >= withFolds {
		t.Fatal("a buffer without foldable blocks must not grow the fold column")
	}
}
