package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/i18n"
)

func TestHelpToggle(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if m.helpOpen {
		t.Fatal("help must start closed")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if !m.helpOpen {
		t.Fatal("f1 must open help")
	}
	v := m.View()
	// Only the top window is visible on a 24-row terminal; "Ctrl+W" sits below
	// the fold and is checked after scrolling in TestHelpScrolls.
	for _, want := range []string{"dmed — keys", "Ctrl+S", "Ctrl+F", "Ctrl+H", "Ctrl+O", "Ctrl+Alt+D", "F5"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("help view missing %q", want)
		}
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.helpOpen {
		t.Fatal("esc must close help")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if m.helpOpen {
		t.Fatal("f1 again must close help")
	}
}

func TestHelpSwallowsTyping(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	typeStr(m, "hello world")
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatalf("typing while help open must not edit buffer, got %q", m.tabs[0].buf.Text())
	}
}

func TestStatusBarShowsHint(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	if v := m.View(); !strings.Contains(v.Content, "F1 help") {
		t.Fatal("status bar must hint F1 help by default")
	}
}

func TestNulByteIsIgnored(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Text: string('\x00')})
	if m.helpOpen {
		t.Fatal("bare NUL (some stacks send it for bare Ctrl) must not toggle help")
	}
	if m.tabs[0].buf.Text() != "\n" {
		t.Fatal("NUL must not insert into buffer")
	}
}

func TestHelpRussianLocale(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.cfg.UI.Lang = "ru"
	m.tr = i18n.New(i18n.Resolve("ru"))
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	v := m.View()
	for _, want := range []string{"клавиши", "сохранить активную вкладку", "быстрый поиск файлов", "брейкпоинт"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("ru help view missing %q", want)
		}
	}
}

func TestHelpScrolls(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	max := m.helpMaxScroll()
	if max == 0 {
		t.Fatal("help must overflow a 24-row terminal")
	}

	m = press(m, tea.KeyPressMsg{Text: "j"})
	if m.helpScroll != 1 {
		t.Fatalf("j must scroll down one row, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.helpScroll != 2 {
		t.Fatalf("pgdn must scroll, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.helpScroll != 1 {
		t.Fatalf("pgup must scroll up, offset=%d", m.helpScroll)
	}
	m = press(m, tea.KeyPressMsg{Text: "k"})
	if m.helpScroll != 0 {
		t.Fatalf("k must scroll up to top, offset=%d", m.helpScroll)
	}

	// G jumps to the end; further scrolling clamps there. The bottom entries
	// ("Ctrl+W / Ctrl+X" close tab) become visible at the end of the list.
	m = press(m, tea.KeyPressMsg{Text: "G"})
	if m.helpScroll != max {
		t.Fatalf("G must jump to the end, offset=%d max=%d", m.helpScroll, max)
	}
	if v := m.View(); !strings.Contains(v.Content, "Ctrl+W") {
		t.Fatal("bottom of help must show after scrolling to the end")
	}
	m = press(m, tea.KeyPressMsg{Text: "j"})
	if m.helpScroll != max {
		t.Fatalf("j past the end must clamp, offset=%d", m.helpScroll)
	}

	// The mouse wheel scrolls the panel too (routed through Update like the
	// real event flow).
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 40, Y: 5})
	m = next.(Model)
	if m.helpScroll != max-1 {
		t.Fatalf("wheel up must scroll back, offset=%d", m.helpScroll)
	}
	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 40, Y: 5})
	m = next.(Model)
	if m.helpScroll != max {
		t.Fatalf("wheel down must restore the end, offset=%d", m.helpScroll)
	}

	// Esc closes and reopening starts at the top again.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	if m.helpScroll != 0 {
		t.Fatalf("reopening must reset scroll, offset=%d", m.helpScroll)
	}
}
