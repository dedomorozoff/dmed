package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/ai"
	"dmed/internal/buffer"
	"dmed/internal/events"
	"dmed/internal/session"
	"dmed/internal/syntax"
	"dmed/internal/vcs"
	"dmed/internal/watcher"
)

type tab struct {
	buf          *buffer.Buffer
	path         string
	syntaxCached []syntax.HighlightedLine
	syntaxText   string
	diffCached   vcs.FileDiff
	diffText     string
}

func (t *tab) name(base string) string {
	if t.path == "" {
		return "[untitled]"
	}
	return shortenPath(base, t.path)
}

func (t *tab) getSyntaxLines() []syntax.HighlightedLine {
	text := t.buf.Text()
	if t.syntaxCached != nil && t.syntaxText == text {
		return t.syntaxCached
	}
	t.syntaxText = text
	t.syntaxCached = syntax.Default().HighlightBuffer(t.path, text)
	return t.syntaxCached
}

func (t *tab) getDiff(repo *vcs.Repo) vcs.FileDiff {
	if t.path == "" {
		return vcs.FileDiff{}
	}
	r := repo
	if r == nil || !strings.HasPrefix(t.path, r.Root) {
		if found, err := vcs.Open(filepath.Dir(t.path)); err == nil {
			r = found
		}
	}
	if r == nil {
		return vcs.FileDiff{}
	}
	text := t.buf.Text()
	if t.diffCached.Lines != nil && t.diffText == text {
		return t.diffCached
	}
	t.diffText = text
	t.diffCached = r.DiffBuffer(t.path, text)
	return t.diffCached
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
	promptSave bool
	promptSaveIn []rune

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

	// Search/replace
	searchOpen         bool
	searchQuery        []rune
	searchMatchIdx     int
	searchTotalMatches int
	replaceOpen        bool
	replaceWith        []rune
	replaceFocusFind   bool

	// Events, watcher, Git
	bus          *events.Bus
	watcher      *watcher.Watcher
	fileEvents   chan string
	repo         *vcs.Repo
	conflictOpen bool
	conflictPath string
	conflictRows       []vcs.DiffRow
	conflictLeftLines  []string
	conflictRightLines []string
	conflictOffY       int
	conflictOffX       int
	gitOpen      bool
	gitMode      gitPanelMode
	gitFiles     []vcs.FileStatus
	gitSel       int
	gitOffset    int
	gitCommitIn  []rune

	// Side-by-side diff view (opened from the Git panel)
	diffViewOpen   bool
	diffPath       string
	diffRows       []vcs.DiffRow
	diffHeadLines  []string
	diffRightLines []string
	diffOffsetY    int
	diffOffsetX    int

	// Bottom terminal panel
	termOpen    bool
	termLines   []string
	termIn      []rune
	termScroll  int // lines of scrollback from the bottom (0 = follow)
	termHist    []string
	termHistIdx int // 0 = live input, N = N commands back
	termCmd     *exec.Cmd
	termStdin   io.WriteCloser
	termCh      <-chan []string

	// Command palette & Clipboard
	paletteOpen bool
	paletteQ    []rune
	paletteSel  int
	clipboard   string

	// Right-side AI chat panel (local Ollama)
	chatOpen   bool
	chatFocus  bool
	chatIn     []rune
	chatMsgs   []ai.Message
	chatReply  string // assistant reply being streamed
	chatErr    string
	chatBusy   bool
	chatRows   []chatRow
	chatScroll int    // lines of scrollback from the bottom (0 = follow)
	chatModel  string // resolved Ollama model tag
	ai         *ai.Client
	chatCh     <-chan chatEvent
	chatCancel context.CancelFunc

	// Inline AI request (Ctrl+I)
	aiInlineOpen     bool
	aiInlineInput    []rune
	aiInlineOriginal string
	aiInlineSelStart [2]int
	aiInlineSelEnd   [2]int
	aiInlineProposal string
	aiInlineBusy     bool
	aiInlineCh       <-chan chatEvent
	aiInlineCancel   context.CancelFunc
	aiReviewMode     bool
	aiReviewRows     []vcs.DiffRow
	aiReviewLeft     []string
	aiReviewRight    []string
	aiReviewOffY     int
	aiReviewOffX     int

	quitConfirm bool
	pendingQuit bool
}

