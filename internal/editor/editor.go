package editor

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/buffer"
)

type Model struct {
	buf     *buffer.Buffer
	path    string
	width   int
	height  int
	offsetX int
	offsetY int
	msg     string
}

func New(path string) Model {
	m := Model{width: 80, height: 24}
	if path == "" {
		m.buf = buffer.New()
		m.path = "[untitled]"
		m.msg = "new buffer"
		return m
	}
	m.path = path
	data, err := os.ReadFile(path)
	if err != nil {
		m.buf = buffer.New()
		if os.IsNotExist(err) {
			m.msg = "new file"
		} else {
			m.msg = "open failed: " + err.Error()
		}
		return m
	}
	m.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
	return m
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
		if msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			return m, tea.Quit
		}
		m.handleKey(msg)
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "ctrl+s":
		m.save()
		return
	case "ctrl+z":
		if m.buf.Undo() {
			m.msg = ""
		}
		return
	case "ctrl+y", "ctrl+r":
		if m.buf.Redo() {
			m.msg = ""
		}
		return
	case "up":
		m.buf.MoveUp()
		return
	case "down":
		m.buf.MoveDown()
		return
	case "left":
		m.buf.MoveLeft()
		return
	case "right":
		m.buf.MoveRight()
		return
	case "home":
		m.buf.LineStart()
		return
	case "end":
		m.buf.LineEnd()
		return
	case "pgup":
		n := m.viewHeight() - 2
		for i := 0; i < n && m.buf.CurLine() > 0; i++ {
			m.buf.MoveUp()
		}
		return
	case "pgdown":
		n := m.viewHeight() - 2
		for i := 0; i < n && m.buf.CurLine() < m.buf.LineCount()-1; i++ {
			m.buf.MoveDown()
		}
		return
	case "enter":
		m.buf.InsertNewline()
	case "backspace":
		m.buf.Backspace()
	case "delete":
		m.buf.Delete()
	case "tab":
		m.buf.Insert('\t')
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			for _, r := range msg.Runes {
				m.buf.Insert(r)
			}
		} else {
			return
		}
	}
	m.msg = ""
}

func (m *Model) save() {
	if m.path == "[untitled]" {
		m.msg = "cannot save: no file name"
		return
	}
	if err := os.WriteFile(m.path, []byte(m.buf.Text()), 0o644); err != nil {
		m.msg = "save failed: " + err.Error()
		return
	}
	m.buf.MarkSaved()
	m.msg = "saved"
}

func (m *Model) clampScroll() {
	h := m.viewHeight()
	cur := m.buf.CurLine()
	if h > 0 {
		if cur < m.offsetY {
			m.offsetY = cur
		}
		if cur >= m.offsetY+h {
			m.offsetY = cur - h + 1
		}
	}
	w := m.viewWidth()
	if w <= 0 {
		return
	}
	x := visCol(m.buf.LineAt(cur), m.buf.Col())
	if x < m.offsetX {
		m.offsetX = x
	}
	if x >= m.offsetX+w {
		m.offsetX = x - w + 1
	}
}
