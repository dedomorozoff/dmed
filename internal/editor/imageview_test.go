package editor

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// writePNG lays down a solid-colour PNG and returns its path.
func writePNG(t *testing.T, dir, name string, w, h int, c color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeGIF lays down an n-frame GIF of solid colours and returns its path.
func writeGIF(t *testing.T, dir, name string, n int, cols []color.RGBA) string {
	t.Helper()
	g := &gif.GIF{}
	for i := 0; i < n; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{cols[i%len(cols)]})
		for p := range img.Pix {
			img.Pix[p] = 0
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 4)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// editorAt opens a file in an editor sized for a frame and returns it.
func editorAt(t *testing.T, path string, w, h int) Model {
	t.Helper()
	m := New(path)
	m.width, m.height = w, h
	m.profile = colorprofile.TrueColor
	m.cfg.Editor.TabWidth = 4
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	if em, ok := nm.(Model); ok {
		m = em
	}
	return m
}

// paneText is the editor's main area with the escapes stripped, which is what a
// reader sees.
func paneText(m Model) string {
	return stripANSI(strings.Join(m.renderPaneRows(0, m.paneContentHeight(0), m.paneTotalWidth(0)), "\n"))
}

// paneArt is the same rows with the escapes left in. A picture's frames differ in
// colour and nothing else, so a stripped comparison says two frames are identical
// when they are two colours apart — which is exactly the bug the frame tests are
// looking for.
func paneArt(m Model) string {
	return strings.Join(m.renderPaneRows(0, m.paneContentHeight(0), m.paneTotalWidth(0)), "\n")
}

// bufText is the active tab's text without the trailing newline a loaded buffer
// carries. An empty buffer's Text() is "\n" rather than "", so the comparisons
// below would be about the newline instead of about what was typed.
func bufText(m Model) string {
	return strings.TrimRight(m.cur().buf.Text(), "\n")
}

// TestAPictureOpensAsAPicture is the feature: a PNG in a tab is drawn, not typed.
//
// The two halves are checked because either alone passes a broken version. The
// art on screen without an empty buffer would still let a keystroke type into the
// document; an empty buffer without art on screen is the old mojibake with the
// file name still attached.
func TestAPictureOpensAsAPicture(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 64, 32, color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 255})
	m := editorAt(t, p, 80, 24)

	if m.cur().img == nil {
		t.Fatal("a PNG opened as text")
	}
	if got := bufText(m); got != "" {
		t.Errorf("the buffer holds %q; a picture must not be loaded into it", got)
	}
	text := paneText(m)
	if !strings.Contains(text, "▀") {
		t.Errorf("the pane draws no picture:\n%s", text)
	}
	// The bytes must not be on screen as prose either — that is the whole point.
	if strings.Contains(text, "ÿ") {
		t.Error("the picture's bytes reached the pane as text")
	}
	if !strings.Contains(text, "64×32") {
		t.Errorf("the caption does not report the dimensions:\n%s", text)
	}
}

// TestTheGutterIsGoneOverAPicture pins a smaller decision: line numbers beside a
// picture describe a document that does not exist, and the empty buffer behind it
// would number every row "1".
func TestTheGutterIsGoneOverAPicture(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 64, 32, color.RGBA{G: 255, A: 255})
	m := editorAt(t, p, 80, 24)
	if gw := m.gutterWidthForTab(m.cur()); gw != 0 {
		t.Errorf("gutter width over a picture = %d, want 0", gw)
	}
}

