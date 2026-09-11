package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The editor test suite asserts English UI strings, so pin the language to
// English regardless of any lang the developer set in their global config.
func init() { _ = os.Setenv("DMED_LANG", "en") }

func press(m Model, k tea.KeyPressMsg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func typeStr(m Model, s string) Model {
	for _, r := range s {
		m = press(m, tea.KeyPressMsg{Text: string(r)})
	}
	return m
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTabsOpenSwitchClose(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	f2 := writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(f1, f2)
	if len(m.tabs) != 2 || m.activeTabIndex() != 1 {
		t.Fatalf("want 2 tabs activeTabIndex=1, got %d tabs activeTabIndex=%d", len(m.tabs), m.activeTabIndex())
	}
	if got := m.activeTab().buf.Text(); got != "bravo\n" {
		t.Fatalf("active content = %q", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	if m.activeTabIndex() != 0 || m.activeTab().buf.Text() != "alpha\n" {
		t.Fatalf("alt+left: activeTabIndex=%d content=%q", m.activeTabIndex(), m.activeTab().buf.Text())
	}
	typeStr(m, "X")
	if !m.tabs[0].buf.Dirty() || m.tabs[1].buf.Dirty() {
		t.Fatal("dirty must be per-tab")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	if m.activeTabIndex() != 1 {
		t.Fatalf("alt+right: activeTabIndex=%d", m.activeTabIndex())
	}
	m = press(m, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if len(m.tabs) != 1 || m.activeTabIndex() != 0 || m.activeTab().path != f1 {
		t.Fatalf("ctrl+w: %d tabs activeTabIndex=%d path=%q", len(m.tabs), m.activeTabIndex(), m.activeTab().path)
	}
}

func TestGotoLine(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "l1\nl2\nl3\nl4\nl5\n")

	m := New(f)
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !m.gotoOpen {
		t.Fatal("ctrl+l must open goto prompt")
	}
	if len(m.gotoIn) != 0 {
		t.Fatalf("goto input should start empty, got %q", string(m.gotoIn))
	}
	m = press(m, tea.KeyPressMsg{Text: "3"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.gotoOpen {
		t.Fatal("esc must close goto without moving")
	}
	if m.cur().buf.CurLine() != 0 {
		t.Fatalf("cursor must stay on line 0 after esc, got %d", m.cur().buf.CurLine())
	}

	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: "3"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.gotoOpen {
		t.Fatal("enter must close goto")
	}
	if m.cur().buf.CurLine() != 2 {
		t.Fatalf("goto 3: cursor on line %d", m.cur().buf.CurLine())
	}
	if m.cur().buf.Col() != 0 {
		t.Fatalf("goto: cursor col = %d", m.cur().buf.Col())
	}
}

func TestGotoLineRelative(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "l1\nl2\nl3\nl4\nl5\nl6\n")
	m := New(f)

	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: "+2"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cur().buf.CurLine() != 2 {
		t.Fatalf("goto +2 from 0: cursor on line %d", m.cur().buf.CurLine())
	}

	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: "-2"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cur().buf.CurLine() != 0 {
		t.Fatalf("goto -2 from 2: cursor on line %d", m.cur().buf.CurLine())
	}
}

func TestGotoLineClamp(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "a\nb\nc\n")
	m := New(f)

	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Text: "100"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cur().buf.CurLine() != 2 {
		t.Fatalf("goto 100 should clamp to last line, got %d", m.cur().buf.CurLine())
	}
}

func TestGotoPaletteCommand(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "one\n")
	m := New(f)
	m.startPalette()
	m.paletteQ = []rune("go to line")
	sel := m.filterPalette()
	if len(sel) != 1 {
		t.Fatalf("expected 1 palette hit, got %d", len(sel))
	}
	c := sel[0]
	c.action(&m)
	if !m.gotoOpen {
		t.Fatal("Go to Line palette command must open the goto prompt")
	}
}

func TestPromptOpensAndCancels(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "one.txt", "one\n")
	target := writeTemp(t, dir, "two.txt", "two lines\n")

	m := New(f1)
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if !m.promptOpen {
		t.Fatal("ctrl+t must open prompt")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.promptOpen || len(m.tabs) != 1 {
		t.Fatal("esc must cancel prompt")
	}
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m = typeStr(m, target)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.promptOpen || len(m.tabs) != 2 || m.activeTabIndex() != 1 {
		t.Fatalf("enter: prompt=%v tabs=%d activeTabIndex=%d", m.promptOpen, len(m.tabs), m.activeTabIndex())
	}
	if got := m.activeTab().buf.Text(); got != "two lines\n" {
		t.Fatalf("opened content = %q", got)
	}
	if m.activeTab().path != target {
		t.Fatalf("opened path = %q", m.activeTab().path)
	}
}

func TestCloseLastTabReturnsQuit(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	nm := next.(Model)
	if nm.lenTabs() != 1 {
		t.Fatal("last tab must stay until program exits")
	}
	if cmd == nil {
		t.Fatal("closing last tab must quit")
	}
	// Bubbletea renders one final frame after Update returns Quit.
	_ = nm.View()
}

func (m Model) lenTabs() int { return len(m.tabs) }

func TestViewShowsTabNames(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "aa.txt", "x\n")
	f2 := writeTemp(t, dir, "bb.txt", "y\n")

	m := New(f1, f2)
	m.width, m.height = 80, 24
	v := m.View()
	if !strings.Contains(v.Content, "aa.txt") || !strings.Contains(v.Content, "bb.txt") {
		t.Fatal("view must show both tab names")
	}
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if !strings.Contains(m.View().Content, "open file:") {
		t.Fatal("prompt line must be visible while open")
	}
}

func TestUntitledCannotSave(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !m.promptSave {
		t.Fatalf("untitled ctrl+s must open save prompt, msg = %q", m.msg)
	}
}

func TestSaveAsWritesNewPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.txt")

	m := New()
	m.width, m.height = 80, 24
	typeStr(m, "hello")
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !m.promptSave {
		t.Fatal("untitled ctrl+s must open save prompt")
	}
	m = typeStr(m, target)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cur().path != target {
		t.Fatalf("path = %q", m.cur().path)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("saved = %q", data)
	}
	if m.cur().buf.Dirty() {
		t.Fatal("must be clean after save")
	}
}

func TestQuitDirtyPrompts(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	nm := next.(Model)
	if !nm.quitConfirm {
		t.Fatalf("dirty ctrl+q must open quit confirm, msg = %q", nm.msg)
	}
}

func TestQuitDirtySaveAndQuit(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	nm := next.(Model)
	if !nm.quitConfirm {
		t.Fatal("dirty ctrl+q must open quit confirm")
	}
	next, cmd := nm.Update(tea.KeyPressMsg{Text: "y"})
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("confirming quit must return Quit")
	}
	if nm.cur().buf.Dirty() {
		t.Fatal("buffer must be clean after save")
	}
}

func TestQuitDirtyCancel(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	m = press(m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if !m.quitConfirm {
		t.Fatal("dirty ctrl+q must open quit confirm")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.quitConfirm {
		t.Fatal("esc must cancel quit confirm")
	}
	if !m.cur().buf.Dirty() {
		t.Fatal("buffer must remain dirty")
	}
}

func TestQuitDirtyConfirmCyrillic(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	m = press(m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if !m.quitConfirm {
		t.Fatal("dirty ctrl+q must open quit confirm")
	}

	// Physical Y key in the Russian layout types «н» — it must confirm (yes).
	next, cmd := m.Update(tea.KeyPressMsg{Text: "н"})
	nm := next.(Model)
	if cmd == nil || nm.quitConfirm {
		t.Fatalf("«н» (physical Y) must confirm save+quit, got cmd=%v quitConfirm=%v", cmd, nm.quitConfirm)
	}

	// «т» (physical N key in Russian layout) must be "no": quit without saving.
	m = New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	m = press(m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	next, cmd = m.Update(tea.KeyPressMsg{Text: "т"})
	nm = next.(Model)
	if nm.quitConfirm || cmd == nil {
		t.Fatalf("«т» (physical N) must say no, got quitConfirm=%v cmd=%v", nm.quitConfirm, cmd)
	}
	if !nm.cur().buf.Dirty() {
		t.Fatal("denying must leave the buffer unsaved")
	}

	// BaseCode is the layout-independent physical key (Windows Console API).
	// The real console delivers all three fields together; a bare BaseCode
	// message would be mangled by handleKey's control-byte normalization.
	m = New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	m = press(m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	next, cmd = m.Update(tea.KeyPressMsg{BaseCode: 'y', Code: 'н', Text: "н"})
	nm = next.(Model)
	if cmd == nil || nm.quitConfirm {
		t.Fatalf("BaseCode 'y' must confirm save+quit, got cmd=%v quitConfirm=%v", cmd, nm.quitConfirm)
	}
}

func TestPasteIntoBuffer(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24

	next, _ := m.Update(tea.PasteMsg{Content: "INSERTED"})
	m = next.(Model)
	if m.cur().buf.Text() != "INSERTEDalpha\n" {
		t.Fatalf("buffer = %q, want pasted text before existing content", m.cur().buf.Text())
	}
}

func TestPasteIntoChatInput(t *testing.T) {
	m := New()
	before := m.cur().buf.Text()
	m.chatOpen = true
	m.chatFocus = true

	next, _ := m.Update(tea.PasteMsg{Content: "explain this"})
	m = next.(Model)
	if string(m.chatIn) != "explain this" {
		t.Fatalf("chat input = %q, want pasted text", string(m.chatIn))
	}
	if m.cur().buf.Text() != before {
		t.Fatalf("paste must not leak into the buffer, changed %q -> %q", before, m.cur().buf.Text())
	}
}

func TestQuitCopySkipsConfirm(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	m.cur().buf.StartSelection()
	m.cur().buf.MoveDownWithSelect()
	m.cur().buf.LineEndWithSelect()
	next, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	nm := next.(Model)
	if nm.quitConfirm {
		t.Fatal("copy with selection must not quit")
	}
	if nm.clipboard != "alpha" {
		t.Fatalf("clipboard = %q", nm.clipboard)
	}
}

func TestQuitCopyOnDirtyQuits(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	nm := next.(Model)
	if !nm.quitConfirm {
		t.Fatal("ctrl+c on dirty without selection must confirm quit")
	}
}

func TestCtrlXCloseTab(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	f2 := writeTemp(t, dir, "b.txt", "bravo\n")

	m := New(f1, f2)
	m = press(m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if len(m.tabs) != 1 || m.activeTab().path != f1 {
		t.Fatalf("ctrl+x must close active tab, tabs=%d", len(m.tabs))
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if nm := next.(Model); len(nm.tabs) != 1 || cmd == nil {
		t.Fatal("ctrl+x on last tab must quit without emptying tabs")
	}
}

func TestCtrlXCloseLastDirtyConfirm(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	nm := next.(Model)
	if !nm.quitConfirm {
		t.Fatalf("dirty ctrl+x on last tab must confirm quit, msg = %q", nm.msg)
	}
	next, cmd := nm.Update(tea.KeyPressMsg{Text: "n"})
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("confirming discard must return Quit")
	}
	if !nm.cur().buf.Dirty() {
		t.Fatal("buffer must remain dirty when cancelling quit")
	}
}

func TestCtrlWCloseLastDirtyConfirm(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "a.txt", "alpha\n")
	m := New(f1)
	m.width, m.height = 80, 24
	typeStr(m, "X")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	nm := next.(Model)
	if !nm.quitConfirm {
		t.Fatalf("dirty ctrl+w on last tab must confirm quit, msg = %q", nm.msg)
	}
	next, cmd := nm.Update(tea.KeyPressMsg{Text: "y"})
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("confirming save+quit must return Quit")
	}
	if nm.cur().buf.Dirty() {
		t.Fatal("buffer must be clean after save")
	}
}

func TestSplitSizes(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	m.splitVert()
	ew := m.editorAreaWidth()
	if m.paneTotalWidth(0)+1+m.paneTotalWidth(1) != ew {
		t.Fatalf("vert split widths must sum to editor width: %d+1+%d != %d", m.paneTotalWidth(0), m.paneTotalWidth(1), ew)
	}

	m = New()
	m.width, m.height = 80, 24
	m.splitHoriz()
	vh := m.viewHeight()
	if m.paneViewHeight(0)+1+m.paneViewHeight(1) != vh {
		t.Fatalf("horiz split heights must sum to view height: %d+1+%d != %d", m.paneViewHeight(0), m.paneViewHeight(1), vh)
	}
}

func TestSaveWritesActiveTab(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "s.txt", "")
	f2 := writeTemp(t, dir, "t.txt", "")

	m := New(f1, f2)
	typeStr(m, "hello")
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	data, err := os.ReadFile(f2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("saved = %q", data)
	}
	if m.tabs[1].buf.Dirty() {
		t.Fatal("must be clean after save")
	}
}

func TestWindowTitle(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "foo.go", "package main\n")
	m := New(f)
	m.width, m.height = 80, 24
	v := m.View()
	if v.WindowTitle == "" {
		t.Fatal("window title must not be empty")
	}
	if !strings.Contains(v.WindowTitle, "foo.go") {
		t.Fatalf("window title must contain file name, got %q", v.WindowTitle)
	}
}

func TestTerminalCursor(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	// Cursor should start at (0,0) — gutter + col 0, tab bar offset 1.
	x, y := m.cursorScreenPos()
	if x < 0 || y != 1 {
		t.Fatalf("initial cursor pos: x=%d y=%d, want x>=0 y=1", x, y)
	}
	// Move right a few times.
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRight()
	}
	x, y = m.cursorScreenPos()
	if x < 0 {
		t.Fatalf("cursor x must be >= 0 after move right, got %d", x)
	}
}

func TestOSC52Clipboard(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "clip.txt", "hello world\n")
	m := New(f)
	m.width, m.height = 80, 24

	// Select "hello".
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRightWithSelect()
	}

	// Copy.
	m = press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.clipboard != "hello" {
		t.Fatalf("clipboard = %q, want 'hello'", m.clipboard)
	}

	// Move to end of line (clears selection).
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})

	// Paste.
	m = press(m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if !strings.HasPrefix(m.cur().buf.Text(), "hello worldhello") {
		t.Fatalf("after paste: %q", m.cur().buf.Text())
	}
}

