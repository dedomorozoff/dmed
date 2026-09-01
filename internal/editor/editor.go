package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/buffer"
	"dmed/internal/config"
	"dmed/internal/events"
	"dmed/internal/i18n"
	"dmed/internal/lsp"
	"dmed/internal/plugin"
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
	lineEnding   string // "lf" or "crlf"
	encoding     string // "utf-8", "utf-16le", "utf-16be", "latin-1"
}

func (t *tab) name(base string) string {
	if t.path == "" {
		return "[untitled]"
	}
	return shortenPath(base, t.path)
}

// detectFileInfo analyzes raw bytes to determine line endings and encoding.
func detectFileInfo(data []byte) (lineEnding, encoding string) {
	lineEnding = "lf"
	encoding = "utf-8"

	// Detect BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		encoding = "utf-8"
		data = data[3:]
	} else if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		encoding = "utf-16le"
		data = data[2:]
	} else if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		encoding = "utf-16be"
		data = data[2:]
	}

	// Detect CRLF
	for _, b := range data {
		if b == '\r' {
			lineEnding = "crlf"
			break
		}
	}

	// Check for non-ASCII bytes that aren't valid UTF-8
	if encoding == "utf-8" {
		for i := 0; i < len(data); {
			b := data[i]
			if b < 0x80 {
				i++
			} else if b < 0xC0 {
				encoding = "latin-1"
				break
			} else if b < 0xE0 {
				if i+1 >= len(data) || data[i+1]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 2
			} else if b < 0xF0 {
				if i+2 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 3
			} else if b < 0xF8 {
				if i+3 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 || data[i+3]&0xC0 != 0x80 {
					encoding = "latin-1"
					break
				}
				i += 4
			} else {
				encoding = "latin-1"
				break
			}
		}
	}

	return
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
	root       string
	cfg        config.Config
	tr         i18n.Translator
	plugins    *plugin.Manager
	lspClient  *lsp.Client
	diagCh     chan lspDiagMsg
	diags      map[string][]lsp.Diagnostic
	tabs       []tab
	panes      []pane
	layout     splitLayout
	activePane int
	width      int
	height     int
	msg        string

	promptOpen      bool
	promptIn        []rune
	promptSave      bool
	promptSaveIn    []rune
	promptNewFile   bool
	promptNewFolder bool

	finderOpen  bool
	finderQ     []rune
	finderFiles []string
	finderHits  []string
	finderSel   int

	helpOpen bool

	aiCfgOpen  bool
	aiCfgField int
	aiCfgEdit  bool
	aiCfgIn    []rune

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
	bus                *events.Bus
	watcher            *watcher.Watcher
	fileEvents         chan string
	repo               *vcs.Repo
	conflictOpen       bool
	conflictPath       string
	conflictRows       []vcs.DiffRow
	conflictLeftLines  []string
	conflictRightLines []string
	conflictOffY       int
	conflictOffX       int
	gitOpen            bool
	gitMode            gitPanelMode
	gitFiles           []vcs.FileStatus
	gitSel             int
	gitOffset          int
	gitCommitIn        []rune
	gitDiffFocused     bool // true when diff preview area has focus (scrollable)

	// Git log
	gitLogEntries []vcs.LogEntry
	gitLogSel     int
	gitLogOffset  int

	// Git branch management
	gitBranchIn     []rune
	gitBranchList   []string
	gitBranchSel    int
	gitBranchOffset int
	gitBranchNew    bool // true when creating a new branch, false when switching

	// Side-by-side diff view (opened from the Git panel)
	diffViewOpen    bool
	diffPath        string
	diffRows        []vcs.DiffRow
	diffHeadLines   []string
	diffRightLines  []string
	diffHeadSyntax  []syntax.HighlightedLine
	diffRightSyntax []syntax.HighlightedLine
	diffOffsetY     int
	diffOffsetX     int

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
	paletteOpen   bool
	paletteQ      []rune
	paletteSel    int
	paletteOffset int
	clipboard     string

	// Language chooser
	langChooserOpen bool
	langChooserSel  int

	// Autocompletion popup
	complOpen   bool
	complItems  []string
	complSel    int
	complOffset int
	complLine   int
	complStart  int

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
	ai         ai.Provider
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
	quitTab     bool // true if confirming close of a single tab (not quit)
	pendingQuit bool

	// Agent background tasks (M4)
	agentQueue    *agent.Queue
	agentRunner   *agent.Runner
	agentApplier  *agent.Applier
	agentCommit   *agent.Committer
	agentCtx      context.Context
	agentCancel   context.CancelFunc
	agentCh       chan struct{} // repaint signal from the agent worker
	agentOpen     bool
	agentPrompt   bool // entering a new task prompt
	agentPromptIn []rune
	agentSel      int
	agentOffset   int

	// Agent diff review (T6): a task's proposed changes shown side-by-side.
	agentReviewMode   bool
	agentReviewTaskID string
	agentReviewChange int // index into the task's Changes
	agentReviewRows   []vcs.DiffRow
	agentReviewLeft   []string
	agentReviewRight  []string
	agentReviewOffY   int
	agentReviewOffX   int

	// Mouse state
	mouseDown bool
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
		diagCh:     make(chan lspDiagMsg, 64),
		diags:      map[string][]lsp.Diagnostic{},
	}
	if w, err := watcher.New(func(p string) {
		select {
		case fe <- p:
		default:
		}
	}); err == nil {
		m.watcher = w
	}

	var files []string
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if m.root == "" {
				m.root = normalizePath(".", p)
				m.msg = m.t("msg.project", filepath.Base(m.root))
			}
			continue
		}
		files = append(files, p)
	}

	// Restore the previous session only when the user did not open specific
	// files and a project root is known — an explicit file list takes
	// precedence over the session.
	restoreActiveTab := -1
	if len(files) == 0 && m.root != "" {
		if sess, err := session.Load(session.DefaultPath(m.root)); err == nil && len(sess.Files) > 0 {
			for _, f := range sess.Files {
				m.openPath(f)
			}
			m.restoreCursors(sess.Cursors)
			restoreActiveTab = sess.ActiveTab
		}
	} else {
		for _, p := range files {
			m.openPath(p)
		}
	}
	if len(m.tabs) == 0 {
		m.tabs = append(m.tabs, tab{buf: buffer.New()})
	}
	m.cfg = config.Load(m.root)
	m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
	syntax.SetDefault(m.cfg.Editor.SyntaxTheme)
	m.initPanes()
	if restoreActiveTab >= 0 && restoreActiveTab < len(m.tabs) {
		m.setActiveTab(restoreActiveTab)
	}
	if m.root != "" {
		m.treeVisible = true
		m.rebuildTree()
	}
	if repo, err := vcs.Open(m.baseDir()); err == nil {
		m.repo = repo
	}
	m.loadPlugins()
	m.plugins.Emit(&m, "ready")
	// Watch root directory for tree updates
	if m.root != "" && m.watcher != nil {
		m.watcher.Watch(m.root)
	}
	// Watch config files for hot-reload
	if p := config.ConfigPath(); m.watcher != nil {
		m.watcher.Watch(p)
	}
	if p := config.ProjectConfigPath(m.root); p != "" && m.watcher != nil {
		m.watcher.Watch(p)
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
	t := tab{path: path, buf: buffer.New(), lineEnding: "lf", encoding: "utf-8"}
	if err != nil {
		if os.IsNotExist(err) {
			m.msg = m.t("msg.new_file", path)
		} else {
			m.msg = m.t("msg.open_failed", err.Error())
		}
	} else {
		le, enc := detectFileInfo(data)
		t.lineEnding = le
		t.encoding = enc
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
	if m.plugins != nil {
		m.plugins.Emit(m, "file_open")
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

func (m *Model) startNewFilePrompt() {
	m.promptOpen = true
	m.promptNewFile = true
	m.promptIn = nil
}

func (m *Model) startNewFolderPrompt() {
	m.promptOpen = true
	m.promptNewFolder = true
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
	m.finderFiles = collectFiles(m.baseDir(), m.cfg.Editor.SkippedDirs)
	m.finderHits = searchFiles(m.finderFiles, "")
}

// openConfigFile opens the .dmed.conf file in a new tab for editing.
// Creates the file with defaults if it doesn't exist.
func (m *Model) openConfigFile() {
	path := config.ConfigPath()
	if m.root != "" {
		path = config.ProjectConfigPath(m.root)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create with defaults
		content := "# dmed configuration\n" +
			"# Uncomment settings to override defaults.\n\n" +
			"[editor]\n" +
			"# tab_width = 4\n" +
			"# syntax_theme = monokai\n" +
			"# line_numbers = true\n" +
			"# skipped_dirs = .git,node_modules\n\n" +
			"[ai]\n" +
			"# provider = ollama  # ollama | openai\n" +
			"# model =\n" +
			"# ollama_url = http://localhost:11434\n" +
			"# api_key =  # for OpenAI-compatible providers\n" +
			"# context_max = 6000\n\n" +
			"[agent]\n" +
			"# system_prompt =  # optional override for background agent tasks\n" +
			"# context_max = 262144  # bytes of file context sent to the agent\n\n" +
			"[ui]\n" +
			"# tree_width = 25\n" +
			"# chat_width_pct = 40\n"
		os.WriteFile(path, []byte(content), 0o644)
	}
	m.openPath(path)
	m.msg = m.t("msg.edit_config")
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

func (m *Model) handleFinder(msg tea.KeyPressMsg) tea.Cmd {
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
		if len(msg.Text) > 0 {
			m.finderQ = append(m.finderQ, []rune(msg.Text)...)
			m.refind()
		}
	}
	return nil
}

func (m *Model) handleHelp(msg tea.KeyPressMsg) tea.Cmd {
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

func (m *Model) handlePrompt(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
	case "enter":
		path := strings.TrimSpace(string(m.promptIn))
		newFolder := m.promptNewFolder
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
		if path != "" {
			if newFolder {
				_ = os.MkdirAll(path, 0o755)
				m.msg = m.t("msg.created_folder", path)
			} else {
				m.openPath(path)
			}
		}
	case "backspace":
		if n := len(m.promptIn); n > 0 {
			m.promptIn = m.promptIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.promptIn = append(m.promptIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) handleSavePrompt(msg tea.KeyPressMsg) tea.Cmd {
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
			text := t.buf.Text()
			if t.lineEnding == "crlf" {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
				m.msg = m.t("msg.save_failed", err.Error())
				return nil
			}
			t.buf.MarkSaved()
			m.msg = m.t("msg.saved")
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
		if len(msg.Text) > 0 {
			m.promptSaveIn = append(m.promptSaveIn, []rune(msg.Text)...)
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

func (m *Model) handleQuitConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
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
			m.quitTab = false
			m.msg = m.t("msg.save_failed_gen")
			if m.quitTab {
				m.quitTab = false
			}
			return nil
		}
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Just close the tab after saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
		m.shutdown()
		return tea.Quit
	case "n", "N":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Close the tab without saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
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
	return tea.Batch(waitForFileEvent(m.fileEvents), waitForTermOutput(m.termCh), waitForChatOutput(m.chatCh), waitForInlineOutput(m.aiInlineCh), waitForLSPDiag(m.diagCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FileChangedMsg:
		path := msg.Path
		// Hot-reload config when .dmed.conf changes
		if strings.HasSuffix(path, ".dmed.conf") {
			m.cfg = config.Load(m.root)
			m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
			syntax.SetDefault(m.cfg.Editor.SyntaxTheme)
			m.msg = m.t("msg.config_reloaded")
			return m, waitForFileEvent(m.fileEvents)
		}
		// Hot-reload a changed plugin without restarting the editor.
		if m.plugins != nil && m.isPluginPath(path) {
			m.reloadPlugin(path)
			return m, waitForFileEvent(m.fileEvents)
		}
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
						m.msg = m.t("msg.reloaded_name", t.name(m.baseDir()))
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
					m.msg = m.t("msg.external")
				}
				break
			}
		}
		// Refresh Git panel if open to show updated file status
		if m.gitOpen {
			m.refreshGitFiles()
		}
		// Rebuild tree if the change is in the project root
		if m.treeVisible && m.root != "" {
			absRoot, _ := filepath.Abs(m.root)
			absPath, _ := filepath.Abs(path)
			if strings.HasPrefix(absPath, absRoot) {
				m.rebuildTree()
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
	case AgentRefreshMsg:
		return m, waitForAgentRefresh(m.agentCh)
	case tea.MouseClickMsg:
		cmd := m.handleMouseClick(msg)
		return m, cmd
	case tea.MouseWheelMsg:
		cmd := m.handleMouseWheel(msg)
		return m, cmd
	case tea.MouseReleaseMsg:
		m.mouseDown = false
	case tea.MouseMotionMsg:
		if m.mouseDown {
			cmd := m.handleMouseMotion(msg)
			return m, cmd
		}
	case tea.PasteMsg:
		text := msg.String()
		if text != "" {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(text)
			} else {
				m.cur().buf.InsertText(text)
			}
			m.msg = m.t("msg.pasted")
		}
	case tea.KeyPressMsg:
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
	case lspCompletionMsg:
		if m.complOpen && msg.path == m.cur().path {
			m.mergeLSPCompletion(msg.items)
		}
	case lspDiagMsg:
		abs, _ := filepath.Abs(msg.path)
		m.diags[abs] = msg.diags
		return m, waitForLSPDiag(m.diagCh)
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	// Some terminal stacks send bare control bytes (Ctrl+O as 0x0f, Ctrl+C as
	// 0x03, bare Ctrl as NUL). Normalize them into proper ctrl+key messages so
	// keybindings work and stray control bytes never reach the buffer.
	if len(msg.Text) == 1 {
		if r := rune(msg.Text[0]); r < 32 && r != '\t' {
			msg = tea.KeyPressMsg{Code: r + 96, Mod: tea.ModCtrl}
		}
	}
	// Normalize Russian ЙЦУКЕН → English QWERTY for layout-independent keys.
	// Use []rune so multi-byte UTF-8 (Cyrillic) inputs don't leave trailing NULs.
	if src := []rune(msg.Text); len(src) > 0 {
		nr := make([]rune, len(src))
		for i, r := range src {
			nr[i] = normalizeKey(r)
		}
		msg = tea.KeyPressMsg{Code: nr[0], Text: string(nr), Mod: msg.Mod}
	}
	s := msg.String()

	// While the completion popup is open, navigation keys control it.
	if m.complOpen && m.handleCompletionKey(s) {
		return nil
	}

	switch s {
	case "ctrl+q":
		return m.requestQuit()
	case "ctrl+c":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			clipboard.WriteAll(m.clipboard)
			m.msg = m.t("msg.copied")
			return nil
		}
		return m.requestQuit()
	case "ctrl+w":
		return m.closeActiveTab()
	case "ctrl+x":
		if m.cur().buf.HasSelection() {
			m.clipboard = m.cur().buf.SelectedText()
			clipboard.WriteAll(m.clipboard)
			m.cur().buf.DeleteSelection()
			m.msg = m.t("msg.cut")
			return nil
		}
		return m.closeActiveTab()
	case "ctrl+p", "ctrl+shift+p", "f2":
		m.startPalette()
		return nil
	case "ctrl+t":
		m.startPrompt()
		return nil
	case "alt+t":
		if cmd := m.toggleTerminal(); cmd != nil {
			return cmd
		}
		return nil
	case "alt+a":
		m.toggleChat()
		return nil
	case "alt+l":
		return m.openAgentPanel()
	case "alt+i":
		m.startInlineRequest()
		return nil
	case "ctrl+o":
		m.startFinder()
		return nil
	case "f3":
		m.updateSearchMatches(true)
		return nil
	case "shift+f3":
		m.findPrev()
		return nil
	case "ctrl+f":
		m.startSearch()
		return nil
	case "ctrl+h":
		m.startReplace()
		return nil
	case "ctrl+g":
		if m.gitOpen {
			m.gitOpen = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
		return nil
	case "f1", "ctrl+e":
		m.helpOpen = !m.helpOpen
		return nil
	case "ctrl+b", "f9":
		m.toggleTree()
		return nil
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
						m.msg = m.t("msg.reloaded")
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
			m.msg = m.t("msg.kept")
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
	if m.agentReviewMode {
		return m.handleAgentReview(msg)
	}
	if m.agentPrompt {
		return m.handleAgentPrompt(msg)
	}
	if m.agentOpen {
		return m.handleAgent(msg)
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
	if m.aiCfgOpen {
		return m.handleAISettings(msg)
	}
	if m.helpOpen {
		return m.handleHelp(msg)
	}
	if m.langChooserOpen {
		return m.handleLangChooser(msg)
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
	// Plugins get first crack at unhandled keys so they can override built-ins.
	if m.plugins != nil && m.plugins.HasBinding(s) {
		if m.plugins.RunBinding(m, s) {
			return nil
		}
	}
	switch s {
	case "ctrl+space":
		return m.triggerCompletion(true)
	case "alt+d":
		b := m.cur().buf
		if !b.AddNextOccurrence() {
			m.msg = m.t("msg.no_more_occurrences")
		} else if !b.HasMultipleCursors() && b.HasSelection() {
			m.msg = m.t("msg.selected")
		} else {
			m.msg = ""
		}
	case "esc":
		m.cur().buf.ClearCursors()
		m.msg = ""
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
	case "ctrl+d":
		m.cur().buf.DuplicateLine()
		m.msg = ""
	case "ctrl+r":
		if m.cur().buf.Redo() {
			m.msg = ""
		}
	case "alt+[":
		m.jumpHunk(-1)
	case "alt+]":
		m.jumpHunk(1)
	case "ctrl+\\", "f6":
		m.splitVert()
	case "ctrl+alt+h", "f7":
		m.splitHoriz()
	case "ctrl+alt+p", "f8":
		m.focusOtherPane()
	case "ctrl+alt+w":
		m.closePane()
	case "ctrl+v":
		if sysClip, err := clipboard.ReadAll(); err == nil && sysClip != "" {
			m.clipboard = sysClip
		}
		if m.clipboard != "" {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(m.clipboard)
			} else {
				m.cur().buf.InsertText(m.clipboard)
			}
			m.msg = ""
		}
		return nil
	case "alt+left":
		m.switchTab(-1)
	case "alt+right":
		m.switchTab(1)
	case "alt+up":
		m.cur().buf.MoveLineUp()
	case "alt+down":
		m.cur().buf.MoveLineDown()
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
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllUp()
		} else {
			m.cur().buf.MoveUp()
		}
	case "down":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllDown()
		} else {
			m.cur().buf.MoveDown()
		}
	case "left":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllLeft()
		} else {
			m.cur().buf.MoveLeft()
		}
	case "right":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MoveAllRight()
		} else {
			m.cur().buf.MoveRight()
		}
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
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiNewline()
		} else {
			m.cur().buf.InsertNewline()
		}
		m.msg = ""
	case "backspace":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiBackspace()
		} else {
			m.cur().buf.Backspace()
		}
		m.msg = ""
		return m.triggerCompletion(false)
	case "delete":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiDelete()
		} else {
			m.cur().buf.Delete()
		}
		m.msg = ""
	case "tab":
		if m.cur().buf.HasMultipleCursors() {
			m.cur().buf.MultiInsertRune('\t')
		} else {
			m.cur().buf.Insert('\t')
		}
		m.msg = ""
	default:
		if len(msg.Text) > 0 {
			if m.cur().buf.HasMultipleCursors() {
				m.cur().buf.MultiInsertText(msg.Text)
			} else {
				for _, r := range msg.Text {
					m.cur().buf.Insert(r)
				}
			}
			m.msg = ""
			return m.triggerCompletion(false)
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
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
	if m.lspClient != nil {
		m.lspClient.Close()
	}
	m.saveSession()
}

// closeActiveTab closes the tab of the active pane; closing the last tab
// quits (with a save confirmation when the buffer is dirty).
func (m *Model) closeActiveTab() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}

	// If this is the last tab, quit (with save prompt if dirty)
	if len(m.tabs) == 1 {
		if t.buf.Dirty() {
			m.quitConfirm = true
			m.pendingQuit = true
			m.quitTab = false // This is a quit, not a tab close
			return nil
		}
		m.shutdown()
		return tea.Quit
	}

	// Multiple tabs: check if the tab being closed is dirty
	if t.buf.Dirty() {
		m.quitTab = true
		m.pendingQuit = true
		m.quitConfirm = true
		return nil
	}

	// Tab is clean, just close it
	cmd := m.closeTab()
	if cmd == nil {
		return nil
	}
	m.shutdown()
	return cmd
}

func (m *Model) saveActive() {
	t := m.cur()
	if t.path == "" {
		m.msg = m.t("msg.cannot_save")
		return
	}
	text := t.buf.Text()
	if t.lineEnding == "crlf" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
		m.msg = m.t("msg.save_failed", err.Error())
		return
	}
	t.buf.MarkSaved()
	m.msg = m.t("msg.saved")
	if m.plugins != nil {
		m.plugins.Emit(m, "save")
	}
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
	x := visCol(t.buf.LineAt(cur), t.buf.Col(), m.cfg.Editor.TabWidth)
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

func (m *Model) handleSearch(msg tea.KeyPressMsg) tea.Cmd {
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
		if len(msg.Text) > 0 {
			m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
			m.updateSearchMatches(false)
		}
	}
	return nil
}