// TestAGIFAnimates is the other half of the feature: frames advance on a tick, and
// the tick chain is one chain.
func TestAGIFAnimates(t *testing.T) {
	dir := t.TempDir()
	p := writeGIF(t, dir, "anim.gif", 3, []color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255},
	})
	m := editorAt(t, p, 80, 24)
	if !m.cur().img.animated() {
		t.Fatal("a three-frame GIF is not animated here")
	}
	if d := m.animationDelay(); d <= 0 {
		t.Fatalf("animationDelay = %v, want a positive delay", d)
	}

	// Opening the file must have armed the chain: the tick is scheduled by the
	// message that brought the picture on screen, and no key was pressed yet.
	if !m.imgPending {
		t.Error("opening an animation did not schedule a frame")
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	if cmd != nil {
		t.Error("a second chain started while one was pending")
	}

	// Each tick advances exactly one frame and schedules exactly one more.
	seen := map[int]bool{}
	for i := 0; i < 6; i++ {
		before := m.cur().img.frame
		nm, cmd := m.Update(imgFrameMsg{})
		m = nm.(Model)
		if m.cur().img.frame == before {
			t.Fatalf("tick %d did not advance the frame", i)
		}
		if cmd == nil {
			t.Fatal("the animation stopped while it was on screen")
		}
		seen[m.cur().img.frame] = true
	}
	if len(seen) != 3 {
		t.Errorf("six ticks visited %d frames, want all 3", len(seen))
	}
}

// TestAStillPictureSchedulesNothing is the ordinary case: a PNG redrawing itself
// forever would keep the terminal busy for no reason.
func TestAStillPictureSchedulesNothing(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	if d := m.animationDelay(); d != 0 {
		t.Errorf("animationDelay for a PNG = %v, want 0", d)
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	if cmd != nil {
		t.Error("a still picture scheduled a tick")
	}
	if len(m.cur().img.rows(20, colorprofile.TrueColor)) == 0 {
		t.Error("the still picture draws nothing")
	}
}

// TestOnlyOneAnimationRunsAtATime is what imgPending is for. Every message asks
// for a tick; without the flag each one would start a chain and the picture would
// run at twice its declared speed.
func TestOnlyOneAnimationRunsAtATime(t *testing.T) {
	dir := t.TempDir()
	p := writeGIF(t, dir, "anim.gif", 2, []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}})
	m := editorAt(t, p, 80, 24)
	// Opening it armed the chain, so the first *new* ask is the one that has to
	// be refused; asking again with nothing pending starts one.
	if cmd := m.animCmd(); cmd != nil {
		t.Error("a second chain started while one was pending")
	}
	m.imgPending = false
	if cmd := m.animCmd(); cmd == nil {
		t.Fatal("with nothing pending, no chain was started")
	}
	if cmd := m.animCmd(); cmd != nil {
		t.Error("a second tick was scheduled while one was pending")
	}
	// The tick itself clears the flag, and the next frame is then schedulable.
	m.imgPending = false
	if cmd := m.animCmd(); cmd == nil {
		t.Error("the chain did not continue after its tick arrived")
	}
}

// TestATallPictureScrolls guards the geometry: the art's height follows the
// aspect ratio, so a tall picture is taller than the pane and must be reachable.
func TestATallPictureScrolls(t *testing.T) {
	dir := t.TempDir()
	// 200x600 at a 78-column pane is far more rows of art than the pane has.
	p := writePNG(t, dir, "tall.png", 200, 600, color.RGBA{R: 200, A: 255})
	m := editorAt(t, p, 80, 24)
	pane := m.paneContentHeight(0) - 1
	maxOff := m.imageScrollMax(0, pane)
	if maxOff <= 0 {
		t.Fatalf("a tall picture has no scroll range (maxOff = %d)", maxOff)
	}

	m.scrollImage(1 << 30)
	if got := m.curPane().offsetY; got != maxOff {
		t.Errorf("scrolled to %d, want the bottom at %d", got, maxOff)
	}
	// A message must not pin it back: the buffer's cursor is on line 0 of an
	// empty buffer, which is exactly what would reset it.
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = nm.(Model)
	if m.curPane().offsetY != maxOff {
		t.Errorf("a message reset the scroll to %d, want %d", m.curPane().offsetY, maxOff)
	}
	m.scrollImage(-1 << 30)
	if m.curPane().offsetY != 0 {
		t.Errorf("scrolled to %d, want the top", m.curPane().offsetY)
	}
	m.scrollImage(-1)
	if m.curPane().offsetY != 0 {
		t.Errorf("scrolled past the top to %d", m.curPane().offsetY)
	}
}