func TestMouseScroll(t *testing.T) {
	dir := t.TempDir()
	lines := ""
	for i := 0; i < 50; i++ {
		lines += "line " + string(rune('a'+i%26)) + "\n"
	}
	f := writeTemp(t, dir, "scroll.txt", lines)
	m := New(f)
	m.width, m.height = 80, 24

	p := m.curPane()
	if p.offsetY != 0 {
		t.Fatalf("initial scroll offset must be 0, got %d", p.offsetY)
	}

	// Simulate mouse wheel down over the buffer area.
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 40, Y: 10})
	if p.offsetY != 1 {
		t.Fatalf("after wheel down: offset=%d, want 1", p.offsetY)
	}

	// Simulate mouse wheel up.
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 40, Y: 10})
	if p.offsetY != 0 {
		t.Fatalf("after wheel up: offset=%d, want 0", p.offsetY)
	}
}

func TestMouseClick(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "click.txt", "hello\nworld\nfoo\n")
	m := New(f)
	m.width, m.height = 80, 24

	// Click on the first editor row, at the gutter+space after leftRail.
	// Row 1 = first editor line (y=1 in 0-indexed). x must skip gutter.
	gw := m.gutterWidthForTab(&m.tabs[0])
	leftW := m.leftRailWidth()
	_ = m.handleMouseClick(tea.MouseClickMsg{X: leftW + gw + 2, Y: 1})

	// Cursor should be on line 0.
	if m.cur().buf.CurLine() != 0 {
		t.Fatalf("curLine = %d, want 0", m.cur().buf.CurLine())
	}
	// Column should be 2 (at 'l' in "hello").
	if m.cur().buf.Col() != 2 {
		t.Fatalf("col = %d, want 2", m.cur().buf.Col())
	}
}

