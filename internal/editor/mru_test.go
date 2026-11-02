package editor

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// MRU tab switching: the chord cycles the pane's recency order one step per
// press and wraps back through the tab the walk started on. The walk records no
// history and reorders none — those two rules are what make repeated presses walk
// the list instead of ping-ponging between two tabs, so they get their own tests.

var (
	mruChord = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModCtrl}
	mruAlias = tea.KeyPressMsg{Code: '`', Text: "`", Mod: tea.ModAlt}
	tabNext  = tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}
)

// mruModel opens four files. New() creates the panes after the tabs, so the
// construction never records anything: the last file is on screen and the
// history is empty.
func mruModel(t *testing.T) (Model, []string) {
	t.Helper()
	dir := t.TempDir()
	paths := []string{
		writeTemp(t, dir, "one.txt", "1\n"),
		writeTemp(t, dir, "two.txt", "2\n"),
		writeTemp(t, dir, "three.txt", "3\n"),
		writeTemp(t, dir, "four.txt", "4\n"),
	}
	m := New(paths...)
	m.width, m.height = 80, 24
	return m, paths
}

func TestMRUCyclesRecencyOrder(t *testing.T) {
	m, _ := mruModel(t)
	m.setActiveTab(0) // leaves 3: history [3]
	m.setActiveTab(2) // leaves 0: history [0 3]

	// The cycle order is the tab on screen, then the tabs left, then the ones
	// never focused: [2 0 3 1].
	want := []int{0, 3, 1, 2} // one step per press, the last press wraps
	for i, w := range want {
		m = press(m, mruChord)
		if got := m.activeTabIndex(); got != w {
			t.Fatalf("press %d: active tab %d, want %d (order %v)", i+1, got, w, want)
		}
	}
}

func TestMRUWalkDoesNotReorderTheList(t *testing.T) {
	m, _ := mruModel(t)
	m.setActiveTab(0)
	m.setActiveTab(2)
	before := slices.Clone(m.curPane().recent)

	seen := map[int]bool{}
	for range 4 {
		m = press(m, mruChord)
		seen[m.activeTabIndex()] = true
	}
	if len(seen) != 4 {
		t.Fatalf("four presses must visit four different tabs, got %v", seen)
	}
	if !slices.Equal(before, m.curPane().recent) {
		t.Fatalf("the walk must not touch the history: %v, was %v", m.curPane().recent, before)
	}
}

func TestMRURecordsRealSwitchesOnly(t *testing.T) {
	m, _ := mruModel(t)
	m.setActiveTab(1)
	m = press(m, mruChord) // a walk step: records nothing
	if got := m.activeTabIndex(); got != 3 {
		t.Fatalf("the first step must reach the tab left last, got %d", got)
	}

	// Alt+→ is a real switch: it records the tab it leaves and ends the walk.
	m = press(m, tabNext)
	if got := m.activeTabIndex(); got != 0 {
		t.Fatalf("setup: alt+right from 3 must reach 0, got %d", got)
	}
	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 3 {
		t.Fatalf("the cycle must start from the tab the real switch left, got %d", got)
	}
}

func TestMRUIncludesTabsNeverFocused(t *testing.T) {
	m, _ := mruModel(t) // no switching at all: history empty, tab 3 on screen
	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 0 {
		t.Fatalf("untouched tabs join the cycle in tab order, got %d", got)
	}
	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 1 {
		t.Fatalf("second step: active tab %d, want 1", got)
	}
}

func TestMRURefusesWithOneTab(t *testing.T) {
	m := New(writeTemp(t, t.TempDir(), "solo.txt", "only\n"))
	m.width, m.height = 80, 24
	m = press(m, mruChord)
	if m.msg != "no other tab to switch to" {
		t.Fatalf("a single tab must refuse, got %q", m.msg)
	}
	if got := m.activeTabIndex(); got != 0 {
		t.Fatalf("a refusal must not move, active tab %d", got)
	}
	if m.curPane().recent != nil || m.curPane().recentWalk != nil {
		t.Fatalf("a refusal records nothing: recent=%v walk=%v", m.curPane().recent, m.curPane().recentWalk)
	}
}

