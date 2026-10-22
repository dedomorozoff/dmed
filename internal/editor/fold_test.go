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
