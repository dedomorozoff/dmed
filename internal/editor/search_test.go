package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSearchFindNextAndPrev(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "search.txt", "hello world\nfoo hello bar\nhello\n")

	m := New(f)
	m.width, m.height = 80, 24

	// Open search
	m = press(m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.searchOpen {
		t.Fatal("search must be open after Ctrl+F")
	}

	// Type "hello"
	m = typeStr(m, "hello")
	if m.searchTotalMatches != 3 {
		t.Fatalf("expected 3 matches, got %d", m.searchTotalMatches)
	}
	if m.searchMatchIdx != 0 {
		t.Fatalf("expected searchMatchIdx 0, got %d", m.searchMatchIdx)
	}
	if m.cur().buf.CurLine() != 0 || m.cur().buf.Col() != 0 {
		t.Fatalf("expected cursor at (0, 0), got (%d, %d)", m.cur().buf.CurLine(), m.cur().buf.Col())
	}

	// Next match (Enter)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.searchMatchIdx != 1 {
		t.Fatalf("expected searchMatchIdx 1, got %d", m.searchMatchIdx)
	}
	if m.cur().buf.CurLine() != 1 || m.cur().buf.Col() != 4 {
		t.Fatalf("expected cursor at (1, 4), got (%d, %d)", m.cur().buf.CurLine(), m.cur().buf.Col())
	}

	// Next match (F3)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF3})
	if m.searchMatchIdx != 2 {
		t.Fatalf("expected searchMatchIdx 2, got %d", m.searchMatchIdx)
	}
	if m.cur().buf.CurLine() != 2 || m.cur().buf.Col() != 0 {
		t.Fatalf("expected cursor at (2, 0), got (%d, %d)", m.cur().buf.CurLine(), m.cur().buf.Col())
	}

	// Previous match (Up / Ctrl+P)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.searchMatchIdx != 1 {
		t.Fatalf("expected searchMatchIdx 1 after up, got %d", m.searchMatchIdx)
	}

	// Close search (Esc)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.searchOpen {
		t.Fatal("search must be closed after Esc")
	}
}

func TestSearchReplaceOneAndAll(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "rep.txt", "apple orange apple\napple pie\n")

	m := New(f)
	m.width, m.height = 80, 24

	// Open Replace (Ctrl+H)
	m = press(m, tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if !m.replaceOpen || !m.searchOpen {
		t.Fatal("replace and search must be open after Ctrl+H")
	}

	// Switch to find field with Tab or type into find field
	m.replaceFocusFind = true
	m = typeStr(m, "apple")
	if m.searchTotalMatches != 3 {
		t.Fatalf("expected 3 matches for apple, got %d", m.searchTotalMatches)
	}

	// Switch to replace field
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.replaceFocusFind {
		t.Fatal("expected replaceFocusFind false after Tab")
	}
	m = typeStr(m, "banana")

	// Replace first match (Enter)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.HasPrefix(m.cur().buf.Text(), "banana orange apple") {
		t.Fatalf("after replace 1: %q", m.cur().buf.Text())
	}

	// Replace all remaining (Ctrl+A)
	m = press(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	expected := "banana orange banana\nbanana pie\n"
	if m.cur().buf.Text() != expected {
		t.Fatalf("after replace all: want %q, got %q", expected, m.cur().buf.Text())
	}

	// Undo (Ctrl+Z)
	m = press(m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if m.cur().buf.Text() != "banana orange apple\napple pie\n" {
		t.Fatalf("after undo replace all: %q", m.cur().buf.Text())
	}
}

func TestSearchViewHighlight(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "view.go", "package main\nfunc main() {}\n")

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m = typeStr(m, "main")

	v := m.View()
	plain := strings.Join(plainRows(v.Content), "\n")
	if !strings.Contains(plain, "search: main") {
		t.Fatalf("view must show search bar with query, got:\n%s", v.Content)
	}
	if !strings.Contains(plain, "[1/2]") {
		t.Fatalf("view must show match count [1/2], got:\n%s", v.Content)
	}
}
