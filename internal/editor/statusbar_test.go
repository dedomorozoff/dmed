package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestStatusIconAtSpansStrip(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(a)
	m.width, m.height = 80, 24

	for _, d := range statusIconDefs {
		if w := lipgloss.Width(d.glyph); w != 1 {
			t.Fatalf("glyph %q has width %d, want 1 (hit-testing assumes single cells)", d.glyph, w)
		}
		x := m.statusIconX(d.act)
		if x < 0 {
			t.Fatalf("statusIconX(%v) = -1", d.act)
		}
		if got := m.statusIconAt(x); got != d.act {
			t.Fatalf("statusIconAt(%d) = %v, want %v", x, got, d.act)
		}
		w := lipgloss.Width(" " + d.glyph + " ")
		if got := m.statusIconAt(x + w - 1); got != d.act {
			t.Fatalf("statusIconAt(%d) = %v, want %v (right edge)", x+w-1, got, d.act)
		}
		if got := m.statusIconAt(x + w); got == d.act {
			t.Fatalf("statusIconAt(%d) still maps to %v past the cell", x+w, d.act)
		}
	}
	if got := m.statusIconAt(200); got != actNone {
		t.Fatalf("statusIconAt(200) = %v, want actNone", got)
	}
}

func TestStatusIconClickTogglesPanels(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(a)
	m.width, m.height = 100, 24
	m.chatModel = "test-model" // avoid probing the network in toggleChat

	click := func(act statusAction) {
		t.Helper()
		_ = m.handleMouseClick(tea.MouseClickMsg{X: m.statusIconX(act), Y: m.viewHeight() + 1})
	}

	// Git panel toggles on then off.
	click(actGit)
	if !m.gitOpen {
		t.Fatal("git icon must open the git panel")
	}
	click(actGit)
	if m.gitOpen {
		t.Fatal("second git icon click must close the git panel")
	}

	// Debug opens the panel and closes the terminal.
	m.termOpen = true
	click(actDebug)
	if !m.dapOpen || m.termOpen {
		t.Fatalf("debug icon: dapOpen=%v termOpen=%v, want true/false", m.dapOpen, m.termOpen)
	}

	// AI chat rail.
	click(actChat)
	if !m.chatOpen {
		t.Fatal("chat icon must open the AI chat")
	}

	// Tree: force focused-visible so the click closes it.
	m.treeVisible, m.treeFocus = true, true
	click(actTree)
	if m.treeVisible {
		t.Fatal("tree icon click must close the focused tree")
	}
}

func TestStatusIconHiddenInPromptMode(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(a)
	m.width, m.height = 80, 24
	y := m.viewHeight() + 1

	m.promptOpen = true
	if m.statusIconsVisible() {
		t.Fatal("icons must be hidden while a prompt owns the bottom line")
	}
	before := m.gitOpen
	_ = m.handleMouseClick(tea.MouseClickMsg{X: m.statusIconX(actGit), Y: y})
	if m.gitOpen != before {
		t.Fatal("status icon click must not fire while the strip is hidden")
	}
}

func TestStatusIconHoverSetsTipAndCallout(t *testing.T) {
	dir := t.TempDir()
	chdir(t, t.TempDir())
	a := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(a)
	m.width, m.height = 100, 5
	y := m.viewHeight() + 1
	x := m.statusIconX(actGit)

	m.updateStatusHover(tea.MouseMotionMsg{X: x, Y: y})
	if m.hoverIcon != actGit {
		t.Fatalf("hoverIcon = %v, want actGit", m.hoverIcon)
	}
	tip := m.statusTip(m.hoverIcon)
	if tip == "" || tip == "status.tip_git" {
		t.Fatalf("statusTip = %q, want a translated label", tip)
	}

	// Moving off the strip clears the hover.
	m.updateStatusHover(tea.MouseMotionMsg{X: 60, Y: 3})
	if m.hoverIcon != actNone {
		t.Fatalf("hoverIcon = %v, want actNone off the strip", m.hoverIcon)
	}

	// The callout replaces the row right above the status bar.
	m.updateStatusHover(tea.MouseMotionMsg{X: x, Y: y})
	rows := make([]string, m.viewHeight()+2)
	for i := range rows {
		rows[i] = "content"
	}
	out := m.overlayStatusTooltip(rows)
	if out[m.viewHeight()] == "content" {
		t.Fatal("hover callout must replace the row above the status bar")
	}
	if !strings.Contains(stripANSI(out[m.viewHeight()]), tip) {
		t.Fatalf("callout %q does not contain the label %q", stripANSI(out[m.viewHeight()]), tip)
	}
}