func (m *Model) handleReplace(msg tea.KeyPressMsg) tea.Cmd {
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
		if len(msg.Text) > 0 {
			if m.replaceFocusFind {
				m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
				m.updateSearchMatches(false)
			} else {
				m.replaceWith = append(m.replaceWith, []rune(msg.Text)...)
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
	m.msg = m.t("msg.replaced_one")
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
		m.msg = m.t("msg.no_git_changes")
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
	m.paletteOffset = 0
}

// setLang switches the interface language, rebuilding the translator and
// persisting the choice to the config file (project if available, else global).
func (m *Model) setLang(lang string) {
	m.cfg.UI.Lang = lang
	m.tr = i18n.New(i18n.Resolve(lang))
	path := config.ProjectConfigPath(m.root)
	if path == "" {
		path = config.ConfigPath()
	}
	_ = config.WriteLang(path, lang)
	m.msg = m.t("msg.lang_set", lang)
}

// openLangChooser opens the language selection list.
func (m *Model) openLangChooser() {
	m.langChooserOpen = true
	m.langChooserSel = 0
	m.paletteOpen = false
}

// handleLangChooser handles keys while the language chooser is open.
func (m *Model) handleLangChooser(msg tea.KeyPressMsg) tea.Cmd {
	langs := i18n.Supported()
	switch msg.String() {
	case "esc":
		m.langChooserOpen = false
	case "enter":
		if m.langChooserSel >= 0 && m.langChooserSel < len(langs) {
			m.setLang(langs[m.langChooserSel].Code)
		}
		m.langChooserOpen = false
	case "up", "k":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel - 1 + len(langs)) % len(langs)
		}
	case "down", "j":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel + 1) % len(langs)
		}
	}
	return nil
}