var debugKeys = os.Getenv("DMED_DEBUG_KEYS") != ""

// Russian ЙЦУКЕН → English QWERTY mapping for layout-independent keybindings.
var ruToEn = map[rune]rune{
	'й': 'q', 'ц': 'w', 'у': 'e', 'к': 'r', 'е': 't', 'н': 'y',
	'г': 'u', 'ш': 'i', 'щ': 'o', 'з': 'p', 'х': '[', 'ъ': ']',
	'ф': 'a', 'ы': 's', 'в': 'd', 'а': 'f', 'п': 'g', 'р': 'h',
	'о': 'j', 'л': 'k', 'д': 'l', 'ж': ';', 'э': '\'',
	'я': 'z', 'ч': 'x', 'с': 'c', 'м': 'v', 'и': 'b', 'т': 'n',
	'ь': 'm', 'б': ',', 'ю': '.',
	'ё': '`',
}

func normalizeKey(r rune) rune {
	if en, ok := ruToEn[r]; ok {
		return en
	}
	return r
}

func New(paths ...string) Model {
	fe := make(chan string, 16)
	m := Model{
		width:      80,
		height:     24,
		expanded:   map[string]bool{},
		fileEvents: fe,
		bus:        events.New(),
	}
	if w, err := watcher.New(func(p string) {
		select {
		case fe <- p:
		default:
		}
	}); err == nil {
		m.watcher = w
	}

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
	if len(paths) == 0 && m.root != "" {
		if sess, err := session.Load(session.DefaultPath(m.root)); err == nil && len(sess.Files) > 0 {
			for _, f := range sess.Files {
				m.openPath(f)
			}
		}
	}
	if len(m.tabs) == 0 {
		m.tabs = append(m.tabs, tab{buf: buffer.New()})
	}
	m.initPanes()
	if m.root != "" {
		m.treeVisible = true
		m.rebuildTree()
	}
	if repo, err := vcs.Open(m.baseDir()); err == nil {
		m.repo = repo
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

func (m *Model) cur() *tab {
	if len(m.tabs) == 0 {
		return nil
	}
	return &m.tabs[m.activeTabIndex()]
}

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
	if m.watcher != nil && path != "" {
		_ = m.watcher.Watch(path)
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
		return tea.Quit
	}
	if m.layout != splitNone {
		// Closing a tab in a split also collapses the split.
		other := 1 - m.activePane
		m.panes = []pane{m.panes[other]}
		m.activePane = 0
		m.layout = splitNone
	}
	m.tabs = append(m.tabs[:idx], m.tabs[idx+1:]...)
	m.fixPaneTabsAfterClose(idx)
	return nil
}

func (m *Model) startPrompt() {
	m.promptOpen = true
	m.promptIn = nil
}

func (m *Model) startSavePrompt() {
	m.promptSave = true
	m.promptSaveIn = nil
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

func (m *Model) handleSavePrompt(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.promptSave = false
		m.pendingQuit = false
	case "enter":
		raw := strings.TrimSpace(string(m.promptSaveIn))
		m.promptSave = false
		if raw != "" {
			path := normalizePath(m.baseDir(), raw)
			t := m.cur()
			t.path = path
			if m.watcher != nil {
				_ = m.watcher.Watch(path)
			}
			if err := os.WriteFile(t.path, []byte(t.buf.Text()), 0o644); err != nil {
				m.msg = "save failed: " + err.Error()
				return nil
			}
			t.buf.MarkSaved()
			m.msg = "saved"
			if m.pendingQuit {
				m.pendingQuit = false
				m.shutdown()
				return tea.Quit
			}
		}
	case "backspace":
		if n := len(m.promptSaveIn); n > 0 {
			m.promptSaveIn = m.promptSaveIn[:n-1]
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.promptSaveIn = append(m.promptSaveIn, msg.Runes...)
		}
	}
	return nil
}

func (m Model) hasDirty() bool {
	for _, t := range m.tabs {
		if t.buf.Dirty() {
			return true
		}
	}
	return false
}

func (m *Model) handleQuitConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.quitConfirm = false
		m.pendingQuit = false
	case "y", "Y":
		t := m.cur()
		if t.path == "" {
			m.pendingQuit = true
			m.quitConfirm = false
			m.startSavePrompt()
			return nil
		}
		m.saveActive()
		if t.buf.Dirty() {
			m.quitConfirm = false
			m.pendingQuit = false
			m.msg = "save failed, not quitting"
			return nil
		}
		m.quitConfirm = false
		m.pendingQuit = false
		m.shutdown()
		return tea.Quit
	case "n", "N":
		m.quitConfirm = false
		m.pendingQuit = false
		m.shutdown()
		return tea.Quit
	}
	return nil
}