// TestAShortPictureDoesNotScroll: a picture that fits must not leave a scroll
// range behind, or the wheel would appear to do something and change nothing.
//
// The picture has to be *wide*, not small: the art is drawn at the pane's width,
// so a 16x16 image in a 78-column pane is 39 rows of half-blocks — three times
// the pane's height. "Small" is not the property that decides whether it fits;
// the aspect ratio against the pane's is.
func TestAShortPictureDoesNotScroll(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "wide.png", 400, 40, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	if got := m.imageScrollMax(0, m.paneContentHeight(0)-1); got != 0 {
		t.Errorf("a picture that fits has a scroll range of %d", got)
	}
}

// TestTheStatusBarDoesNotReportACursorOnAPicture: "Ln 1, Col 1" under a rendered
// image describes a document that is not there, and an encoding badge on a PNG is
// a question nobody asked.
func TestTheStatusBarDoesNotReportACursorOnAPicture(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 40, 20, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	bar := stripANSI(m.paneStatusBar(0))
	if strings.Contains(bar, "Ln ") {
		t.Errorf("the status bar reports a cursor position: %q", bar)
	}
	if strings.Contains(bar, "UTF-8") || strings.Contains(bar, "LF") {
		t.Errorf("the status bar reports line endings for a picture: %q", bar)
	}
	if !strings.Contains(bar, "40×20") {
		t.Errorf("the status bar does not report the dimensions: %q", bar)
	}
}

// TestEachFrameIsActuallyDrawn: the frames and the drawing are both correct in
// isolation and the composition still wrong — a GIF frame is a rectangle drawn
// over the last one, and a renderer that skips that shows an empty cell for every
// partial frame. Comparing the drawn rows across frames is the only way to see it.
func TestEachFrameIsActuallyDrawn(t *testing.T) {
	dir := t.TempDir()
	p := writeGIF(t, dir, "anim.gif", 2, []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}})
	m := editorAt(t, p, 80, 24)
	first := paneArt(m)
	m.advanceFrame()
	second := paneArt(m)
	if first == "" || second == "" {
		t.Fatal("a frame drew nothing")
	}
	if first == second {
		t.Error("both frames drew the same rows; the second frame is not being drawn")
	}
	if !strings.Contains(first, "▀") || !strings.Contains(second, "▀") {
		t.Error("a frame drew no half blocks")
	}
}

// TestTheFrameCounterIsVisible: a GIF with no frame counter reads as a flicker, so
// the caption has to say where it is.
func TestTheFrameCounterIsVisible(t *testing.T) {
	dir := t.TempDir()
	p := writeGIF(t, dir, "anim.gif", 3, []color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}})
	m := editorAt(t, p, 80, 24)
	if !strings.Contains(paneText(m), "1/3") {
		t.Errorf("the caption has no frame counter:\n%s", paneText(m))
	}
	m.advanceFrame()
	if !strings.Contains(paneText(m), "2/3") {
		t.Errorf("the frame counter did not move:\n%s", paneText(m))
	}
}

// TestTheFrameDelayIsWhatTheFileSays: the tick is scheduled from the GIF's own
// numbers, so a file that declares 4 hundredths animates at 4 hundredths and not
// at some constant of this file's own.
func TestTheFrameDelayIsWhatTheFileSays(t *testing.T) {
	dir := t.TempDir()
	p := writeGIF(t, dir, "anim.gif", 2, []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}})
	m := editorAt(t, p, 80, 24)
	if got, want := m.animationDelay(), 40*time.Millisecond; got != want {
		t.Errorf("animationDelay = %v, want %v", got, want)
	}
}

// TestAPictureReloadsAsAPicture: the watcher's reload must not put the bytes back
// into the buffer, which is the one place where a re-decoded picture silently
// becomes mojibake again.
func TestAPictureReloadsAsAPicture(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{R: 255, A: 255})
	m := editorAt(t, p, 80, 24)
	writePNG(t, dir, "shot.png", 32, 16, color.RGBA{G: 255, A: 255})

	nm, _ := m.Update(FileChangedMsg{Path: p})
	m = nm.(Model)
	if m.cur().img == nil {
		t.Fatal("the reload dropped the picture")
	}
	if got := bufText(m); got != "" {
		t.Errorf("the reload put the bytes back in the buffer: %q", got)
	}
	if !strings.Contains(paneText(m), "32×16") {
		t.Error("the reload did not redraw the new picture")
	}
}

