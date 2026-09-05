package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCompletionWordTriggerAndAccept(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "c.txt", "hello world\nhelmet head\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Text: "h"})
	m = press(m, tea.KeyPressMsg{Text: "e"})
	if !m.complOpen {
		t.Fatal("popup must open after typing 'he'")
	}
	if len(m.complItems) == 0 {
		t.Fatal("no candidates")
	}
	hasHelmet := false
	for _, c := range m.complItems {
		if c == "helmet" {
			hasHelmet = true
		}
	}
	if !hasHelmet {
		t.Fatalf("'helmet' missing from candidates %v", m.complItems)
	}

	// Tab accepts the selected (first, sorted) candidate.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	got := m.cur().buf.Text()
	if strings.Contains(got, "hehello") {
		t.Fatalf("word was not replaced, buffer=%q", got)
	}
	if m.complOpen {
		t.Fatal("popup must close after accepting")
	}
}

func TestCompletionNavigationAndDismiss(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "n.txt", "alpha beta\nalpine\ngamma\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Text: "al"})
	if !m.complOpen || len(m.complItems) < 2 {
		t.Fatalf("popup with candidates expected, got %v", m.complItems)
	}
	first := m.complSel
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.complSel == first {
		t.Fatal("down did not move selection")
	}
	// Esc dismisses.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.complOpen {
		t.Fatal("esc must dismiss popup")
	}
}

func TestCompletionCtrlSpace(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "s.txt", "alpha beta gamma\n")
	m := New(f)
	m.width, m.height = 80, 24

	// Position cursor at line start (no prefix); Ctrl+Space lists everything.
	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if !m.complOpen || len(m.complItems) == 0 {
		t.Fatalf("Ctrl+Space should list identifiers, got %v", m.complItems)
	}
}

func TestCompletionFloatsUnderCursor(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "f.txt", "hello world\nhelmet head\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.cur().buf.SetCursor(0, 0)
	m = press(m, tea.KeyPressMsg{Text: "he"})
	if !m.complOpen || len(m.complItems) == 0 {
		t.Fatalf("popup must be open with candidates, got %v", m.complItems)
	}

	_, sy := m.cursorScreenPos()
	v := m.View()
	lines := strings.Split(strings.TrimRight(v.Content, "\n"), "\n")

	// The popup title must sit right below the cursor line, inline with the
	// editor text — not pinned to the bottom of the screen.
	title := m.t("compl.title")
	caught := false
	for i, ln := range lines {
		plain := stripANSI(ln)
		if strings.Contains(plain, title) {
			if i != sy+1 {
				t.Fatalf("completion title on screen row %d, want %d (under cursor)", i, sy+1)
			}
			caught = true
		}
	}
	if !caught {
		t.Fatal("completion popup missing from view")
	}
}