func TestMRUAfterCloseRenumbersTheList(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "a\n")
	b := writeTemp(t, dir, "b.txt", "b\n")
	c := writeTemp(t, dir, "c.txt", "c\n")
	m := New(a, b, c)
	m.width, m.height = 80, 24
	m.setActiveTab(0) // history [2]
	if m.cur().path != a {
		t.Fatalf("setup: path=%q", m.cur().path)
	}

	m.closeTabAt(1) // b.txt closes; c.txt moves down to index 1
	if len(m.tabs) != 2 {
		t.Fatalf("setup: tabs=%d", len(m.tabs))
	}
	if got := m.curPane().recent; !slices.Equal(got, []int{1}) {
		t.Fatalf("the closed tab must drop out and the rest move down, recent=%v", got)
	}

	m = press(m, mruChord)
	if m.cur().path != c {
		t.Fatalf("the cycle must land on c.txt, path=%q", m.cur().path)
	}
	m = press(m, mruChord)
	m = press(m, mruChord)
	if m.cur().path == b {
		t.Fatal("a closed tab must never come back through the cycle")
	}
}

// TestMRUHandlesTheUntitledTab is the case that rules out naming the history by
// path: the untitled buffer has none, and focusOrOpen("") would resolve it
// against the project root and try to open a directory.
func TestMRUHandlesTheUntitledTab(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "later.txt", "x\n")
	m := New()
	m.width, m.height = 80, 24
	if m.cur().path != "" {
		t.Fatalf("setup: the editor opens with an untitled buffer, path=%q", m.cur().path)
	}
	m.openPath(f)
	if got := m.activeTabIndex(); got != 1 {
		t.Fatalf("setup: the opened file must be on screen, active tab %d", got)
	}

	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 0 {
		t.Fatalf("the cycle must reach the untitled tab, active tab %d", got)
	}
	if m.cur().path != "" {
		t.Fatalf("and must not try to open a path for it, got %q", m.cur().path)
	}
}

func TestMRUIsPerPane(t *testing.T) {
	m, _ := mruModel(t)
	m.splitVert() // the new pane shows tab 2 and starts with an empty history
	if got := m.activePane; got != 1 {
		t.Fatalf("setup: splitVert must focus the new pane, activePane=%d", got)
	}

	m = press(m, mruChord)
	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 1 {
		t.Fatalf("pane 1 walks its own order [2 0 1 3], got tab %d", got)
	}
	if other := m.panes[0].tabIdx; other != 3 {
		t.Fatalf("pane 1 must not move pane 0, its tab is %d", other)
	}

	m.focusOtherPane()
	m = press(m, mruChord)
	if got := m.activeTabIndex(); got != 0 {
		t.Fatalf("pane 0 starts its own cycle from tab 3 into the untouched ones, got %d", got)
	}
	if kept := m.panes[1].tabIdx; kept != 1 {
		t.Fatalf("pane 1 keeps its tab across the focus change, got %d", kept)
	}
}

func TestMRUPaletteCommand(t *testing.T) {
	m, _ := mruModel(t)
	m.setActiveTab(0)
	m.setActiveTab(2)

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	m = typeStr(m, "mru")
	hits := m.filterPalette()
	sel := -1
	for i, h := range hits {
		if h.id == "tab_mru" {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatalf("filtering on \"mru\" must find the command, got %v", paletteIDs(hits))
	}
	m.paletteSel = sel
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}

	viaChord, _ := mruModel(t)
	viaChord.setActiveTab(0)
	viaChord.setActiveTab(2)
	viaChord = press(viaChord, mruChord)
	if m.activeTabIndex() != viaChord.activeTabIndex() {
		t.Fatalf("palette and chord disagree: palette tab %d, chord tab %d",
			m.activeTabIndex(), viaChord.activeTabIndex())
	}
}

func TestHelpListsMRURow(t *testing.T) {
	const wantKey = "Ctrl+Tab / Alt+`"
	found := false
	for _, e := range helpEntries {
		if e.keys == wantKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("helpEntries is missing the %q row", wantKey)
	}

	m, _ := mruModel(t)
	m.width, m.height = 120, 24 // the panel truncates a wide description; see TestHelpListsBracketRows
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	for i, e := range helpEntries {
		if e.keys == wantKey {
			m.helpScroll = i
		}
	}
	out := m.View().Content
	for _, want := range []string{wantKey, m.t("help.tab_mru")} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must show %q", want)
		}
	}
}