// restoreCursors applies saved per-file cursor positions to the just-opened
// tabs. Positions are clamped by SetCursor, so stale values are harmless.
func (m *Model) restoreCursors(cursors map[string]session.CursorPos) {
	for _, t := range m.tabs {
		if c, ok := cursors[t.path]; ok {
			t.buf.SetCursor(c.Line, c.Col)
		}
	}
}

func (m *Model) saveSession() {
	var files []string
	cursors := map[string]session.CursorPos{}
	for _, t := range m.tabs {
		if t.path != "" {
			files = append(files, t.path)
			cursors[t.path] = session.CursorPos{Line: t.buf.CurLine(), Col: t.buf.Col()}
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
		Cursors:    cursors,
	}
	_ = session.Save(session.DefaultPath(m.root), sess)
}

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	y := msg.Y
	x := msg.X

	// Ignore clicks on the tab bar (row 0), status bar (row viewHeight+1),
	// finder/palette/terminal panels.  Only handle the editor area.
	h := m.viewHeight()
	if y < 1 || y > h {
		return nil
	}

	// When git panel is open with inline diff, clicks in the diff area
	// toggle focus to the diff panel.
	if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		leftW := m.leftRailWidth()
		if x >= leftW {
			m.gitDiffFocused = true
			return nil
		}
		// Click on the file list — unfocus diff, don't try to set buffer cursor
		m.gitDiffFocused = false
		return nil
	}

	// Map y to a buffer line via the active pane's scroll offset.
	editorRow := y - 1
	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	ln := editorRow + p.offsetY

	// Clamp line before accessing buffer.
	if ln >= t.buf.LineCount() {
		ln = t.buf.LineCount() - 1
	}
	if ln < 0 {
		ln = 0
	}

	// Map x to a column, accounting for the left rail, gutter, and scroll.
	leftW := m.leftRailWidth()
	gw := m.gutterWidthForTab(t)

	clickX := x - leftW - gw + p.offsetX
	if clickX < 0 {
		clickX = 0
	}

	// Convert expanded column back to raw column (accounting for tabs).
	rawCol := expandedToRawCol(t.buf.LineAt(ln), clickX, m.cfg.Editor.TabWidth)

	lineLen := t.buf.LineLen(ln)
	if rawCol > lineLen {
		rawCol = lineLen
	}

	// Alt+Click adds a secondary cursor instead of moving the main one.
	if msg.Mod&tea.ModAlt != 0 {
		if t.buf.AddCursor(ln, rawCol, rawCol, rawCol) {
			m.msg = m.t("msg.added_cursor")
		}
		return nil
	}

	t.buf.SetCursor(ln, rawCol)
	t.buf.Deselect()

	// Start mouse drag for potential selection.
	m.mouseDown = true

	return nil
}