func TestToggleCommentKey(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "main.go", "func main() {}\n")
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	got := m.cur().buf.Text()
	if got != "// func main() {}\n" {
		t.Fatalf("ctrl+/ comment: got %q", got)
	}
	m = press(m, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	if m.cur().buf.Text() != "func main() {}\n" {
		t.Fatalf("ctrl+/ uncomment: got %q", m.cur().buf.Text())
	}
}

func TestToggleCommentKeyUnderscore(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "main.go", "func main() {}\n")
	m := New(f)
	m.width, m.height = 80, 24

	// Some terminals deliver Ctrl+/ as 0x1f, decoded as ctrl+_.
	m = press(m, tea.KeyPressMsg{Code: '_', Mod: tea.ModCtrl})
	if m.cur().buf.Text() != "// func main() {}\n" {
		t.Fatalf("ctrl+_ comment: got %q", m.cur().buf.Text())
	}
}

func TestToggleCommentNoSyntax(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "plain\n")
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	if m.cur().buf.Text() != "plain\n" {
		t.Fatalf("text changed for unknown type: %q", m.cur().buf.Text())
	}
	if m.msg != "no comment syntax for this file type" {
		t.Fatalf("msg = %q", m.msg)
	}
}

func TestWordWrapSegments(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "hello world foo bar baz\n")
	m := New(f)
	m.width, m.height = 26, 24

	tt := m.cur()
	contentW := m.paneContentWidth(m.activePane)
	segs := tt.tabWrap(contentW, m.cfg.Editor.TabWidth)
	if len(segs) < 2 {
		t.Fatalf("expected >=2 segments for contentW=%d, got %d: %+v", contentW, len(segs), segs)
	}
	if segs[0].line != 0 || segs[0].expStart != 0 {
		t.Fatalf("first segment: %+v", segs[0])
	}
	if segs[1].line != 0 || segs[1].expStart < segs[0].expEnd {
		t.Fatalf("second segment should follow first: %+v", segs[1])
	}
}