type FileChangedMsg struct {
	Path string
}

func waitForFileEvent(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return FileChangedMsg{Path: p}
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForFileEvent(m.fileEvents), waitForTermOutput(m.termCh), waitForChatOutput(m.chatCh), waitForInlineOutput(m.aiInlineCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FileChangedMsg:
		path := msg.Path
		for i := range m.tabs {
			t := &m.tabs[i]
			if t.path == "" {
				continue
			}
			absT, _ := filepath.Abs(t.path)
			if absT == path || t.path == path {
				if !t.buf.Dirty() {
					if data, err := os.ReadFile(t.path); err == nil {
						t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
						t.syntaxCached = nil
						t.diffText = ""
						m.msg = "reloaded: " + t.name(m.baseDir())
					}
				} else {
					if data, err := os.ReadFile(t.path); err == nil {
						diskText := strings.ReplaceAll(string(data), "\r\n", "\n")
						bufText := t.buf.Text()
						m.conflictRows = vcs.SideBySide(bufText, diskText)
						m.conflictLeftLines = strings.Split(strings.TrimRight(bufText, "\n"), "\n")
						m.conflictRightLines = strings.Split(strings.TrimRight(diskText, "\n"), "\n")
						m.conflictOffY = 0
						m.conflictOffX = 0
					}
					m.conflictOpen = true
					m.conflictPath = path
					m.msg = "file modified externally: (r)eload or (i)gnore?"
				}
				break
			}
		}
		return m, waitForFileEvent(m.fileEvents)
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case TerminalOutputMsg:
		if len(msg.Lines) > 0 {
			m.termLines = append(m.termLines, msg.Lines...)
			maxBack := len(m.termLines) - 1
			if m.termScroll > maxBack {
				m.termScroll = maxBack
			}
			if m.termScroll < 0 {
				m.termScroll = 0
			}
		}
		return m, waitForTermOutput(m.termCh)
	case ChatOutputMsg:
		switch {
		case msg.Err != nil:
			m.chatBusy = false
			m.chatErr = msg.Err.Error()
		case msg.Done:
			m.chatBusy = false
			if m.chatReply != "" {
				m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "assistant", Content: m.chatReply})
				m.chatReply = ""
			}
		default:
			m.chatReply += msg.Delta
		}
		m.rebuildChatRows()
		if !msg.Done && msg.Err == nil {
			return m, waitForChatOutput(m.chatCh)
		}
		if m.chatCancel != nil {
			m.chatCancel()
			m.chatCancel = nil
		}
		return m, nil
	case InlineOutputMsg:
		cmd := m.handleInlineOutput(msg)
		return m, cmd
	case tea.KeyMsg:
		if debugKeys {
			fmt.Fprintf(os.Stderr, "dmed: key %q\n", msg.String())
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
	// Normalize Russian ЙЦУКЕН → English QWERTY for layout-independent keys.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		nr := make([]rune, len(msg.Runes))
		for i, r := range msg.Runes {
			nr[i] = normalizeKey(r)
		}
		msg = tea.KeyMsg{Type: msg.Type, Runes: nr, Alt: msg.Alt}
	}
	s := msg.String()
	// Quit/close keys must work in every mode (tree, git panel, prompts, search...).
	switch s {
	case "ctrl+q":
		return m.requestQuit()
	case "ctrl+c":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			m.msg = "copied to clipboard"
			return nil
		}
		return m.requestQuit()
	case "ctrl+w":
		return m.closeActiveTab()
	case "ctrl+x":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			m.cur().buf.DeleteSelection()
			m.msg = "cut to clipboard"
			return nil
		}
		return m.closeActiveTab()
	}
	if m.conflictOpen {
		switch s {
		case "r", "R":
			for i := range m.tabs {
				t := &m.tabs[i]
				absT, _ := filepath.Abs(t.path)
				if absT == m.conflictPath || t.path == m.conflictPath {
					if data, err := os.ReadFile(t.path); err == nil {
						t.buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
						t.syntaxCached = nil
						t.diffText = ""
						m.msg = "reloaded from disk"
					}
					break
				}
			}
			m.conflictOpen = false
			m.conflictRows = nil
			return nil
		case "i", "I", "esc":
			m.conflictOpen = false
			m.conflictRows = nil
			m.msg = "kept buffer changes"
			return nil
		case "up", "k":
			if m.conflictOffY > 0 {
				m.conflictOffY--
			}
		case "down", "j":
			if m.conflictOffY < len(m.conflictRows)-1 {
				m.conflictOffY++
			}
		case "pgup":
			m.conflictOffY -= m.paneViewHeight(m.activePane) / 2
			if m.conflictOffY < 0 {
				m.conflictOffY = 0
			}
		case "pgdn":
			m.conflictOffY += m.paneViewHeight(m.activePane) / 2
			maxOff := len(m.conflictRows) - 1
			if m.conflictOffY > maxOff {
				m.conflictOffY = maxOff
			}
		case "home", "g":
			m.conflictOffY = 0
		case "end", "G":
			m.conflictOffY = len(m.conflictRows) - 1
			if m.conflictOffY < 0 {
				m.conflictOffY = 0
			}
		case "left", "h":
			m.conflictOffX -= 8
			if m.conflictOffX < 0 {
				m.conflictOffX = 0
			}
		case "right", "l":
			m.conflictOffX += 8
		}
		return nil
	}
	if m.diffViewOpen {
		return m.handleDiffView(msg)
	}
	if m.termOpen {
		return m.handleTerm(msg)
	}
	if m.chatOpen {
		return m.handleChat(msg)
	}
	if m.gitOpen {
		return m.handleGit(msg)
	}
	if m.paletteOpen {
		return m.handlePalette(msg)
	}
	if m.helpOpen {
		return m.handleHelp(msg)
	}
	if m.promptOpen {
		return m.handlePrompt(msg)
	}
	if m.promptSave {
		return m.handleSavePrompt(msg)
	}
	if m.finderOpen {
		return m.handleFinder(msg)
	}
	if m.treeFocus {
		return m.handleTree(msg)
	}
	if m.searchOpen {
		if m.replaceOpen {
			return m.handleReplace(msg)
		}
		return m.handleSearch(msg)
	}
	if m.aiReviewMode {
		return m.handleInlineReview(msg)
	}
	if m.aiInlineOpen {
		return m.handleInlineRequest(msg)
	}
	if m.quitConfirm {
		return m.handleQuitConfirm(msg)
	}
	if len(s) == 5 && strings.HasPrefix(s, "alt+") && s[4] >= '1' && s[4] <= '9' {
		m.jumpTab(int(s[4] - '1'))
		return nil
	}
	switch s {
	case "ctrl+s":
		t := m.cur()
		if t.path == "" {
			m.startSavePrompt()
		} else {
			m.saveActive()
		}
	case "ctrl+z":
		if m.cur().buf.Undo() {
			m.msg = ""
		}
	case "ctrl+y":
		m.cur().buf.DeleteLine()
		m.msg = ""
	case "ctrl+r":
		if m.cur().buf.Redo() {
			m.msg = ""
		}
	case "ctrl+t":
		m.startPrompt()
	case "alt+t":
		if cmd := m.toggleTerminal(); cmd != nil {
			return cmd
		}
	case "alt+a":
		m.toggleChat()
	case "alt+i":
		m.startInlineRequest()
	case "ctrl+o":
		m.startFinder()
	case "ctrl+p", "ctrl+shift+p", "f2":
		m.startPalette()
	case "f3":
		m.updateSearchMatches(true)
	case "shift+f3":
		m.findPrev()
	case "ctrl+f":
		m.startSearch()
	case "ctrl+h":
		m.startReplace()
	case "ctrl+g":
		if m.gitOpen {
			m.gitOpen = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
	case "alt+[":
		m.jumpHunk(-1)
	case "alt+]":
		m.jumpHunk(1)
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
	case "ctrl+v":
		if m.clipboard != "" {
			m.cur().buf.InsertText(m.clipboard)
			m.msg = ""
		}
	case "alt+left":
		m.switchTab(-1)
	case "alt+right":
		m.switchTab(1)
	case "shift+up":
		m.cur().buf.MoveUpWithSelect()
	case "shift+down":
		m.cur().buf.MoveDownWithSelect()
	case "shift+left":
		m.cur().buf.MoveLeftWithSelect()
	case "shift+right":
		m.cur().buf.MoveRightWithSelect()
	case "shift+home":
		m.cur().buf.LineStartWithSelect()
	case "shift+end":
		m.cur().buf.LineEndWithSelect()
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

func (m *Model) requestQuit() tea.Cmd {
	if m.hasDirty() {
		m.quitConfirm = true
		return nil
	}
	m.shutdown()
	return tea.Quit
}

// shutdown stops background processes and persists the session.
func (m *Model) shutdown() {
	m.cancelChat()
	m.killTerminal()
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
	m.saveSession()
}

// closeActiveTab closes the tab of the active pane; closing the last tab
// quits (with a save confirmation when the buffer is dirty).
func (m *Model) closeActiveTab() tea.Cmd {
	cmd := m.closeTab()
	if cmd == nil {
		return nil
	}
	if m.hasDirty() {
		m.quitConfirm = true
		m.pendingQuit = true
		return nil
	}
	m.shutdown()
	return cmd
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
	if len(m.tabs) == 0 {
		return
	}
	p := m.curPane()
	t := m.cur()
	if t == nil {
		return
	}
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

func (m *Model) startSearch() {
	m.searchOpen = true
	m.replaceOpen = false
	m.replaceFocusFind = true
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) startReplace() {
	m.searchOpen = true
	m.replaceOpen = true
	m.replaceFocusFind = false
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) handleSearch(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "enter", "f3", "down", "ctrl+n":
		if len(m.searchQuery) > 0 {
			m.updateSearchMatches(true)
		}
	case "shift+f3", "up", "ctrl+p":
		if len(m.searchQuery) > 0 {
			m.findPrev()
		}
	case "ctrl+h":
		m.startReplace()
	case "backspace":
		if n := len(m.searchQuery); n > 0 {
			m.searchQuery = m.searchQuery[:n-1]
			m.updateSearchMatches(false)
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.searchQuery = append(m.searchQuery, msg.Runes...)
			m.updateSearchMatches(false)
		}
	}
	return nil
}

func (m *Model) handleReplace(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "tab":
		m.replaceFocusFind = !m.replaceFocusFind
	case "ctrl+a":
		m.doReplaceAll()
	case "enter":
		if m.replaceFocusFind {
			m.updateSearchMatches(true)
		} else {
			m.doReplace()
		}
	case "f3", "down", "ctrl+n":
		m.updateSearchMatches(true)
	case "shift+f3", "up", "ctrl+p":
		m.findPrev()
	case "backspace":
		if m.replaceFocusFind {
			if n := len(m.searchQuery); n > 0 {
				m.searchQuery = m.searchQuery[:n-1]
				m.updateSearchMatches(false)
			}
		} else {
			if n := len(m.replaceWith); n > 0 {
				m.replaceWith = m.replaceWith[:n-1]
			}
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if m.replaceFocusFind {
				m.searchQuery = append(m.searchQuery, msg.Runes...)
				m.updateSearchMatches(false)
			} else {
				m.replaceWith = append(m.replaceWith, msg.Runes...)
			}
		}
	}
	return nil
}

type searchMatch struct {
	line int
	col  int
}

func findMatchesInRunes(line []rune, query []rune) []int {
	if len(query) == 0 || len(line) < len(query) {
		return nil
	}
	var cols []int
	for i := 0; i <= len(line)-len(query); i++ {
		match := true
		for j := 0; j < len(query); j++ {
			if line[i+j] != query[j] {
				match = false
				break
			}
		}
		if match {
			cols = append(cols, i)
			i += len(query) - 1
		}
	}
	return cols
}

func (m *Model) allMatches() []searchMatch {
	if len(m.searchQuery) == 0 {
		return nil
	}
	t := m.cur()
	var matches []searchMatch
	for ln := 0; ln < t.buf.LineCount(); ln++ {
		cols := findMatchesInRunes(t.buf.LineAt(ln), m.searchQuery)
		for _, col := range cols {
			matches = append(matches, searchMatch{line: ln, col: col})
		}
	}
	return matches
}

func (m *Model) updateSearchMatches(jumpToNext bool) {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	t := m.cur()
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()

	foundIdx := 0
	for i, mPos := range matches {
		if mPos.line > curLine || (mPos.line == curLine && mPos.col >= curCol) {
			foundIdx = i
			break
		}
	}
	if jumpToNext && m.searchMatchIdx >= 0 {
		foundIdx = (m.searchMatchIdx + 1) % len(matches)
	}
	m.searchMatchIdx = foundIdx
	target := matches[foundIdx]
	t.buf.SetCursor(target.line, target.col)
}

func (m *Model) findPrev() {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	if m.searchMatchIdx <= 0 {
		m.searchMatchIdx = len(matches) - 1
	} else {
		m.searchMatchIdx--
	}
	target := matches[m.searchMatchIdx]
	m.cur().buf.SetCursor(target.line, target.col)
}

func (m *Model) doReplace() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	matches := m.allMatches()
	if len(matches) == 0 {
		return
	}
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()
	qLen := len(m.searchQuery)

	onMatch := false
	for _, mPos := range matches {
		if mPos.line == curLine && mPos.col == curCol {
			onMatch = true
			break
		}
	}
	if !onMatch {
		m.updateSearchMatches(false)
		return
	}

	t.buf.ReplaceRange(curLine, curCol, qLen, m.replaceWith)
	m.msg = "replaced 1 occurrence"
	m.updateSearchMatches(false)
}

