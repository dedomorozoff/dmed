package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/buffer"
)

type tab struct {
	buf  *buffer.Buffer
	path string
}

func (t *tab) name(base string) string {
	if t.path == "" {
		return "[untitled]"
	}
	return shortenPath(base, t.path)
}

type Model struct {
	root   string
	tabs   []tab
	panes  []pane
	layout splitLayout
	activePane int
	width  int
	height int
	msg    string

	promptOpen bool
	promptIn   []rune

	finderOpen  bool
	finderQ     []rune
	finderFiles []string
	finderHits  []string
	finderSel   int

	helpOpen bool

	treeVisible bool
	treeFocus   bool
	treeRows    []treeEntry
	treeSel     int
	treeOffset  int
	expanded    map[string]bool
}

var debugKeys = os.Getenv("DMED_DEBUG_KEYS") != ""

func New(paths ...string) Model {
	m := Model{width: 80, height: 24, expanded: map[string]bool{}}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if m.root == "" {
				m.root = normalizePath(".", p)
				m.msg = "project: " + filepath.Base(m.root)
			}
			continue
		}
		m.openPath(p)
	}
	if len(m.tabs) == 0 {
		m.tabs = append(m.tabs, tab{buf: buffer.New()})
	}
	m.initPanes()
	if m.root != "" {
		m.treeVisible = true
		m.rebuildTree()
	}
	return m
}

func (m Model) baseDir() string {
	if m.root != "" {
		return m.root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func (m Model) activeTab() *tab { return &m.tabs[m.activeTabIndex()] }

func (m *Model) cur() *tab { return &m.tabs[m.activeTabIndex()] }

func (m *Model) openPath(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
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
	// Only set active tab if panes are already initialized
	if len(m.panes) > 0 {
		m.setActiveTab(len(m.tabs) - 1)
	}
}

func (m *Model) switchTab(d int) {
	n := len(m.tabs)
	if n == 0 {
		return
	}
	idx := m.activeTabIndex()
	m.setActiveTab(((idx+d)%n + n) % n)
}

func (m *Model) jumpTab(n int) {
	m.setActiveTab(n)
}

func (m *Model) closeTab() tea.Cmd {
	idx := m.activeTabIndex()
	if len(m.tabs) == 1 {
		// Keep the tab so Bubbletea can render one final frame before Quit.
		return tea.Quit
	}
	m.tabs = append(m.tabs[:idx], m.tabs[idx+1:]...)
	m.fixPaneTabsAfterClose(idx)
	return nil
}

func (m *Model) startPrompt() {
	m.promptOpen = true
	m.promptIn = nil
}

func (m *Model) startFinder() {
	m.finderOpen = true
	m.finderQ = nil
	m.finderSel = 0
	m.finderFiles = collectFiles(m.baseDir())
	m.finderHits = searchFiles(m.finderFiles, "")
}

func (m *Model) refind() {
	m.finderHits = searchFiles(m.finderFiles, string(m.finderQ))
	if m.finderSel >= len(m.finderHits) {
		m.finderSel = len(m.finderHits) - 1
	}
	if m.finderSel < 0 {
		m.finderSel = 0
	}
}

func (m *Model) handleFinder(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.finderOpen = false
	case "enter":
		if n := len(m.finderHits); n > 0 {
			path := m.finderHits[m.finderSel]
			m.finderOpen = false
			m.focusOrOpen(path)
		} else {
			m.finderOpen = false
		}
	case "up":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel - 1 + n) % n
		}
	case "down":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel + 1) % n
		}
	case "backspace":
		if n := len(m.finderQ); n > 0 {
			m.finderQ = m.finderQ[:n-1]
			m.refind()
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.finderQ = append(m.finderQ, msg.Runes...)
			m.refind()
		}
	}
	return nil
}

func (m *Model) handleHelp(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "f1", "ctrl+e", "q":
		m.helpOpen = false
	}
	return nil
}

func (m *Model) focusOrOpen(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
	for i := range m.tabs {
		if m.tabs[i].path == path {
			m.setActiveTab(i)
			return
		}
	}
	m.openPath(path)
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
		if debugKeys {
			fmt.Fprintf(os.Stderr, "dmed: key %q\n", msg.String())
		}
		if !m.promptOpen && (msg.String() == "ctrl+c" || msg.String() == "ctrl+q") {
			return m, tea.Quit
		}
		cmd := m.handleKey(msg)
		if debugKeys {
			m.msg = "k:" + msg.String()
		}
		if cmd != nil {
			return m, cmd
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		if r := msg.Runes[0]; r < 32 && r != '\t' {
			msg = tea.KeyMsg{Type: tea.KeyType(r)}
		}
	}
	s := msg.String()
	if m.helpOpen {
		return m.handleHelp(msg)
	}
	if m.promptOpen {
		return m.handlePrompt(msg)
	}
	if m.finderOpen {
		return m.handleFinder(msg)
	}
	if m.treeFocus {
		return m.handleTree(msg)
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
	case "ctrl+o", "f3":
		m.startFinder()
	case "f1", "ctrl+e":
		m.helpOpen = !m.helpOpen
	case "ctrl+b", "f9":
		m.toggleTree()
	case "ctrl+\\", "f6":
		m.splitVert()
	case "ctrl+alt+h", "f7":
		m.splitHoriz()
	case "ctrl+alt+p", "f8":
		m.focusOtherPane()
	case "ctrl+alt+w":
		m.closePane()
	case "ctrl+w", "ctrl+x":
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
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() > 0; i++ {
			t.buf.MoveUp()
		}
	case "pgdown":
		t := m.cur()
		for i := 0; i < m.paneViewHeight(m.activePane)-2 && t.buf.CurLine() < t.buf.LineCount()-1; i++ {
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
	p := m.curPane()
	t := m.cur()
	h := m.paneViewHeight(m.activePane)
	cur := t.buf.CurLine()
	if h > 0 {
		if cur < p.offsetY {
			p.offsetY = cur
		}
		if cur >= p.offsetY+h {
			p.offsetY = cur - h + 1
		}
	}
	w := m.paneContentWidth(m.activePane)
	if w <= 0 {
		return
	}
	x := visCol(t.buf.LineAt(cur), t.buf.Col())
	if x < p.offsetX {
		p.offsetX = x
	}
	if x >= p.offsetX+w {
		p.offsetX = x - w + 1
	}
}