func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	// When git diff is focused, scroll the diff instead of the editor.
	if m.gitDiffFocused {
		if msg.Button == tea.MouseWheelUp {
			m.diffOffsetY--
		} else if msg.Button == tea.MouseWheelDown {
			m.diffOffsetY++
		}
		m.clampDiffScroll(m.viewHeight())
		return nil
	}
	p := m.curPane()
	if msg.Button == tea.MouseWheelUp {
		if p.offsetY > 0 {
			p.offsetY--
		}
	} else if msg.Button == tea.MouseWheelDown {
		t := &m.tabs[p.tabIdx]
		maxOff := t.buf.LineCount() - m.paneViewHeight(m.activePane)
		if maxOff < 0 {
			maxOff = 0
		}
		if p.offsetY < maxOff {
			p.offsetY++
		}
	}
	return nil
}

func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.mouseDown {
		return nil
	}
	y := msg.Y
	x := msg.X

	h := m.viewHeight()
	if y < 1 {
		y = 1
	}
	if y > h {
		y = h
	}

	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	editorRow := y - 1
	ln := editorRow + p.offsetY

	// Clamp line before accessing buffer.
	if ln >= t.buf.LineCount() {
		ln = t.buf.LineCount() - 1
	}
	if ln < 0 {
		ln = 0
	}

	leftW := m.leftRailWidth()
	gw := m.gutterWidthForTab(t)
	clickX := x - leftW - gw + p.offsetX
	if clickX < 0 {
		clickX = 0
	}
	rawCol := expandedToRawCol(t.buf.LineAt(ln), clickX, m.cfg.Editor.TabWidth)

	lineLen := t.buf.LineLen(ln)
	if rawCol > lineLen {
		rawCol = lineLen
	}

	t.buf.DragSelect(ln, rawCol)
	return nil
}

