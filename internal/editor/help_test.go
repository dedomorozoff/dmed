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
	for _, want := range []string{"dmed — keys", "Ctrl+S", "Ctrl+F", "Ctrl+H", "Ctrl+O", "Ctrl+W"} {
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
	for _, want := range []string{"клавиши", "сохранить активную вкладку", "быстрый поиск файлов"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("ru help view missing %q", want)
		}
	}
}
