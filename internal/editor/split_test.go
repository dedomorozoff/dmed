package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/buffer"
)

func plainRows(s string) []string {
	out := make([]string, 0, 32)
	for _, line := range strings.Split(s, "\n") {
		var b strings.Builder
		inEsc := false
		for _, r := range line {
			if r == 0x1b {
				inEsc = true
				continue
			}
			if inEsc {
				if r == 'm' {
					inEsc = false
				}
				continue
			}
			b.WriteRune(r)
		}
		out = append(out, b.String())
	}
	return out
}

func TestSplitVertSeparatorAligned(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.tabs[0].buf = buffer.Load("short\na much longer line of text here to shift the separator\nx\n")
	m.splitVert()

	rows := plainRows(m.View())
	h := m.viewHeight()
	sepCol := -1
	for row := 1; row <= h; row++ {
		col := strings.IndexRune(rows[row], '│')
		if col < 0 {
			t.Fatalf("row %d has no vertical separator: %q", row, rows[row])
		}
		if sepCol == -1 {
			sepCol = col
		} else if col != sepCol {
			t.Fatalf("separator column moved: row 1 col %d, row %d col %d", sepCol, row, col)
		}
	}
}

func TestSplitHorizRowsFixedWidth(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.tabs[0].buf = buffer.Load("short\na much longer line of text here to shift things around a bit\nx\n")
	m.splitHoriz()

	rows := plainRows(m.View())
	want := m.editorAreaWidth()
	h := m.viewHeight()
	for row := 1; row <= h; row++ {
		if got := len([]rune(rows[row])); got != want {
			t.Fatalf("row %d width = %d, want %d (all pane rows must fill the editor area)", row, got, want)
		}
	}
}

func TestCloseTabCollapsesSplit(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	f2 := writeTemp(t, dir, "b.txt", "bravo\n")

	// New(f1,f2): tabs=[f1,f2], active tab f2. splitVert pairs pane0=f2, pane1=f1,
	// focus moves to pane1 → active tab is f1.
	m := New(f1, f2)
	m.splitVert()
	if m.activeTab().path != f1 {
		t.Fatalf("setup: expected active pane on f1, got %s", m.activeTab().path)
	}

	// Close the tab shown in the ACTIVE pane (f1): split collapses, f2 remains.
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.layout != splitNone {
		t.Fatalf("ctrl+x in split must collapse the split, layout=%d", m.layout)
	}
	if len(m.panes) != 1 {
		t.Fatalf("want 1 pane after close, got %d", len(m.panes))
	}
	if len(m.tabs) != 1 || m.activeTab().path != f2 {
		t.Fatalf("want only f2 left, got %d tabs, active=%s", len(m.tabs), m.activeTab().path)
	}

	// Same from the INACTIVE pane side.
	m = New(f1, f2)
	m.splitVert()
	m.focusOtherPane() // active pane now shows f2
	if m.activeTab().path != f2 {
		t.Fatalf("setup: expected active pane on f2, got %s", m.activeTab().path)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.layout != splitNone || len(m.panes) != 1 {
		t.Fatalf("want collapsed single pane, layout=%d panes=%d", m.layout, len(m.panes))
	}
	if len(m.tabs) != 1 || m.activeTab().path != f1 {
		t.Fatalf("want only f1 left, got %d tabs, active=%s", len(m.tabs), m.activeTab().path)
	}
}