// expandedToRawCol converts an expanded (tab-expanded) column back to a raw
// character index, reversing the tab expansion done during rendering.
func expandedToRawCol(line []rune, expCol, tabWidth int) int {
	return expandedToRaw(line, expCol, tabWidth)
}

func expandedToRaw(line []rune, expCol, tabWidth int) int {
	col := 0
	for i, r := range line {
		if r == '\t' {
			next := ((col / tabWidth) + 1) * tabWidth
			if expCol < next {
				return i
			}
			col = next
		} else {
			if expCol <= col {
				return i
			}
			col++
		}
	}
	return len(line)
}

// cursorScreenPos returns the (x, y) position of the editor cursor in
// the terminal content, accounting for the tab bar, left rail, gutter,
// scroll offsets, and split layout.
func (m Model) cursorScreenPos() (int, int) {
	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	curLine := t.buf.CurLine()
	curRawCol := t.buf.Col()

	// Expand cursor column for tabs.
	raw := t.buf.LineAt(curLine)
	expCol := 0
	for i := 0; i < curRawCol && i < len(raw); i++ {
		if raw[i] == '\t' {
			expCol += m.cfg.Editor.TabWidth
		} else {
			expCol++
		}
	}

	gw := m.gutterWidthForTab(t)
	leftW := m.leftRailWidth()

	// X position within the pane content area.
	paneX := gw + (expCol - p.offsetX)

	// Determine the screen X based on which pane we're in.
	var screenX int
	switch m.layout {
	case splitVert:
		if m.activePane == 0 {
			screenX = leftW + paneX
		} else {
			screenX = leftW + m.paneTotalWidth(0) + 1 + paneX
		}
	default:
		screenX = leftW + paneX
	}

	// Y position: tab bar (1 row) + line offset within the pane.
	screenY := 1 + (curLine - p.offsetY)

	// For horizontal split, the second pane starts lower.
	if m.layout == splitHoriz && m.activePane == 1 {
		screenY += m.paneViewHeight(0) + 1 // +1 for separator
	}

	// Clamp to valid range.
	if screenX < 0 {
		screenX = 0
	}
	if screenY < 0 {
		screenY = 0
	}
	return screenX, screenY
}