func (m *Model) doReplaceAll() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	count := t.buf.ReplaceAll(string(m.searchQuery), string(m.replaceWith))
	m.msg = fmt.Sprintf("replaced %d occurrence(s)", count)
	m.searchOpen = false
	m.replaceOpen = false
}

func (m *Model) jumpHunk(dir int) {
	t := m.cur()
	if t.path == "" {
		return
	}
	diff := t.getDiff(m.repo)
	if len(diff.Hunks) == 0 {
		m.msg = "no git changes"
		return
	}
	curLine := t.buf.CurLine()
	if dir > 0 {
		for _, h := range diff.Hunks {
			if h.StartLine > curLine {
				t.buf.SetCursor(h.StartLine, 0)
				m.msg = fmt.Sprintf("git hunk: lines %d-%d", h.StartLine+1, h.EndLine+1)
				return
			}
		}
		t.buf.SetCursor(diff.Hunks[0].StartLine, 0)
		m.msg = fmt.Sprintf("git hunk: lines %d-%d", diff.Hunks[0].StartLine+1, diff.Hunks[0].EndLine+1)
	} else {
		for i := len(diff.Hunks) - 1; i >= 0; i-- {
			h := diff.Hunks[i]
			if h.StartLine < curLine {
				t.buf.SetCursor(h.StartLine, 0)
				m.msg = fmt.Sprintf("git hunk: lines %d-%d", h.StartLine+1, h.EndLine+1)
				return
			}
		}
		last := diff.Hunks[len(diff.Hunks)-1]
		t.buf.SetCursor(last.StartLine, 0)
		m.msg = fmt.Sprintf("git hunk: lines %d-%d", last.StartLine+1, last.EndLine+1)
	}
}

func (m *Model) startPalette() {
	m.paletteOpen = true
	m.paletteQ = nil
	m.paletteSel = 0
}

func (m *Model) saveSession() {
	var files []string
	for _, t := range m.tabs {
		if t.path != "" {
			files = append(files, t.path)
		}
	}
	if len(files) == 0 {
		return
	}
	sess := session.SessionState{
		Root:       m.root,
		Files:      files,
		ActiveTab:  m.activeTabIndex(),
		Layout:     int(m.layout),
		ActivePane: m.activePane,
	}
	_ = session.Save(session.DefaultPath(m.root), sess)
}
