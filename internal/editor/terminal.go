package editor

import (
	"fmt"
	"image/color"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hinshun/vt10x"

	"dmed/internal/debug"
	"dmed/internal/ptyterm"
)

// Bottom terminal panel backed by a real PTY and a small ANSI screen emulator.

var ansiRe = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|][^\x07]*(?:\x07|\x1b\\\\)|[@-Z\\\\-_])")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

type terminalOutputMsg struct {
	gen    int
	rows   []terminalRow
	err    error
	exited bool
}

type terminalRow struct {
	text  string
	cells []vt10x.Glyph
}
type terminalExitMsg struct {
	gen int
	err error
}

func waitForTermOutput(ch <-chan terminalOutputMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}
func waitForTermExit(ch <-chan terminalExitMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (m *Model) shellCommand() string {
	if s := os.Getenv("DMED_SHELL"); s != "" {
		return s
	}
	if runtime.GOOS == "windows" {
		if s := os.Getenv("COMSPEC"); s != "" {
			return s
		}
		return "cmd.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func (m *Model) ensureShell() {
	if m.termSession != nil {
		return
	}
	width, height := m.terminalGeometry()
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	if runtime.GOOS == "windows" {
		env = append(env, "TERM=xterm-256color")
	}
	term, err := ptyterm.Start(ptyterm.Options{
		Command: m.shellCommand(), Dir: m.baseDir(), Env: env,
		Width: width, Height: height,
	})
	if err != nil {
		m.msg = "terminal: " + err.Error()
		return
	}
	m.termGen++
	gen := m.termGen
	m.termSession = term
	m.termVT = vt10x.New(vt10x.WithSize(width, height))
	m.termCh = make(chan terminalOutputMsg, 64)
	exitCh := make(chan terminalExitMsg, 1)
	m.termExitCh = exitCh
	outCh := m.termCh

	termVT := m.termVT
	go debug.CapturePanicReport(func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				_, _ = termVT.Write(buf[:n])
				rows := captureTerminalRows(termVT)
				select {
				case outCh <- terminalOutputMsg{gen: gen, rows: rows}:
				case <-time.After(2 * time.Second):
				}
			}
			if err != nil {
				rows := captureTerminalRows(termVT)
				select {
				case outCh <- terminalOutputMsg{gen: gen, rows: rows, err: err, exited: true}:
				case <-time.After(2 * time.Second):
				}
				return
			}
		}
	})
	go debug.CapturePanicReport(func() {
		err := term.Wait()
		select {
		case exitCh <- terminalExitMsg{gen: gen, err: err}:
		case <-time.After(time.Second):
		}
	})
}

func captureTerminalRows(term vt10x.Terminal) []terminalRow {
	term.Lock()
	defer term.Unlock()
	cols, rows := term.Size()
	out := make([]terminalRow, rows)
	for y := 0; y < rows; y++ {
		cells := make([]vt10x.Glyph, cols)
		for x := 0; x < cols; x++ {
			cells[x] = term.Cell(x, y)
		}
		out[y] = terminalRow{cells: cells, text: terminalRowText(cells)}
	}
	return out
}

func (m *Model) toggleTerminal() tea.Cmd {
	if m.termOpen {
		m.termOpen = false
		m.msg = ""
		return nil
	}
	m.ensureShell()
	if m.termSession == nil {
		return nil
	}
	m.termOpen = true
	m.msg = ""
	return tea.Batch(waitForTermOutput(m.termCh), waitForTermExit(m.termExitCh))
}

func (m *Model) killTerminal() {
	m.termGen++
	if m.termSession != nil {
		_ = m.termSession.Close()
	}
	m.termSession = nil
	m.termVT = nil
	m.termCh = nil
	m.termExitCh = nil
	m.termOpen = false
}

