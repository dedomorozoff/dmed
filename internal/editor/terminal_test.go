package editor

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hinshun/vt10x"
)

func altT() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt} }

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
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[32mgreen\x1b[0m plain"); got != "green plain" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalScreenParsesANSI(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 3))
	_, _ = term.Write([]byte("one\x1b[2;3Htwo\x1b[31mred\x1b[0m"))
	rows := captureTerminalRows(term)
	if got := rows[0].text; got != "one" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := rows[1].text; got != "  twored" {
		t.Fatalf("cursor addressing failed: %q", got)
	}
}

func TestTerminalKeyEncoding(t *testing.T) {
	tests := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "\r"},
		{tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{tea.KeyPressMsg{Text: "я"}, "я"},
		{tea.KeyPressMsg{Text: "x", Mod: tea.ModAlt}, "\x1bx"},
	}
	for _, tt := range tests {
		if got := string(terminalKeyBytes(tt.msg)); got != tt.want {
			t.Fatalf("key %v = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestTerminalToggleAndCommand(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	next, cmd := m.Update(altT())
	m = next.(Model)
	defer m.killTerminal()
	if !m.termOpen || m.termSession == nil || cmd == nil {
		t.Fatal("Alt+T must open a live PTY")
	}
	if h := m.viewHeight(); h >= 24 {
		t.Fatalf("editor did not shrink: %d", h)
	}
	for _, key := range []tea.KeyPressMsg{{Text: "echo dmed_term_ok"}, {Code: tea.KeyEnter}} {
		m = press(m, key)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-m.termCh:
			m.termRows = msg.rows
			if strings.Contains(strings.Join(rowTexts(m.termRows), "\n"), "dmed_term_ok") {
				return
			}
		case <-deadline:
			t.Fatalf("timed out; rows=%v", rowTexts(m.termRows))
		}
	}
}

func rowTexts(rows []terminalRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].text
	}
	return out
}

func TestTerminalPanelRows(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.termOpen = true
	rows := plainRows(strings.Join(m.terminalPanel(), "\n"))
	if len(rows) != m.termPanelHeight() {
		t.Fatalf("got %d rows", len(rows))
	}
}
