package editor

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func altT() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt}
}

func TestTerminalShellSelection(t *testing.T) {
	m := New()
	t.Setenv("DMED_SHELL", "C:\\fake\\shell.exe")
	if got := m.shellCommand(); got != "C:\\fake\\shell.exe" {
		t.Fatalf("DMED_SHELL must win, got %q", got)
	}
	t.Setenv("DMED_SHELL", "")
	got := m.shellCommand()
	if got == "" || strings.Contains(got, "/bin/") && runtime.GOOS == "windows" {
		t.Fatalf("platform default shell must be usable, got %q", got)
	}
	if termNewline() == "" {
		t.Fatal("newline must not be empty")
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[32mgreen\x1b[0m plain \x1b[1;34mblue\x1b[m tail"
	want := "green plain blue tail"
	if got := stripANSI(in); got != want {
		t.Fatalf("stripANSI = %q, want %q", got, want)
	}
	if got := stripANSI("no escapes"); got != "no escapes" {
		t.Fatalf("plain text mangled: %q", got)
	}
}

func TestTerminalToggleAndInput(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	hBefore := m.viewHeight()
	next, cmd := m.Update(altT())
	m = next.(Model)
	if !m.termOpen {
		t.Fatal("alt+t must open the terminal panel")
	}
	if cmd == nil {
		t.Fatal("opening the terminal must arm the output listener")
	}
	if h := m.viewHeight(); h >= hBefore {
		t.Fatalf("editor area must shrink when terminal is open: %d (was %d)", h, hBefore)
	}
	if len(m.termLines) != 0 {
		t.Fatalf("expected no output yet, got %v", m.termLines)
	}

	// Typing goes to the input line; Enter echoes and submits
	m = typeStr(m, "echo dmed_term_ok")
	if string(m.termIn) != "echo dmed_term_ok" {
		t.Fatalf("input line = %q", string(m.termIn))
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if string(m.termIn) != "" {
		t.Fatal("enter must clear the input line")
	}
	found := false
	for _, l := range m.termLines {
		if strings.Contains(l, "> echo dmed_term_ok") {
			found = true
		}
	}
	if !found {
		t.Fatalf("submitted command must be echoed, lines=%v", m.termLines)
	}

	// Esc closes the panel but keeps the session
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.termOpen {
		t.Fatal("esc must close the terminal panel")
	}
	if m.termCmd == nil {
		t.Fatal("shell session should stay alive after closing the panel")
	}
	m.killTerminal()
}

func TestTerminalRunsCommand(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	next, _ := m.Update(altT())
	m = next.(Model)

	m.termIn = []rune("echo dmed_term_ok")
	m.termSubmit()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case batch, ok := <-m.termCh:
			if !ok {
				t.Fatal("shell output channel closed before command result")
			}
			m.termLines = append(m.termLines, batch...)
			for _, l := range batch {
				if strings.Contains(l, "dmed_term_ok") && !strings.HasPrefix(l, ">") {
					m.killTerminal()
					return
				}
			}
		case <-deadline:
			m.killTerminal()
			t.Fatalf("timed out waiting for echo output, lines=%v", m.termLines)
		}
	}
}

func TestTerminalHistory(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	m.termIn = []rune("first")
	m.termSubmit()
	m.termIn = []rune("second")
	m.termSubmit()

	m.termHistory(1) // one back
	if string(m.termIn) != "second" {
		t.Fatalf("history up = %q, want %q", string(m.termIn), "second")
	}
	m.termHistory(1)
	if string(m.termIn) != "first" {
		t.Fatalf("history up = %q, want %q", string(m.termIn), "first")
	}
	m.termHistory(1)
	if string(m.termIn) != "first" {
		t.Fatalf("history must clamp at oldest, got %q", string(m.termIn))
	}
	m.termHistory(-1)
	if string(m.termIn) != "second" {
		t.Fatalf("history down = %q, want %q", string(m.termIn), "second")
	}
}

func TestTerminalPanelRows(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	next, _ := m.Update(altT())
	m = next.(Model)
	defer m.killTerminal()

	for i := 0; i < 50; i++ {
		m.termLines = append(m.termLines, "line")
	}
	rows := plainRows(strings.Join(m.terminalPanel(), "\n"))
	want := m.termPanelHeight()
	if len(rows) != want {
		t.Fatalf("panel renders %d rows, want exactly %d", len(rows), want)
	}
	// Input line is always the last row
	last := rows[len(rows)-1]
	var b strings.Builder
	inEsc := false
	for _, r := range last { // strip styles for the check below
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
	if !strings.Contains(b.String(), ">") {
		t.Fatalf("last panel row must be the input line, got %q", b.String())
	}
}
