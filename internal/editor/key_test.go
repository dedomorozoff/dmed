package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCtrlPlainControlByteKeepsAlt(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	// On some terminals Ctrl+Alt+letter arrives as ESC + control byte; the
	// parser already flags ModAlt, so it must survive normalization.
	m = press(m, tea.KeyPressMsg{Text: "\x08", Mod: tea.ModAlt})
	if m.layout != splitHoriz {
		t.Fatalf("ctrl+alt+h must split horizontally, layout=%d", m.layout)
	}
	if m.searchOpen || m.replaceOpen {
		t.Fatal("ctrl+alt+h must not reduce to ctrl+h and open replace")
	}
}

func TestCtrlAltControlByteInCode(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	m = press(m, tea.KeyPressMsg{Code: 0x08, Mod: tea.ModAlt})
	if m.layout != splitHoriz {
		t.Fatalf("ctrl+alt+h via control-byte code must split horizontally, layout=%d", m.layout)
	}
}

func TestCtrlSpaceFromNUL(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	m = press(m, tea.KeyPressMsg{Text: "\x00"})
	if !m.complOpen {
		t.Fatal("NUL must normalize to ctrl+space and open completion")
	}
	if m.cur().buf.Dirty() {
		t.Fatal("NUL must not be inserted into the buffer")
	}
}

func TestControlByteInCodeNormalizes(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	// Lone 0x0f as Code (some stacks report control bytes there) → ctrl+o.
	m = press(m, tea.KeyPressMsg{Code: 0x0f})
	if !m.finderOpen {
		t.Fatal("control-byte code 0x0f must normalize to ctrl+o and open finder")
	}
}

func TestCtrlCyrillicEmptyText(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "a\nb\n")
	chdir(t, dir)

	m := New(f1)
	// 'в' is the Cyrillic letter on the physical 'd' key → ctrl+d.
	m = press(m, tea.KeyPressMsg{Code: 'в', Mod: tea.ModCtrl})
	if got := strings.Count(m.cur().buf.Text(), "a"); got != 2 {
		t.Fatalf("ctrl+в must act as ctrl+d (duplicate line), 'a' count=%d", got)
	}
}

func TestCyrillicTextInsertsAsTyped(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "x\n")
	chdir(t, dir)

	m := New(f1)
	m = press(m, tea.KeyPressMsg{Text: "Ф"})
	if got := m.cur().buf.Text(); !strings.Contains(got, "Ф") {
		t.Fatalf("uppercase Cyrillic must be inserted verbatim into the buffer, got %q", got)
	}
}

func TestUppercaseCyrillicCtrlMapsToLatin(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "x.txt", "a\nb\n")
	chdir(t, dir)

	m := New(f1)
	// Uppercase 'В' is also the physical 'd' key → ctrl+d must still fire.
	m = press(m, tea.KeyPressMsg{Code: 'В', Mod: tea.ModCtrl})
	if got := strings.Count(m.cur().buf.Text(), "a"); got != 2 {
		t.Fatalf("ctrl+В must act as ctrl+d, 'a' count=%d", got)
	}
}