func terminalKeyBytes(msg tea.KeyPressMsg) []byte {
	s := msg.String()
	if msg.Mod&tea.ModCtrl != 0 {
		key := msg.Code
		if len(msg.Text) > 0 {
			key = []rune(msg.Text)[0]
		}
		if key >= 'a' && key <= 'z' {
			return []byte{byte(key - 'a' + 1)}
		}
		if key >= 'A' && key <= 'Z' {
			return []byte{byte(key - 'A' + 1)}
		}
	}
	switch s {
	case "enter":
		return []byte{'\r'}
	case "tab":
		return []byte{'\t'}
	case "backspace":
		return []byte{0x7f}
	case "escape":
		return []byte{0x1b}
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "delete":
		return []byte("\x1b[3~")
	case "pageup":
		return []byte("\x1b[5~")
	case "pagedown":
		return []byte("\x1b[6~")
	case "insert":
		return []byte("\x1b[2~")
	case "f1":
		return []byte("\x1bOP")
	case "f2":
		return []byte("\x1bOQ")
	case "f3":
		return []byte("\x1bOR")
	case "f4":
		return []byte("\x1bOS")
	case "f5":
		return []byte("\x1b[15~")
	case "f6":
		return []byte("\x1b[17~")
	case "f7":
		return []byte("\x1b[18~")
	case "f8":
		return []byte("\x1b[19~")
	case "f9":
		return []byte("\x1b[20~")
	case "f10":
		return []byte("\x1b[21~")
	case "f11":
		return []byte("\x1b[23~")
	case "f12":
		return []byte("\x1b[24~")
	}
	if len(msg.Text) > 0 {
		if msg.Mod&tea.ModAlt != 0 {
			return append([]byte{0x1b}, []byte(msg.Text)...)
		}
		return []byte(msg.Text)
	}
	return nil
}

func (m *Model) forwardTerminalMouse(button, x, y int) {
	m.forwardTerminalMouseEvent(button, x, y, 'M')
}

func (m *Model) forwardTerminalMouseEvent(button, x, y int, final byte) {
	if m.termVT == nil || m.termSession == nil {
		return
	}
	m.termVT.Lock()
	mouseEnabled := m.termVT.Mode()&vt10x.ModeMouseMask != 0
	m.termVT.Unlock()
	if !mouseEnabled {
		return
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	_, _ = fmt.Fprintf(m.termSession, "\x1b[<%d;%d;%d%c", button, x+1, y+1, final)
}

func (m *Model) handleTerm(msg tea.KeyPressMsg) tea.Cmd {
	if data := terminalKeyBytes(msg); len(data) > 0 && m.termSession != nil {
		if _, err := m.termSession.Write(data); err != nil {
			m.msg = "terminal: " + err.Error()
		}
	}
	return nil
}

func (m *Model) resizeTerminal() {
	if m.termSession == nil || m.termVT == nil {
		return
	}
	w, h := m.terminalGeometry()
	if w > 0 && h > 0 {
		m.termVT.Resize(w, h)
		_ = m.termSession.Resize(w, h)
	}
}

func terminalRowText(cells []vt10x.Glyph) string {
	var b strings.Builder
	for _, c := range cells {
		r := c.Char
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

func terminalColor(c vt10x.Color, fg bool) (color.Color, bool) {
	if c == vt10x.DefaultFG || c == vt10x.DefaultBG || c == vt10x.DefaultCursor {
		return nil, false
	}
	if c < 16 {
		colors := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}
		return lipgloss.Color(colors[c]), true
	}
	if c < 232 {
		n := int(c) - 16
		levels := []int{0, 95, 135, 175, 215, 255}
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", levels[n/36], levels[(n/6)%6], levels[n%6])), true
	}
	if c < 256 {
		v := 8 + (int(c)-232)*10
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", v, v, v)), true
	}
	r := int(c >> 16)
	g := int(c >> 8 & 0xff)
	b := int(c & 0xff)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b)), true
}

func renderTerminalRow(row terminalRow, width, cursorX int) string {
	if width < 1 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < width && i < len(row.cells); i++ {
		c := row.cells[i]
		ch := string(c.Char)
		if c.Char == 0 {
			ch = " "
		}
		style := lipgloss.NewStyle()
		if fg, ok := terminalColor(c.FG, true); ok {
			style = style.Foreground(fg)
		}
		if bg, ok := terminalColor(c.BG, false); ok {
			style = style.Background(bg)
		}
		if c.Mode&1 != 0 {
			style = style.Reverse(true)
		}
		if c.Mode&2 != 0 {
			style = style.Underline(true)
		}
		if c.Mode&4 != 0 {
			style = style.Bold(true)
		}
		if c.Mode&16 != 0 {
			style = style.Italic(true)
		}
		if i == cursorX {
			style = style.Reverse(true)
		}
		b.WriteString(style.Render(ch))
	}
	return b.String()
}
