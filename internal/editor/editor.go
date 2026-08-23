package editor

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/buffer"
)

type tab struct {
	buf     *buffer.Buffer
	path    string
	offsetX int
	offsetY int
}

func (t *tab) name() string {
	if t.path == "" {
		return "[untitled]"
	}
	return t.path
}

type Model struct {
	tabs   []tab
	active int
	width  int
	height int
	msg    string

	promptOpen bool
	promptIn   []rune
}

func New(paths ...string) Model {
	m := Model{width: 80, height: 24}
	for _, p := range paths {
		m.openPath(p)
	}
	if len(m.tabs) == 0 {
		m.tabs = append(m.tabs, tab{buf: buffer.New()})
	}
	return m
}

func (m Model) activeTab() *tab { return &m.tabs[m.active] }

func (m *Model) cur() *tab { return &m.tabs[m.active] }

func (m *Model) openPath(path string) {
	data, err := os.ReadFile(path)
	t := tab{path: path, buf: buffer.New()}
	if err != nil {
		if os.IsNotExist(err) {
			m.msg = "new file: " + path
		} else {
			m.msg = "open failed: " + err.Error()
		}
	} else {
		t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
	}
	m.tabs = append(m.tabs, t)
	m.active = len(m.tabs) - 1
}

func (m *Model) switchTab(d int) {
	n := len(m.tabs)
	if n == 0 {
		return
	}
	m.active = ((m.active+d)%n + n) % n
}

func (m *Model) jumpTab(n int) {
	if n >= 0 && n < len(m.tabs) {
		m.active = n
	}
}

func (m *Model) closeTab() tea.Cmd {
	m.tabs = append(m.tabs[:m.active], m.tabs[m.active+1:]...)
	if len(m.tabs) == 0 {
		return tea.Quit
	}
	if m.active >= len(m.tabs) {
		m.active = len(m.tabs) - 1
	}
	return nil
}

func (m *Model) startPrompt() {
	m.promptOpen = true
	m.promptIn = nil
}

func (m *Model) handlePrompt(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.promptOpen = false
	case "enter":
		path := strings.TrimSpace(string(m.promptIn))
		m.promptOpen = false
		if path != "" {
			m.openPath(path)
		}
	case "backspace":
		if n := len(m.promptIn); n > 0 {
			m.promptIn = m.promptIn[:n-1]
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.promptIn = append(m.promptIn, msg.Runes...)
		}
	}
	return nil
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case tea.KeyMsg:
		if !m.promptOpen && (msg.String() == "ctrl+c" || msg.String() == "ctrl+q") {
			return m, tea.Quit
		}
		if cmd := m.handleKey(msg); cmd != nil {
			return m, cmd
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	if m.promptOpen {
		return m.handlePrompt(msg)
	}
	if len(s) == 5 && strings.HasPrefix(s, "alt+") && s[4] >= '1' && s[4] <= '9' {
		m.jumpTab(int(s[4] - '1'))
		return nil
	}
	switch s {
	case "ctrl+s":
		m.saveActive()
	case "ctrl+z":
		if m.cur().buf.Undo() {
			m.msg = ""
		}
	case "ctrl+y", "ctrl+r":
		if m.cur().buf.Redo() {
			m.msg = ""
		}
	case "ctrl+t":
		m.startPrompt()
	case "ctrl+w":
		if cmd := m.closeTab(); cmd != nil {
			return cmd
		}
	case "alt+left":
		m.switchTab(-1)
	case "alt+right":
		m.switchTab(1)
	case "up":
		m.cur().buf.MoveUp()
	case "down":
		m.cur().buf.MoveDown()
	case "left":
		m.cur().buf.MoveLeft()
	case "right":
		m.cur().buf.MoveRight()
	case "home":
		m.cur().buf.LineStart()
	case "end":
		m.cur().buf.LineEnd()
	case "pgup":
		t := m.cur()
		for i := 0; i < m.viewHeight()-2 && t.buf.CurLine() > 0; i++ {
			t.buf.MoveUp()
		}
	case "pgdown":
		t := m.cur()
		for i := 0; i < m.viewHeight()-2 && t.buf.CurLine() < t.buf.LineCount()-1; i++ {
			t.buf.MoveDown()
		}
	case "enter":
		m.cur().buf.InsertNewline()
		m.msg = ""
	case "backspace":
		m.cur().buf.Backspace()
		m.msg = ""
	case "delete":
		m.cur().buf.Delete()
		m.msg = ""
	case "tab":
		m.cur().buf.Insert('\t')
		m.msg = ""
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			for _, r := range msg.Runes {
				m.cur().buf.Insert(r)
			}
			m.msg = ""
		} else {
			return nil
		}
	}
	return nil
}

func (m *Model) saveActive() {
	t := m.cur()
	if t.path == "" {
		m.msg = "cannot save: no file name"
		return
	}
	if err := os.WriteFile(t.path, []byte(t.buf.Text()), 0o644); err != nil {
		m.msg = "save failed: " + err.Error()
		return
	}
	t.buf.MarkSaved()
	m.msg = "saved"
}

func (m *Model) clampScroll() {
	t := m.cur()
	h := m.viewHeight()
	cur := t.buf.CurLine()
	if h > 0 {
		if cur < t.offsetY {
			t.offsetY = cur
		}
		if cur >= t.offsetY+h {
			t.offsetY = cur - h + 1
		}
	}
	w := m.viewWidth()
	if w <= 0 {
		return
	}
	x := visCol(t.buf.LineAt(cur), t.buf.Col())
	if x < t.offsetX {
		t.offsetX = x
	}
	if x >= t.offsetX+w {
		t.offsetX = x - w + 1
	}
}
