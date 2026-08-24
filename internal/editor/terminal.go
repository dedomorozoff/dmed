package editor

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Bottom terminal panel: a persistent shell session rendered as the last
// rows of the screen. It is pipe-based (not ConPTY), so it comfortably runs
// build/test/git style commands but not full-screen TUI programs.

var ansiRe = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|][^\x07]*(?:\x07|\x1b\\\\)|[@-Z\\\\-_])")

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// TerminalOutputMsg delivers a batch of output lines from the shell.
type TerminalOutputMsg struct{ Lines []string }

func waitForTermOutput(ch <-chan []string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		lines, ok := <-ch
		if !ok {
			return nil
		}
		return TerminalOutputMsg{Lines: lines}
	}
}

// shellCommand picks the shell for the bottom panel: DMED_SHELL overrides,
// then the platform default (COMSPEC/cmd.exe on Windows, $SHELL/sh elsewhere;
// SHELL is ignored on Windows where it often points to a POSIX path).
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

func termNewline() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func (m *Model) ensureShell() {
	if m.termCmd != nil && m.termCmd.Process != nil {
		return // session already running (or already dead-and-replaced lazily)
	}
	cmd := exec.Command(m.shellCommand())
	cmd.Dir = m.baseDir()

	outR, outW, err := os.Pipe()
	if err != nil {
		m.msg = "terminal: " + err.Error()
		return
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		m.msg = "terminal: " + err.Error()
		return
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		m.msg = "terminal: " + err.Error()
		return
	}
	m.termCmd = cmd
	m.termStdin = inW

	ch := make(chan []string, 32)
	m.termCh = ch

	// Reader: raw lines -> lineCh. Pump: batches lineCh -> ch so the UI
	// gets output at most ~30ms late even when few new lines arrive.
	lineCh := make(chan string, 512)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lineCh <- strings.TrimRight(stripANSI(sc.Text()), "\r")
		}
		close(lineCh)
		outW.Close()
	}()
	go func() {
		var batch []string
		send := func() {
			if len(batch) == 0 {
				return
			}
			select {
			case ch <- batch:
			default: // drop when the UI cannot keep up
			}
			batch = nil
		}
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case l, ok := <-lineCh:
				if !ok {
					send()
					close(ch)
					return
				}
				batch = append(batch, l)
				if len(batch) >= 16 {
					send()
				}
			case <-tick.C:
				send()
			}
		}
	}()
	go func() { _ = cmd.Wait() }()
}

func (m *Model) toggleTerminal() tea.Cmd {
	if m.termOpen {
		m.termOpen = false
		m.msg = ""
		return nil
	}
	m.ensureShell()
	if m.termCmd == nil {
		return nil
	}
	m.termOpen = true
	m.termIn = nil
	m.termScroll = 0
	m.msg = ""
	return waitForTermOutput(m.termCh)
}

func (m *Model) killTerminal() {
	if m.termStdin != nil {
		_ = m.termStdin.Close()
		m.termStdin = nil
	}
	if m.termCmd != nil && m.termCmd.Process != nil {
		_ = m.termCmd.Process.Kill()
	}
	m.termCmd = nil
	m.termOpen = false
}

func (m *Model) termSubmit() {
	line := string(m.termIn)
	m.termIn = nil
	m.termHist = append(m.termHist, line)
	m.termHistIdx = 0
	m.termLines = append(m.termLines, "> "+line)
	m.termScroll = 0 // follow new output
	if m.termStdin != nil {
		_, _ = io.WriteString(m.termStdin, line+termNewline())
	}
}

func (m *Model) termHistory(d int) {
	n := len(m.termHist)
	if n == 0 {
		return
	}
	m.termHistIdx += d
	if m.termHistIdx < 1 {
		m.termHistIdx = 1
	}
	if m.termHistIdx > n {
		m.termHistIdx = n
	}
	m.termIn = []rune(m.termHist[n-m.termHistIdx])
}

func (m *Model) handleTerm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.termOpen = false
		m.msg = ""
	case "enter":
		m.termSubmit()
	case "backspace":
		if n := len(m.termIn); n > 0 {
			m.termIn = m.termIn[:n-1]
		}
	case "up":
		m.termHistory(1)
	case "down":
		if m.termHistIdx > 0 {
			m.termHistory(-1)
		} else if m.termScroll > 0 {
			m.termScroll-- // scroll back toward the bottom
		}
	case "pgup":
		m.termScroll += m.termPanelHeight() / 2
	case "pgdn":
		m.termScroll -= m.termPanelHeight() / 2
		if m.termScroll < 0 {
			m.termScroll = 0
		}
	default:
		switch msg.String() {
		case "ctrl+l":
			m.termLines = nil
			m.termScroll = 0
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.termIn = append(m.termIn, msg.Runes...)
			}
		}
	}
	return nil
}
