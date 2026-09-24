package editor

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestSplitIconsRenderAndClick(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(dir, f)
	m.width, m.height = 80, 24

	if !m.splitIconsVisible() {
		t.Fatal("split icons must fit next to a single tab")
	}
	for _, d := range splitIconGlyphs {
		if w := lipgloss.Width(d.glyph); w != 1 {
			t.Fatalf("split glyph %q has width %d, want 1", d.glyph, w)
		}
	}

	// The tab bar row carries both glyphs at the far right.
	v := m.View()
	if !strings.Contains(v.Content, "◫") || !strings.Contains(v.Content, "▤") {
		t.Fatalf("tab bar must render split icons:\n%s", v.Content)
	}

	start := m.width - m.splitIconsWidth()
	if got := m.splitIconAt(start); got != actSplitV {
		t.Fatalf("splitIconAt(%d) = %v, want actSplitV", start, got)
	}
	if got := m.splitIconAt(start + m.splitIconsWidth() - 1); got != actSplitH {
		t.Fatalf("splitIconAt(right edge) = %v, want actSplitH", got)
	}
	if got := m.splitIconAt(start - 1); got != actNone {
		t.Fatalf("splitIconAt(%d) = %v, want actNone", start-1, got)
	}

	// Clicking the vertical icon splits side by side, then closes it.
	if m.layout != splitNone {
		t.Fatalf("layout = %v, want splitNone", m.layout)
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: start, Y: 0})
	if m.layout != splitVert {
		t.Fatalf("click vertical icon: layout = %v, want splitVert", m.layout)
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: start, Y: 0})
	if m.layout != splitNone {
		t.Fatalf("second click must close the split, layout = %v", m.layout)
	}

	// Horizontal icon stacks the panes.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: start + 3, Y: 0})
	if m.layout != splitHoriz {
		t.Fatalf("click horizontal icon: layout = %v, want splitHoriz", m.layout)
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: start + 3, Y: 0})
	if m.layout != splitNone {
		t.Fatalf("second horizontal click must close the split, layout = %v", m.layout)
	}
}

func TestSplitIconsHiddenWhenTabsFillWidth(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.width, m.height = 30, 24

	// Open so many tabs that the labels swallow the whole top row.
	for i := 0; i < 20; i++ {
		f := writeTemp(t, dir, strings.Repeat("x", 4)+strconv.Itoa(i)+".txt", "x\n")
		m.openPath(f)
	}
	if m.splitIconsVisible() {
		t.Fatal("split icons must be hidden when tabs fill the top row")
	}
	if got := m.splitIconAt(m.width - 1); got != actNone {
		t.Fatalf("splitIconAt must return actNone when icons are hidden, got %v", got)
	}

	// And the last tab label still receives the click.
	before := m.activeTabIndex()
	_ = m.handleMouseClick(tea.MouseClickMsg{X: m.width - 1, Y: 0})
	if m.activeTabIndex() == before {
		t.Fatalf("click on the right edge must still hit a tab when icons are hidden")
	}
}

func TestSplitIconHoverSetsTipAndCallout(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(dir, f)
	m.width, m.height = 100, 5

	start := m.width - m.splitIconsWidth()
	m.updateStatusHover(tea.MouseMotionMsg{X: start, Y: 0})
	if m.hoverSplit != actSplitV {
		t.Fatalf("hoverSplit = %v, want actSplitV", m.hoverSplit)
	}
	tip := m.statusTip(m.hoverSplit)
	if tip == "" || tip == "status.tip_splitv" {
		t.Fatalf("statusTip = %q, want a translated label", tip)
	}

	// Moving off the top row clears the hover.
	m.updateStatusHover(tea.MouseMotionMsg{X: 10, Y: 2})
	if m.hoverSplit != actNone {
		t.Fatalf("hoverSplit = %v, want actNone off the tab bar", m.hoverSplit)
	}

	// The callout replaces the row right under the tab bar.
	m.updateStatusHover(tea.MouseMotionMsg{X: start, Y: 0})
	rows := make([]string, m.viewHeight()+2)
	for i := range rows {
		rows[i] = "content"
	}
	out := m.overlaySplitTooltip(rows)
	if out[1] == "content" {
		t.Fatal("hover callout must replace the first editor row")
	}
	if !strings.Contains(stripANSI(out[1]), tip) {
		t.Fatalf("callout %q does not contain the label %q", stripANSI(out[1]), tip)
	}
}