// TestAPictureThatStopsBeingOneFallsBackToText: a .png overwritten with text has
// to open as what it now is. Leaving the tab a picture would show the old frame of
// a file that no longer exists.
func TestAPictureThatStopsBeingOneFallsBackToText(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{R: 255, A: 255})
	m := editorAt(t, p, 80, 24)
	writeTemp(t, dir, "shot.png", "now I am text")

	nm, _ := m.Update(FileChangedMsg{Path: p})
	m = nm.(Model)
	if m.cur().img != nil {
		t.Fatal("a text file is still being drawn as the old picture")
	}
	if got := bufText(m); got != "now I am text" {
		t.Errorf("buffer = %q", got)
	}
	if !strings.Contains(paneText(m), "now I am text") {
		t.Error("the pane does not show the text")
	}
}

// TestTheColorProfileIsRecorded pins the wiring: a picture drawn in truecolor
// escapes on a 16-colour terminal is a field of wrong-coloured blocks, and the
// editor has no other way to learn what the terminal can do.
func TestTheColorProfileIsRecorded(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255})
	m := editorAt(t, p, 80, 24)

	nm, _ := m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	m = nm.(Model)
	if m.profile != colorprofile.ANSI {
		t.Fatalf("profile = %v, want ANSI", m.profile)
	}
	art := strings.Join(m.cur().img.rows(8, m.profileOrDefault()), "")
	if strings.Contains(art, "38;2;") {
		t.Error("truecolor escapes were sent to a 16-colour profile")
	}
}

// TestASwitchedAwayAnimationStops is the property that makes the chain safe: the
// tick asks who is on screen, and a text tab is nobody.
func TestASwitchedAwayAnimationStops(t *testing.T) {
	dir := t.TempDir()
	gifPath := writeGIF(t, dir, "anim.gif", 2, []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}})
	txt := writeTemp(t, dir, "main.go", "package main\n")
	m := editorAt(t, txt, 80, 24)
	m.openPath(gifPath)
	m.setActiveTab(0) // the text file

	if d := m.animationDelay(); d != 0 {
		t.Errorf("a GIF in another tab is still being ticked: %v", d)
	}
	nm, cmd := m.Update(imgFrameMsg{})
	m = nm.(Model)
	if cmd != nil {
		t.Error("the chain kept running with the animation in another tab")
	}
}

// TestEveryKeyOnAPictureTabIsHarmless walks the editing keys rather than the four
// it would be tempting to check. The rule is that nothing reaches the buffer, and
// a table is the only way to be sure a key added later did not land in the wrong
// switch.
func TestEveryKeyOnAPictureTabIsHarmless(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	keys := []tea.KeyPressMsg{
		{Code: 'a'},
		{Code: tea.KeyEnter},
		{Code: tea.KeyBackspace},
		{Code: tea.KeyDelete},
		{Code: tea.KeyTab},
		{Code: tea.KeySpace},
		{Code: 'z', Mod: tea.ModCtrl},
		{Code: 'y', Mod: tea.ModCtrl},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: 'k', Mod: tea.ModCtrl},
		{Code: 'l', Mod: tea.ModCtrl},
		{Code: '/', Mod: tea.ModCtrl},
		{Code: 'r', Mod: tea.ModCtrl},
		{Code: 'o', Mod: tea.ModCtrl},
		{Code: ' ', Mod: tea.ModCtrl},
		{Code: tea.KeySpace, Mod: tea.ModCtrl},
		{Code: tea.KeyDown, Mod: tea.ModShift},
		{Code: tea.KeyRight, Mod: tea.ModShift},
		{Code: tea.KeyDown, Mod: tea.ModAlt},
		{Code: 'e', Mod: tea.ModAlt},
		{Code: 'i', Mod: tea.ModAlt},
	}
	for _, k := range keys {
		m := editorAt(t, p, 80, 24)
		m = press(m, k)
		if got := bufText(m); got != "" {
			t.Errorf("key %q reached the buffer: %q", k.String(), got)
		}
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the picture was modified on disk")
	}
}