func TestWordWrapSegRowForCol(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "hello world foo bar baz\n")
	m := New(f)
	m.width, m.height = 26, 24

	tt := m.cur()
	contentW := m.paneContentWidth(m.activePane)
	segs := tt.tabWrap(contentW, m.cfg.Editor.TabWidth)
	if len(segs) < 2 {
		t.Skipf("contentW=%d didn't wrap", contentW)
	}

	// Col 0 → first segment, row 0.
	row, expStart := segRowForCol(segs, 0, 0)
	if row != 0 || expStart != 0 {
		t.Fatalf("col 0: row=%d expStart=%d", row, expStart)
	}

	// Col 20 → first char of second segment.
	row, expStart = segRowForCol(segs, 0, segs[1].expStart)
	if row != 1 || expStart != segs[1].expStart {
		t.Fatalf("col %d: row=%d expStart=%d", segs[1].expStart, row, expStart)
	}

	// Col 22 (last char 'z') → still second segment.
	row, _ = segRowForCol(segs, 0, 22)
	if row != 1 {
		t.Fatalf("col 22: row=%d, want 1", row)
	}
}

func TestToggleWordWrap(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "hello world\n")
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt})
	if !m.panes[0].wordWrap {
		t.Fatal("alt+z should enable word wrap")
	}
	if m.msg != "word wrap on" {
		t.Fatalf("msg after enable = %q", m.msg)
	}

	m = press(m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt})
	if m.panes[0].wordWrap {
		t.Fatal("alt+z again should disable word wrap")
	}
	if m.msg != "word wrap off" {
		t.Fatalf("msg after disable = %q", m.msg)
	}
}

func TestToggleWordWrapResetsOffsetX(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "hello world\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.panes[0].offsetX = 5

	m = press(m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt})
	if m.panes[0].offsetX != 0 {
		t.Fatalf("offsetX after wrap toggle: %d, want 0", m.panes[0].offsetX)
	}
}