// TestPasteOnAPictureTabIsDropped: a paste arrives as one message carrying the
// whole text, so a check written for key presses alone would miss it entirely.
func TestPasteOnAPictureTabIsDropped(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	nm, _ := m.Update(tea.PasteMsg{Content: "package pasted"})
	m = nm.(Model)
	if got := bufText(m); got != "" {
		t.Errorf("a paste reached the buffer of a picture tab: %q", got)
	}
}

// TestTabSwitchingWorksOnAPictureTab is the other side of the key table: a tab
// full of dropped keys would be a trap, so the keys that are about the workspace
// have to keep working.
func TestTabSwitchingWorksOnAPictureTab(t *testing.T) {
	dir := t.TempDir()
	pic := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	txt := writeTemp(t, dir, "main.go", "package main\n")
	m := editorAt(t, pic, 80, 24)
	m.openPath(txt)

	m = press(m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	if m.activeTabIndex() != 0 {
		t.Fatalf("Alt+Left did not leave the picture tab (active %d)", m.activeTabIndex())
	}
	if m.cur().img == nil {
		t.Error("the wrong tab is active")
	}
}

// TestTheTabBarNamesThePicture: a picture tab's name is its file, like any other,
// so the bar cannot be the thing that fails to say what is open.
func TestTheTabBarNamesThePicture(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	if got := stripANSI(m.tabBar()); !strings.Contains(got, "shot.png") {
		t.Errorf("tab bar = %q, want the file name", got)
	}
}

// TestAPictureCannotBeTypedIntoOrSavedOver is the safety property. Every key that
// would reach the buffer is pressed, and then the file on disk is compared with
// the file that was written: a version that lets one key through loses the
// picture, and it loses it silently.
func TestAPictureCannotBeTypedIntoOrSavedOver(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 32, 32, color.RGBA{B: 255, A: 255})
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m := editorAt(t, p, 80, 24)

	m = typeStr(m, "hello")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = press(m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})

	if got := bufText(m); got != "" {
		t.Errorf("keys reached the buffer of a picture tab: %q", got)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the picture on disk was overwritten by an empty buffer")
	}
	if m.msg == "" {
		t.Error("Ctrl+S on a picture said nothing")
	}
}

// TestAPictureIsNotDirty pins what the previous test depends on: a picture tab is
// clean, so closing the editor over it never asks about unsaved changes.
func TestAPictureIsNotDirty(t *testing.T) {
	dir := t.TempDir()
	p := writePNG(t, dir, "shot.png", 16, 16, color.RGBA{A: 255})
	m := editorAt(t, p, 80, 24)
	for i := range m.tabs {
		if m.tabs[i].buf.Dirty() {
			t.Fatal("a freshly opened picture tab is dirty")
		}
	}
	if m.hasDirty() {
		t.Error("hasDirty over a picture tab")
	}
}

// TestTextFilesAreUnaffected is the guard on the other side: the decode attempt
// must not turn a source file into a picture, which is what a content sniff
// instead of an extension list would do to every file the editor opens.
func TestTextFilesAreUnaffected(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "main.go", "package main\n")
	m := editorAt(t, p, 80, 24)
	if m.cur().img != nil {
		t.Fatal("a Go file was opened as a picture")
	}
	if got := bufText(m); got != "package main" {
		t.Errorf("buffer = %q", got)
	}
	if !strings.Contains(paneText(m), "package main") {
		t.Error("the text pane lost its content")
	}
}

// TestAPictureThatIsNotOneFallsBackToTextWithAReason: a .png that is not a PNG must
// open as text *and say so*. The text alone is mojibake, and the message alone is
// a tab that refuses to open.
func TestAPictureThatIsNotOneFallsBackToTextWithAReason(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "fake.png", "this is not a png")
	m := editorAt(t, p, 80, 24)
	if m.cur().img != nil {
		t.Fatal("a text file named .png was drawn as a picture")
	}
	if got := bufText(m); got != "this is not a png" {
		t.Errorf("buffer = %q", got)
	}
	if !strings.Contains(m.msg, "text") {
		t.Errorf("status line = %q, want a note that it opened as text", m.msg)
	}
}
