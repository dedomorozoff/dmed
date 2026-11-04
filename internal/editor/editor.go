package editor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/hinshun/vt10x"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/ask"
	"dmed/internal/buffer"
	"dmed/internal/config"
	"dmed/internal/dap"
	"dmed/internal/debug"
	"dmed/internal/events"
	"dmed/internal/i18n"
	"dmed/internal/lsp"
	"dmed/internal/mcp"
	"dmed/internal/plugin"
	"dmed/internal/ptyterm"
	"dmed/internal/session"
	"dmed/internal/sqlitedb"
	"dmed/internal/syntax"
	"dmed/internal/todo"
	"dmed/internal/vcs"
	"dmed/internal/watcher"
	"dmed/internal/websearch"
)

type tab struct {
	buf            *buffer.Buffer
	path           string
	syntaxCached   []syntax.HighlightedLine
	syntaxText     string
	syntaxWant     string // text the pending pass was started for
	syntaxPending  bool   // a highlighting pass is scheduled or running
	syntaxComplete bool   // the cached highlight covers the whole text
	langTag        string // cached syntax language of the file
	langKnown      bool
	ecPath         string // path the options below were read for
	ecOpts         config.FileOpts
	// inc is the per-document incremental token cache (internal/syntax): a
	// pass reuses the lines whose text and lexer state are unchanged and
	// tokenizes only the changed region plus its re-sync window. Recreated
	// when the file's language changes.
	inc           *syntax.Incremental
	incLang       string
	repo          *vcs.Repo // cached repository of the file's directory
	repoTried     bool
	diffCached    vcs.FileDiff
	diffText      string
	diffWant      string          // text the pending diff was started for
	diffPending   bool            // a diff pass is scheduled or running
	blame         []vcs.BlameLine // git blame of HEAD lines; nil = not computed
	lineEnding    string          // "lf" or "crlf"
	encoding      string          // "utf-8", "utf-16le", "utf-16be", "latin-1"
	wrapSegs      []wrapSeg
	wrapW         int
	wrapTabW      int
	wrapText      string
	foldCached    []foldRange // indentation fold blocks, cached like wrapSegs
	foldText      string
	foldTabW      int
	folds         map[int]bool // folded block start lines; nil = nothing folded
	img           *imageView   // a picture tab: decoded frames, nil = plain text
	delimMatch    buffer.DelimiterMatch
	hasDelimMatch bool
	// modSeen is the buffer change counter the buffer:modified publication
	// last saw; the per-frame compare notices a move without a text compare.
	modSeen uint64
}

// wrapSeg describes one screen row of a wrapped (or plain) buffer line: the
// line number and the tab-expanded column range it covers. In non-wrap mode
// every buffer line yields exactly one segment spanning the whole line.
type wrapSeg struct {
	line     int
	expStart int
	expEnd   int
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

	// Detect CRLF: only treat \r\n sequences as CRLF, not lone \r
	// (old Mac line endings or stray carriage returns).
	hasCRLF := false
	for i := 0; i+1 < len(data); i++ {
		if data[i] == '\r' && data[i+1] == '\n' {
			hasCRLF = true
			break
		}
	}
	if hasCRLF {
		lineEnding = "crlf"
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

// getSyntaxLines returns the highlighted lines the tab currently knows about.
// The highlighting itself runs in the background (see syntax.go): the UI
// thread must never wait for a whole-file re-tokenisation.
func (t *tab) getSyntaxLines() []syntax.HighlightedLine {
	return t.syntaxCached
}

// tabWrap returns the wrap segments for the tab at the given content width:
// one segment per screen row. In non-wrap mode this is one full-line segment
// per buffer line; callers only enable segmentation when the pane wraps.
// Results are cached and invalidated automatically when the text, content
// width, or tab width changes.
func (t *tab) tabWrap(contentW, tabW int) []wrapSeg {
	text := t.buf.Text()
	if t.wrapSegs != nil && t.wrapW == contentW && t.wrapTabW == tabW && t.wrapText == text {
		return t.wrapSegs
	}
	var segs []wrapSeg
	for ln := 0; ln < t.buf.LineCount(); ln++ {
		line := t.buf.LineAt(ln)
		var exp []rune
		for _, r := range line {
			if r == '\t' {
				for k := 0; k < tabW; k++ {
					exp = append(exp, ' ')
				}
			} else {
				exp = append(exp, r)
			}
		}
		segs = append(segs, wrapLine(ln, exp, contentW)...)
	}
	t.wrapSegs = segs
	t.wrapW = contentW
	t.wrapTabW = tabW
	t.wrapText = text
	return segs
}

// wrapLine splits one tab-expanded line into width-sized segments, breaking at
// word boundaries when possible.
func wrapLine(ln int, exp []rune, w int) []wrapSeg {
	if w < 1 {
		w = 1
	}
	if len(exp) <= w {
		return []wrapSeg{{line: ln, expStart: 0, expEnd: len(exp)}}
	}
	var segs []wrapSeg
	start := 0
	for start < len(exp) {
		end := start + w
		if end > len(exp) {
			end = len(exp)
		}
		if end < len(exp) {
			// Prefer breaking right after the last space inside the window.
			brk := -1
			for i := end - 1; i > start; i-- {
				if exp[i] == ' ' {
					brk = i + 1
					break
				}
			}
			if brk > start {
				end = brk
			}
		}
		if end <= start {
			end = start + 1
			if end > len(exp) {
				end = len(exp)
			}
		}
		segs = append(segs, wrapSeg{line: ln, expStart: start, expEnd: end})
		if end == len(exp) {
			break
		}
		start = end
	}
	return segs
}

// segRowForCol finds the screen row (index into tabWrap's result) that shows
// the given tab-expanded column of a buffer line, returning its segment start.
func segRowForCol(segs []wrapSeg, line, expCol int) (row, expStart int) {
	for i := range segs {
		s := segs[i]
		if s.line == line && expCol >= s.expStart && expCol < s.expEnd {
			return i, s.expStart
		}
	}
	// Fall back to the last segment of the line (column past end of line).
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].line == line {
			return i, segs[i].expStart
		}
	}
	return 0, 0
}

// lang returns the syntax language of the tab. The name of the file does not
// change, so the lookup (which asks chroma and touches the filesystem) must
// not run on every frame.
func (t *tab) lang() string {
	if !t.langKnown {
		t.langTag = syntax.Lang(t.path)
		t.langKnown = true
	}
	return t.langTag
}

// repoFor returns the git repository the tab belongs to: the one of the
// editor when it covers the file, otherwise the repository of the file's
// directory. The lookup is cached — opening a repository means touching the
// filesystem, and the gutter asks for it on every frame.
func (t *tab) repoFor(repo *vcs.Repo) *vcs.Repo {
	if repo != nil && strings.HasPrefix(t.path, repo.Root) {
		return repo
	}
	if !t.repoTried {
		t.repoTried = true
		if found, err := vcs.Open(filepath.Dir(t.path)); err == nil {
			t.repo = found
		}
	}
	return t.repo
}

// getDiff returns the diff of the buffer against HEAD. The diff itself is
// computed in the background (see gdiff.go) — it is a whole-file job and
// would cost tens of milliseconds per keystroke on the UI thread — so this
// only hands out the result of the last pass.
func (t *tab) getDiff() vcs.FileDiff {
	if t.path == "" || t.diffCached.Lines == nil {
		return vcs.FileDiff{}
	}
	return t.diffCached
}

type Model struct {
	root string
	cfg  config.Config
	// keys is this model's resolved chord map. The tables in keys.go are the
	// defaults and stay read-only; the chain below reads only through here, so a
	// rebind applies to this model and a hot reload swaps the pointer rather
	// than editing shared data.
	keys *KeyMap
	g    glyphSet // active render glyphs (unicode or ASCII fallback)
	// syn is the syntax highlighter owned by the model. It was a package-level
	// variable in internal/syntax, written from Update on config hot-reload
	// and read while rendering; keeping it here removes that global mutable.
	syn     *syntax.Highlighter
	tr      i18n.Translator
	plugins *plugin.Manager
	// pluginCh is the wake channel of the plugin pump: an event or a spawn
	// completion queues in the manager (from any goroutine) and one
	// pluginPumpMsg here asks the UI thread to run the waiting Lua.
	pluginCh   chan tea.Msg
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
	Version    string

	// imgPending keeps the animation-tick chain single: every message asks for
	// a tick and without it each of them would start another chain.
	imgPending bool
	// profile is the terminal's colour answer (tea.ColorProfileMsg); the picture
	// renderer must convert to it rather than ship escapes it cannot read.
	profile colorprofile.Profile

	promptOpen      bool
	promptIn        []rune
	promptSave      bool
	promptSaveIn    []rune
	promptNewFile   bool
	promptNewFolder bool
	promptRename    bool
	promptRenameRel string

	finderOpen  bool
	finderQ     []rune
	finderFiles []string
	finderHits  []string
	finderSel   int

	// Folder browser: a native TUI picker so "File: Open Folder..." works the
	// same on every platform (no zenity/kdialog/PowerShell dependency).
	folderOpen    bool
	folderPath    string // current directory being browsed
	folderEntries []folderEntry
	folderSel     int
	folderOffset  int

	helpOpen   bool
	helpScroll int // help panel scroll offset (the list overflows small screens)

	aiCfgOpen  bool
	aiCfgField int
	aiCfgEdit  bool
	aiCfgIn    []rune
	aiCfgTest  aiTestState // last connection probe from the wizard Test row
	// aiCfgModels is the list the provider reported, so the Model row can offer
	// real choices; aiCfgGen discards replies from a superseded request.
	aiCfgModels []string
	aiCfgGen    int
	// aiCfgNeedsReload remembers a probe that was skipped because one was
	// already in flight when the endpoint changed.
	aiCfgNeedsReload bool
	// The open option list for a choice row (Provider, Model, Allow Run,
	// Restrict Root). A list, not a value cycler: fifty models cannot be walked
	// one arrow press at a time.
	aiCfgSelOpen  bool
	aiCfgSelField int
	aiCfgSelItems []string
	aiCfgSelIdx   int
	aiCfgSelOff   int

	treeVisible    bool
	treeFocus      bool
	treeRows       []treeEntry
	treeSel        int
	treeOffset     int
	expanded       map[string]bool
	treeConfirm    string // "" | "delete" | "trash" — pending file action confirmation
	treeConfirmRel string // path (relative to baseDir) the confirmation applies to

	// Search/replace
	searchOpen         bool
	searchQuery        []rune
	searchMatchIdx     int
	searchTotalMatches int
	// searchRegex toggles regex mode (Tab in the search prompt); the compiled
	// pattern is memoized so rendering does not recompile it per line.
	searchRegex      bool
	searchRe         *regexp.Regexp
	searchReQuery    string
	replaceOpen      bool
	replaceWith      []rune
	replaceFocusFind bool

	// Find in project (Ctrl+Shift+F): async full-text search across the tree.
	projSearchOpen  bool
	projSearchInput bool // prompt mode (true) vs results navigation
	projSearchQ     []rune
	projSearchRegex bool
	projSearchHits  []projHit
	projSearchSel   int
	projSearchOff   int
	projSearchGen   int  // one generation stamps both scans of this panel
	projSearchScan  bool // a background walk is in flight
	projSearchCh    chan projSearchMsg

	// Replace in project (Ctrl+Shift+R): the same panel with a second field. The
	// scan produces a file-level preview, and nothing is written until a diff
	// review hands the series to the agent Applier.
	projSearchRepl bool
	projReplStep   int // 0 find field, 1 replace field, 2 preview list
	projReplWith   []rune
	projReplFiles  []projReplFile
	projReplOrder  []int // rows that carry a change, in list order
	projReplStats  projReplStats
	projReplScan   bool
	projReplCh     chan projReplMsg
	// The side-by-side review of one pending change.
	projReplReview bool
	projReplRevIdx int
	projReplRows   []vcs.DiffRow
	projReplLeft   []string
	projReplRight  []string
	projReplOffY   int
	projReplOffX   int

	// LSP rename (Ctrl+Alt+R), code actions (Ctrl+Alt+Q) and reformat
	// (Ctrl+Alt+L, legacy fallback Ctrl+Alt+F).
	lspRenameOpen  bool
	lspRenameIn    []rune
	lspActionsOpen bool
	lspActionsSel  int
	lspActionsOff  int
	lspActions     []lsp.CodeAction

	// M10: diagnostics navigation, hover popup, references and outline.
	// diagsCursor tracks the F8 walk through the active tab's diagnostics;
	// hover renders as a bottom overlay like the rename input; references and
	// outline reuse the code-actions panel shape (a bottom list with j/k and
	// Enter jumping to the target).
	diagCursor int // index into the sorted diags of the active tab, -1 = unset
	hoverOpen  bool
	hoverText  []string

	refsOpen  bool
	refsSel   int
	refsOff   int
	refs      []lsp.Location
	refsPaths []string // resolved paths for each ref (refs[i].Path)

	outlineOpen bool
	outlineSel  int
	outlineOff  int
	outline     []lsp.DocumentSymbol

	// "AI: Switch Model…" — the provider's list as a bottom panel, stamped
	// so a reply to an abandoned pick cannot open anything.
	modelPickOpen bool
	modelPickSel  int
	modelPickOff  int
	modelPick     []string
	modelPickGen  uint64

	// Go to line
	gotoOpen bool
	gotoIn   []rune

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
	gitFocus           bool // keys route to the git panel; open+unfocused shows it in the sidebar
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

	// Inline git blame annotations (Alt+B)
	blameOn bool

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

	// SQLite browser (fullscreen view opened for a .db file)
	sqlOpen       bool
	sqlPath       string
	sqlDB         *sqlitedb.DB
	sqlTables     []sqlitedb.Entry
	sqlTbl        int // index into sqlTables
	sqlCols       []sqlitedb.Column
	sqlHasRid     bool // current table has rowid → editable
	sqlRows       [][]sqlitedb.Value
	sqlRids       []string
	sqlTotal      int
	sqlOff        int // first loaded row
	sqlLim        int // page size
	sqlRow        int // cursor row within the page
	sqlCol        int // cursor column within sqlCols
	sqlOffX       int // horizontal scroll in characters
	sqlOrder      int // order-by column index, -1 = none
	sqlDesc       bool
	sqlFilter     string
	sqlFilterIn   bool // filter input active on the bottom line
	sqlFilterBuf  []rune
	sqlEdit       *sqlCellEdit // active cell editor, nil otherwise
	sqlSchema     bool         // DDL view shown instead of the grid
	sqlSchemaOffY int
	sqlSchemaText string

	// Bottom terminal panel (real PTY + ANSI screen state)
	termOpen     bool
	termRows     []terminalRow
	termCursorX  int
	termCursorY  int
	termCursorOK bool
	termSession  *ptyterm.Terminal
	termVT       vt10x.Terminal
	termCh       chan terminalOutputMsg
	termExitCh   chan terminalExitMsg
	termGen      int

	// DAP debug panel (Delve) — the backend is a DAP client or the DBGp
	// Xdebug client, either of which serves the same panel interface.
	dapClient             debugBackend
	dapCh                 chan dapEventMsg
	dapOpen               bool
	dapRunState           string
	dapReason             string
	dapThreads            []dap.Thread
	dapFrames             []dap.StackFrame
	dapSelThread          int
	dapSelFrame           int
	dapFocus              int // 0=threads, 1=frames, 2=variables
	dapVarStack           [][]dapVarRow
	dapVarSel             int
	dapScopes             []dap.Scope
	dapConsole            []string
	dapIn                 []rune
	dapBreak              map[string]map[int]bool // abs path → line → true
	dapBPVerif            map[string]map[int]bool // adapter-verified breakpoints
	bookmarks             map[string]map[int]bool // abs path → line (1-based) → true
	syntaxCh              chan tea.Msg            // messages of the highlighting pipeline
	diffCh                chan tea.Msg            // messages of the git diff pipeline
	dapCurPath            string
	dapCurLine            int
	dapBusy               bool
	dapGen                int  // session generation; drops stale start/launch msgs
	dapFollowPending      bool // reveal the stopped location on the next Update
	dapConsolePeek        bool
	dapConsoleScroll      int // console lines scrolled back from the newest
	dapPanelRows          int // panel height override; 0 = a quarter of the terminal
	dapSupportsConfigDone bool
	dapDeduced            *config.DebugConfig // language-detected [debug] for the live session

	// DAP settings wizard (debug/launch configuration dialog)
	dapCfgOpen  bool
	dapCfgField int
	dapCfgEdit  bool
	dapCfgIn    []rune

	// Theme panel (the [theme] section and the base style, with a sample painted
	// from what the rows say before any of it is written)
	themeCfgOpen  bool
	themeCfgField int
	themeCfgEdit  bool
	themeCfgIn    []rune
	themeName     string            // the base chroma style the panel is holding
	themeTok      string            // token name being given a rule
	themeSpec     string            // style entry written for that token
	themeRules    map[string]string // staged [theme] rules, not yet in the file
	themePreview  []string          // the sample, painted with the staged rules
	themeRefused  []string          // rules the file holds that read as nothing

	// Command palette & Clipboard
	paletteOpen   bool
	paletteQ      []rune
	paletteSel    int
	paletteOffset int
	paletteAll    bool     // "search everywhere": commands, files and settings
	paletteFiles  []string // the project files the palette can offer
	clipboard     string
	// True when `clipboard` was copied off a column (rectangular) selection, so a
	// paste can unfold it back down a column — the system clipboard carries no
	// type, this flag is the only memory. See column.go.
	clipboardBlock bool

	// Language chooser
	langChooserOpen bool
	langChooserSel  int

	// Plugin store (install/uninstall bundled plugins)
	pluginStoreOpen       bool
	pluginStoreSel        int
	pendingPluginRemovals map[string]bool
	storeItems            []storeItem
	storeLoading          bool
	storeErr              string
	pendingStoreInstall   string

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
	// aiKey fingerprints the [ai] settings the current provider was built
	// from, so a hot-reloaded .dmed.conf transparently rebuilds it instead of
	// pinning the session to a stale endpoint/model/key.
	aiKey         string
	chatCh        <-chan chatEvent
	chatCancel    context.CancelFunc
	chatToolRound int    // remaining tool loop iterations for the current turn
	chatGen       uint64 // conversation generation, guards against stale stream events

	// Chat history: persisted threads (newest-first; -1 = new unsaved thread)
	// and the prompt input history browsed with Up/Down in the input.
	chatThreads    []chatThread
	chatThreadPos  int
	chatPrompts    []string
	chatPromptIdx  int // -1 = free input, 0 = most recent prompt
	chatDraft      string
	chatHistLoaded bool
	chatClearArm   bool // Ctrl+L pressed once: next Ctrl+L wipes history

	// Pending native-tool round awaiting finalisation (assistant message with
	// its tool calls plus the tool result messages that follow).
	chatPendingAssistant ai.Message
	chatPendingResults   []ai.Message
	chatPendingChanges   []agent.Change
	chatEditTracks       []chatEditTrack

	// Token accounting for the session: chatTurnUsage adds up the reports of
	// the turn in progress (tool rounds included), chatSessionUsage is the
	// running total the header and the cards read.
	chatTurnUsage    ai.Usage
	chatSessionUsage ai.Usage

	// Chat EDIT diff review: proposals are applied only after the user picks
	// y (apply) or n (discard), routed through the agent Applier.
	chatReviewMode  bool
	chatReviewIdx   int
	chatReviewRows  []vcs.DiffRow
	chatReviewLeft  []string
	chatReviewRight []string
	chatReviewOffY  int
	chatReviewOffX  int

	// Tool calls parked for an explicit user decision (today: a RUN command
	// with allow_run = ask, or an ASK_USER question). While a park is active
	// the chat input is parked too; the pending ones wait in chatParks so a
	// single round that needs several approvals asks for them one by one.
	chatPark  *toolPark
	chatParks []toolPark

	// The model's checklist and the questions it is waiting an answer for.
	// Both live behind pointers because they are written from the chat tool
	// goroutine while the render loop reads them.
	todo *todo.List
	asks *ask.Queue

	// aiFallback is set when the session runs on a keyless public provider
	// because nothing was configured and nothing local answered. chatNotice
	// explains that in the chat, because prompts and code leave the machine.
	aiFallback bool
	chatNotice string
	// aiFreeURLOverride redirects the keyless fallback to another host; only
	// tests set it (the public service must never be contacted from a unit
	// test). Empty means "use the preset".
	aiFreeURLOverride string

	// Web search: the client (built on first use) and how many queries this
	// session has spent. webSearchOverride lets tests aim it at a local server.
	web               *websearch.Client
	webSearchUsed     int
	webSearchOverride *websearch.Client

	// MCP tool servers ([ai] mcp.*): the manager built from the last spawn
	// round, the generation that round was stamped with, and the fingerprint
	// of the server set it was started for — a config reload respawns only
	// when the set actually moved. mcpSpawn is the test seam: a unit test
	// injects a client over an in-memory pipe instead of starting a process.
	mcpMgr   *mcp.Manager
	mcpGen   int
	mcpFP    string
	mcpSpawn func(cmdline, dir string) (*mcp.Client, error)

	// The question currently on screen and the answer being typed into it.
	// askSel indexes into askReq.Choices (-1 while there are none).
	askReq *ask.Request
	askIn  []rune
	askSel int

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
	// aiFix (LSP diagnostic fix) state
	aiFixOpen        bool
	aiFixBusy        bool
	aiFixCh          <-chan chatEvent
	aiFixCancel      context.CancelFunc
	aiFixInput       []rune
	aiFixOriginal    string
	aiFixProposal    string
	aiFixReviewMode  bool
	aiFixReviewRows  []vcs.DiffRow
	aiFixReviewLeft  []string
	aiFixReviewRight []string
	aiFixReviewOffY  int
	aiFixReviewOffX  int
	aiReviewOffX     int

	// Ghost text (Copilot-style inline suggestions)
	ghostLines   []string // lines of ghost suggestion
	ghostCol     int      // column where ghost starts
	ghostRow     int      // line where ghost starts
	ghostVisible bool     // whether ghost overlay is active
	ghostCh      <-chan chatEvent
	ghostText    string // accumulated text during streaming
	ghostCancel  context.CancelFunc
	// ghostGen stamps the debounce window and the streamed request: a new
	// edit bumps it, and every message carrying an older value is dropped.
	ghostGen    uint64
	ghostStamps []time.Time // recent edit times: the rhythm the window reads

	quitConfirm bool
	quitTab     bool // true if confirming close of a single tab (not quit)
	pendingQuit bool

	// Agent background tasks (M4)
	agentQueue   *agent.Queue
	agentRunner  *agent.Runner
	agentApplier *agent.Applier
	agentCommit  *agent.Committer
	// projReplApplier is the human path's atomic writer: Replace in Project
	// lands its previewed series through it, never with a direct file write.
	projReplApplier *agent.Applier
	agentCtx        context.Context
	agentCancel     context.CancelFunc
	agentCh         chan struct{} // repaint signal from the agent worker
	agentOpen       bool
	agentFocus      bool // keys route to the agent panel; open+unfocused shows it in the sidebar
	agentPrompt     bool // entering a new task prompt
	agentPromptIn   []rune
	agentSel        int
	agentOffset     int
	// agentStoreSig is the durable shape last written to .dmed_agent.json
	// (ids+statuses+errors, no progress): a refresh that only moved progress
	// must not rewrite the file.
	agentStoreSig string

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
	mouseDown  bool
	hoverIcon  statusAction // status-bar icon under the cursor (actNone if none)
	hoverSplit statusAction // top-right split icon under the cursor

	// Panel text selection: drag with the mouse over the chat transcript or
	// (with Shift held) over the terminal output; on release the selected
	// text lands in the system clipboard.
	chatSelActive bool
	chatSelAnchor selPos
	chatSelEnd    selPos
	termSelActive bool
	termSelAnchor selPos
	termSelEnd    selPos
	dragChat      bool // motion events extend the chat selection
	dragTerm      bool // motion events extend the terminal selection
	dragColumn    bool // Shift+drag extends the column (rectangular) selection

	// Macro recording (Alt+R record, Alt+Shift+R replay). macroKeys collects
	// the text-pane keys while recording; macro is the last completed recording
	// the replay walks one key per UI step. Not persisted. See macro.go.
	macroRecording bool
	macroKeys      []macroKey
	macro          []macroKey
	replaying      bool
	termFocus      bool             // the terminal panel owns keyboard input
	hoverChatBtn   chatButtonAction // chat panel button under the cursor

	// Double-click detection: last click position/time plus a validity flag so
	// a third quick click starts a fresh pair instead of chaining.
	lastClickX     int
	lastClickY     int
	lastClickTime  time.Time
	lastClickValid bool

	// Double-Shift detection (JetBrains-style "search everywhere"): the time of
	// the previous bare Shift press so two rapid taps open the palette. Only
	// terminals with the Kitty protocol / Windows Console API report bare
	// modifier presses, so this degrades gracefully elsewhere.
	lastShiftTime time.Time
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

func New(paths ...string) Model {
	fe := make(chan string, 16)
	pe := make(chan tea.Msg, 8)
	m := Model{
		g:                     unicodeGlyphs,
		width:                 80,
		height:                24,
		expanded:              map[string]bool{},
		fileEvents:            fe,
		pluginCh:              pe,
		bus:                   events.New(),
		diagCh:                make(chan lspDiagMsg, 64),
		diags:                 map[string][]lsp.Diagnostic{},
		pendingPluginRemovals: map[string]bool{},
		dapCh:                 make(chan dapEventMsg, 64),
		dapBreak:              map[string]map[int]bool{},
		dapBPVerif:            map[string]map[int]bool{},
		bookmarks:             map[string]map[int]bool{},
		syntaxCh:              make(chan tea.Msg, 8),
		diffCh:                make(chan tea.Msg, 8),
		chatThreadPos:         -1,
		chatPromptIdx:         -1,
		todo:                  todo.New(),
		asks:                  ask.NewQueue(),
	}
	if w, err := watcher.New(func(p string) {
		select {
		case fe <- p:
		default:
		}
	}); err == nil {
		m.watcher = w
	}
	// Chroma parses every embedded grammar on its first lookup. Doing that
	// in the background keeps the first frame from waiting for it.
	go debug.CapturePanicReport(func() { syntax.Warmup() })

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
			m.restoreBookmarks(sess.Bookmarks)
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
	m.keys = ResolveKeys(m.cfg)
	m.mcpFP = m.mcpFingerprint()
	// Create the AI provider up front so chat, inline, and ghost all work even
	// before the chat panel is first opened (previously ghost silently no-opped
	// until toggleChat ran).
	m.ai = m.aiProvider(m.cfg.AI.Model)
	m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
	m.syn, m.themeRefused = syntaxFor(m.cfg)
	if msg := m.configRefusals(); msg != "" {
		m.msg = msg
	}
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
	// The plugin pump: background-arriving Lua work (bus events, dmed.spawn
	// completions) queues in the manager and one message on pluginCh asks the
	// Update loop to run the waiting handlers on this thread.
	m.plugins.SetWake(func() {
		select {
		case pe <- pluginPumpMsg{}:
		default:
		}
	})
	m.wirePluginEvents()
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
			"# skipped_dirs = .git,node_modules\n" +
			"# A .editorconfig beside a file overrides tab_width for that file:\n" +
			"# indent_style, indent_size, tab_width, trim_trailing_whitespace and\n" +
			"# insert_final_newline are read per file, and re-read when the file changes\n\n" +
			"[ai]\n" +
			"# provider = ollama  # ollama | openai\n" +
			"# model =\n" +
			"# ollama_url = http://localhost:11434\n" +
			"# api_key =  # for OpenAI-compatible providers\n" +
			"# context_max = 6000\n" +
			"# temperature = 0  # tenths (7 => 0.7); 0 = provider default\n" +
			"# num_ctx = 0  # Ollama context window tokens; 0 = default\n" +
			"# num_predict = 0  # max output tokens; 0 = provider default\n" +
			"# tool_rounds = 0  # chat tool-loop cap; 0 = built-in (6)\n" +
			"# allow_run = ask  # always | never | ask (default: ask = confirm each command)\n" +
			"# restrict_to_root = true  # true (default) = keep READ/EDIT/REPLACE inside the project\n\n" +
			"[agent]\n" +
			"# system_prompt =  # optional override for background agent tasks\n" +
			"# context_max = 262144  # bytes of file context sent to the agent\n\n" +
			"[ui]\n" +
			"# tree_width = 25\n" +
			"# chat_width_pct = 40\n\n" +
			"[plugins]\n" +
			"# repo = dedomorozoff/dmed  # GitHub store for the plugin store\n" +
			"# dir = plugins\n" +
			"# branch = main\n\n" +
			"[keys]\n" +
			"# action = chord[, chord]  # rebind a command; an empty value unbinds it\n" +
			"# file.save = ctrl+s, ctrl+1\n" +
			"# dmed --dump-keys actions lists the action names and the chords they ship\n" +
			"# with; Esc, Enter, Tab, the movement keys and Ctrl+Q refuse a rebind\n\n" +
			"[theme]\n" +
			"# token = style  # recolour one syntax token over the base theme\n" +
			"# keyword = #ff79c6 bold\n" +
			"# comment = #6272a4 italic bg:#101014\n" +
			"# An empty value keeps the line and switches the rule off.\n" +
			"# dmed --dump-tokens lists the token names; the style is chroma's grammar:\n" +
			"# a hex colour, bg:#rrggbb, and bold / italic / underline, each of which a\n" +
			"# no-prefix turns back off (nobold)\n"
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

// normalizePaste converts CRLF / lone CR line endings in pasted text to LF.
// Windows terminals and the system clipboard deliver "\r\n", and a literal
// "\r" inside buffer content would both garble the terminal rendering (the
// terminal treats it as a carriage return) and pollute the saved file.
func normalizePaste(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// pasteInput inserts pasted text into the active field, mirroring where typed
// keys land (see handleKey routing), and falls back to the editor buffer.
func (m *Model) pasteInput(text string) {
	text = normalizePaste(text)
	switch {
	case m.aiCfgEdit:
		m.aiCfgIn = append(m.aiCfgIn, []rune(text)...)
		return
	case m.aiInlineOpen:
		m.aiInlineInput = append(m.aiInlineInput, []rune(text)...)
		return
	case m.agentPrompt:
		m.agentPromptIn = append(m.agentPromptIn, []rune(text)...)
		return
	case m.chatOpen && m.chatFocus:
		m.chatIn = append(m.chatIn, []rune(text)...)
		return
	case m.promptSave:
		m.promptSaveIn = append(m.promptSaveIn, []rune(text)...)
		return
	case m.promptOpen:
		m.promptIn = append(m.promptIn, []rune(text)...)
		return
	case m.searchOpen:
		if m.replaceOpen && !m.replaceFocusFind {
			m.replaceWith = append(m.replaceWith, []rune(text)...)
		} else {
			m.searchQuery = append(m.searchQuery, []rune(text)...)
			m.updateSearchMatches(false)
		}
		return
	case m.projSearchOpen && m.projSearchInput:
		if m.projSearchRepl && m.projReplStep == 1 {
			m.projReplWith = append(m.projReplWith, []rune(text)...)
		} else {
			m.projSearchQ = append(m.projSearchQ, []rune(text)...)
		}
		return
	case m.lspRenameOpen:
		m.lspRenameIn = append(m.lspRenameIn, []rune(text)...)
		return
	case m.finderOpen:
		m.finderQ = append(m.finderQ, []rune(text)...)
		m.refind()
		return
	case m.paletteOpen:
		m.paletteQ = append(m.paletteQ, []rune(text)...)
		return
	}
	if m.cur().buf.HasMultipleCursors() {
		m.cur().buf.MultiInsertText(text)
	} else {
		m.cur().buf.InsertText(text)
	}
	m.msg = m.t("msg.pasted")
}

func (m *Model) handleFinder(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("finder", s) {
	case "finder.close":
		m.finderOpen = false
	case "finder.run":
		if n := len(m.finderHits); n > 0 {
			path := m.finderHits[m.finderSel]
			m.finderOpen = false
			m.focusOrOpen(path)
		} else {
			m.finderOpen = false
		}
	case "finder.up":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel - 1 + n) % n
		}
	case "finder.down":
		if n := len(m.finderHits); n > 0 {
			m.finderSel = (m.finderSel + 1) % n
		}
	case "finder.erase":
		if n := len(m.finderQ); n > 0 {
			m.finderQ = m.finderQ[:n-1]
			m.refind()
		}
	case paneUnbound:
		return nil
	default:
		if len(msg.Text) > 0 {
			m.finderQ = append(m.finderQ, []rune(msg.Text)...)
			m.refind()
		}
	}
	return nil
}

func (m *Model) handleHelp(s string) tea.Cmd {
	switch m.paneCmd("help", s) {
	case "help.close":
		m.helpOpen = false
	case "help.down":
		m.scrollHelp(1)
	case "help.up":
		m.scrollHelp(-1)
	case "help.top":
		m.helpScroll = 0
	case "help.bottom":
		m.helpScroll = m.helpMaxScroll()
	}
	return nil
}

// scrollHelp moves the help panel by d rows (negative = up) and clamps it.
func (m *Model) scrollHelp(d int) {
	m.helpScroll += d
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
	if max := m.helpMaxScroll(); m.helpScroll > max {
		m.helpScroll = max
	}
}

// helpMaxScroll is the maximum help scroll offset so the last row stays
// visible; 0 when everything already fits.
func (m Model) helpMaxScroll() int {
	n := len(m.helpRows()) + 1 // title row + entries
	if h := m.viewHeight(); n > h {
		return n - h
	}
	return 0
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

// pluginPumpMsg wakes the Update loop to run the Lua work the plugin
// manager has queued (bus events, dmed.spawn completions).
type pluginPumpMsg struct{}

func waitForPlugin(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForFileEvent(m.fileEvents), waitForTermOutput(m.termCh), waitForChatOutput(m.chatCh, m.chatGen), waitForInlineOutput(m.aiInlineCh), waitForFixOutput(m.aiFixCh), waitForLSPDiag(m.diagCh), waitForDAPEvent(m.dapCh), waitForSyntax(m.syntaxCh), waitForDiff(m.diffCh), waitForPlugin(m.pluginCh), m.mcpCmd(m.mcpGen))
}

// Update handles a message and keeps the background highlighting in step
// with the buffers: a stale highlight is recomputed off the UI thread, so
// typing never waits for a whole-file re-tokenisation.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	mdl, cmd := m.update(msg)
	mm, ok := mdl.(Model)
	if !ok {
		return mdl, cmd
	}
	mm.requestSyntax()
	mm.requestDiff()
	// Publish buffer:modified for the tabs whose change counter moved this
	// frame; the bus subscription routes it into the plugin pump.
	mm.emitBufferModified()
	return mm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case diffPassMsg:
		// The buffer had time to settle: start the diff pass if the text is
		// still the one the wait was started for.
		m.runDiff(msg.tabIdx, m.tabs[msg.tabIdx].repoFor(m.repo))
		return m, waitForDiff(m.diffCh)
	case diffReadyMsg:
		// A background diff landed: keep it if it still matches the buffer
		// and arm the waiter for the next one.
		m.applyDiff(msg)
		return m, waitForDiff(m.diffCh)
	case syntaxPassMsg:
		// The buffer had time to settle: start the pass if the text is still
		// the one the wait was started for, otherwise wait again.
		m.runSyntax(msg.tabIdx)
		return m, waitForSyntax(m.syntaxCh)
	case syntaxReadyMsg:
		// A background pass landed: keep it if it still matches the buffer
		// and arm the waiter for the next one.
		m.applySyntax(msg)
		return m, waitForSyntax(m.syntaxCh)
	case pluginPumpMsg:
		// The pump: every Lua work a background goroutine queued (bus events,
		// spawn completions) runs here, on the UI thread, and the waiter
		// re-arms for the next wake. A burst coalesces into one drain.
		m.drainPlugins()
		return m, waitForPlugin(m.pluginCh)
	case mcpReadyMsg:
		// A spawn round landed. A reply from a superseded server set is
		// dropped, as every other async subsystem drops its stale stamp.
		m.applyMCPReady(msg)
		return m, nil
	case FileChangedMsg:
		path := msg.Path
		// Every file change funnels through the bus here: agent commits
		// publish from their own goroutine, and the watcher's events join on
		// the UI thread in this branch. The plugin pump delivers both.
		m.publishBus(events.Event{Type: events.EventFileChanged, Path: path})
		// Hot-reload config when .dmed.conf changes
		if strings.HasSuffix(path, ".dmed.conf") {
			m.cfg = config.Load(m.root)
			m.keys = ResolveKeys(m.cfg)
			m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
			// A new highlighter is built from the style and the [theme] rules. The
			// per-tab caches are kept: they hold token kinds, and a theme changes
			// only which style a kind paints as, which the view asks for per run.
			syn, refused := syntaxFor(m.cfg)
			m.syn = syn
			m.themeRefused = refused
			if m.themeCfgOpen {
				// The panel is showing the file, so a change made in the file has to
				// be reflected in it rather than left behind as a stale preview.
				m.themeName = m.cfg.Editor.SyntaxTheme
				m.themeRules = themeRulesOf(m.cfg)
				m.refreshThemePreview()
			}
			m.msg = m.t("msg.config_reloaded")
			if msg := m.configRefusals(); msg != "" {
				m.msg = msg
			}
			// A moved [ai] mcp.* set respawns the servers; the same reload
			// with the set untouched leaves running processes alone.
			if m.mcpFingerprint() != m.mcpFP {
				return m, tea.Batch(waitForFileEvent(m.fileEvents), m.restartMCP())
			}
			return m, waitForFileEvent(m.fileEvents)
		}
		// A changed .editorconfig is read again on the next frame: the per-tab
		// cache is keyed on the path, so dropping the key is the whole reload.
		// The wrap and fold caches need nothing here, both are keyed on the tab
		// stop they were built with and a different stop rebuilds them.
		if filepath.Base(path) == config.EditorConfigFile {
			m.dropFileOptsCaches()
		}
		// Hot-reload a changed plugin without restarting the editor.
		if m.plugins != nil && m.isPluginPath(path) {
			// Skip events for plugins the store just removed itself.
			if m.pendingPluginRemovals[path] {
				delete(m.pendingPluginRemovals, path)
				return m, waitForFileEvent(m.fileEvents)
			}
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
				// A picture tab reloads as a picture — and falls back to text
				// with the reason if the file stopped being one. Letting the
				// bytes through to the buffer is the one place a re-decoded
				// picture silently becomes mojibake again, and conflict mode
				// is meaningless when the user cannot edit what is on screen.
				if t.img != nil {
					t.img = nil
					if data, err := os.ReadFile(t.path); err == nil {
						if v, err2 := loadImage(data); err2 == nil {
							t.img = v
						} else {
							t.buf = buffer.Load(normalizePaste(string(data)))
							m.msg = m.t("msg.image_failed", err2.Error())
						}
					}
					break
				}
				if !t.buf.Dirty() {
					if data, err := os.ReadFile(t.path); err == nil {
						t.buf = buffer.Load(normalizePaste(string(data)))
						t.syntaxCached = nil
						t.diffText = ""
						m.msg = m.t("msg.reloaded_name", t.name(m.baseDir()))
					}
				} else {
					if data, err := os.ReadFile(t.path); err == nil {
						diskText := normalizePaste(string(data))
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
		// The language server learns about files changed outside the editor
		// (agent commits, tool runs): without the notification it keeps
		// serving stale diagnostics from its caches.
		if m.lspClient != nil && m.lspClient.Alive() {
			m.lspClient.DidChangeWatchedFiles([]string{path})
		}
		return m, waitForFileEvent(m.fileEvents)
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.resizeTerminal()
	case tea.ColorProfileMsg:
		// The terminal's colour range: the picture renderer converts every
		// pixel to it. A profile nobody delivered falls back to Detect at
		// draw time.
		m.profile = msg.Profile
	case imgFrameMsg:
		// The tick clears its own pending flag, then the tail either advances
		// the animation (it is still on screen) or lets the chain die — and
		// schedules the next frame either way.
		m.imgPending = false
		if m.animationDelay() > 0 {
			m.advanceFrame()
		}
	case macroReplayMsg:
		// One recorded key per UI step: the frame paints between the steps of
		// a long replay the way it does between GIF frames.
		return m, m.replayStep(msg.idx)
	case terminalOutputMsg:
		if msg.gen != m.termGen {
			return m, nil
		}
		m.termRows = msg.rows
		if m.termVT != nil {
			m.termVT.Lock()
			cur := m.termVT.Cursor()
			m.termCursorX, m.termCursorY = cur.X, cur.Y
			m.termCursorOK = m.termVT.CursorVisible()
			m.termVT.Unlock()
		}
		if msg.exited {
			m.msg = m.t("term.exited")
		}
		return m, waitForTermOutput(m.termCh)
	case terminalExitMsg:
		if msg.gen != m.termGen {
			return m, nil
		}
		m.termSession = nil
		if msg.err != nil && !strings.Contains(strings.ToLower(msg.err.Error()), "killed") {
			m.msg = m.t("term.error", msg.err.Error())
		}
	case projSearchMsg:
		m.handleProjSearchMessage(msg)
		return m, waitForProjSearch(m.projSearchCh)
	case projReplMsg:
		m.handleProjReplMessage(msg)
		return m, waitForProjRepl(m.projReplCh)
	case ChatOutputMsg:
		if msg.Gen != m.chatGen {
			return m, nil // stale event from a cancelled/cleared conversation
		}
		switch {
		case msg.Err != nil:
			m.chatBusy = false
			m.chatErr = msg.Err.Error()
			if m.chatCancel != nil {
				m.chatCancel()
				m.chatCancel = nil
			}
			m.rebuildChatRows()
			return m, nil
		case msg.Done:
			m.chatBusy = false
			if m.chatCancel != nil {
				m.chatCancel()
				m.chatCancel = nil
			}
			if len(msg.Tools) > 0 {
				// The model invoked tools; execute them, review any edits, and
				// continue the conversation.
				cmd := m.handleChatToolsDone(m.chatReply, msg.Tools)
				m.chatReply = ""
				m.msg = "tool: " + msg.Tools[0].Name
				m.rebuildChatRows()
				return m, cmd
			}
			var usage *ai.Usage
			if !m.chatTurnUsage.Empty() {
				// A turn with tool rounds reports usage per round, and every
				// round re-sends (and re-bills) the whole context: the sum is
				// the cost of the turn, which is the honest thing to show.
				m.chatSessionUsage = m.chatSessionUsage.Add(m.chatTurnUsage)
				u := m.chatTurnUsage
				usage = &u
				m.chatTurnUsage = ai.Usage{}
			}
			if m.chatReply != "" {
				m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "assistant", Content: m.chatReply, Usage: usage})
				m.chatReply = ""
			}
			m.saveChatThread()
			m.rebuildChatRows()
			return m, nil
		default:
			m.chatReply += msg.Delta
			if msg.HasUsage {
				m.chatTurnUsage = m.chatTurnUsage.Add(msg.Usage)
			}
		}
		m.rebuildChatRows()
		if !msg.Done && msg.Err == nil {
			return m, waitForChatOutput(m.chatCh, m.chatGen)
		}
	case AITestResultMsg:
		return m, m.handleAITestResult(msg)
	case InlineOutputMsg:
		cmd := m.handleInlineOutput(msg)
		return m, cmd
	case FixOutputMsg:
		cmd := m.handleFixOutput(msg)
		return m, cmd
	case GhostOutputMsg:
		cmd := m.handleGhostOutput(msg)
		return m, cmd
	case ghostWaitMsg:
		// The debounce window expired; fire the request it was counting for.
		return m, m.handleGhostWait(msg)
	case AgentRefreshMsg:
		// A delegation may have finished while the chat was parked on it; the
		// queue update is what wakes the conversation back up.
		m.pollSubAgentPark()
		// The queue's durable shape (ids, statuses, errors) is what the store
		// compares; a progress-only refresh writes nothing.
		m.saveAgentStore()
		return m, waitForAgentRefresh(m.agentCh)
	case gitTransferMsg:
		if msg.err != "" {
			m.msg = m.t("git.transfer_error", m.t("git.op_"+msg.op), msg.err)
			return m, nil
		}
		m.msg = m.t("git.transfer_done", m.t("git.op_"+msg.op))
		if m.gitOpen {
			m.refreshGitFiles()
		}
	case gitBlameMsg:
		if msg.err != "" {
			m.msg = m.t("git.blame_error", msg.err)
			m.blameOn = false
			return m, nil
		}
		for i := range m.tabs {
			t := &m.tabs[i]
			abs, _ := filepath.Abs(t.path)
			if abs == msg.path {
				t.blame = msg.lines
				if len(msg.lines) == 0 {
					m.msg = m.t("msg.blame_none")
				} else {
					m.msg = m.t("msg.blame_on", len(msg.lines))
				}
				break
			}
		}
	case tea.MouseClickMsg:
		cmd := m.handleMouseClick(msg)
		return m, cmd
	case tea.MouseWheelMsg:
		cmd := m.handleMouseWheel(msg)
		return m, cmd
	case tea.MouseReleaseMsg:
		m.mouseDown = false
		m.dragColumn = false
		switch {
		case m.dragChat:
			m.dragChat = false
			m.copyToClipboard(m.chatSelectionText())
		case m.dragTerm:
			m.dragTerm = false
			m.copyToClipboard(m.termSelectionText())
		case m.termOpen && msg.Y >= m.termStartRow() && msg.Y < m.termStartRow()+m.termPanelHeight():
			button := 0
			if msg.Button == tea.MouseMiddle {
				button = 1
			} else if msg.Button == tea.MouseRight {
				button = 2
			}
			m.forwardTerminalMouseEvent(button, msg.X, msg.Y-m.termStartRow(), 'm')
		}
	case tea.MouseMotionMsg:
		if m.mouseDown {
			cmd := m.handleMouseMotion(msg)
			return m, cmd
		}
		m.updateStatusHover(msg)
	case tea.PasteMsg:
		if text := msg.String(); text != "" {
			if t := m.cur(); t != nil && t.img != nil {
				// A paste arrives as one message carrying the whole text, so a
				// check written for key presses alone would miss it entirely.
				m.msg = m.t("msg.image_readonly")
			} else if m.termOpen && m.termSession != nil {
				_, _ = m.termSession.Write([]byte(strings.ReplaceAll(text, "\r\n", "\r")))
			} else {
				m.pasteInput(text)
			}
		}
	case tea.KeyPressMsg:
		// The debug flag exists to show the chord the bindings actually
		// matched, not the bytes the terminal sent.
		_, kn := keyOf(msg)
		if debugKeys {
			fmt.Fprintf(os.Stderr, "dmed: key %q\n", kn)
		}
		cmd := m.handleKey(msg)
		if debugKeys {
			m.msg = "k:" + kn
		}
		if cmd != nil {
			return m, cmd
		}
	case lspCompletionMsg:
		if m.complOpen && msg.path == m.cur().path {
			if msg.err != nil {
				m.msg = "lsp: " + msg.err.Error()
				if len(m.complItems) == 0 {
					m.closeCompletion()
				}
			} else {
				m.mergeLSPCompletion(msg.items)
				if len(m.complItems) == 0 {
					m.closeCompletion()
				}
			}
		}
	case lspDiagMsg:
		abs, _ := filepath.Abs(msg.path)
		m.diags[abs] = msg.diags
		return m, waitForLSPDiag(m.diagCh)
	case lspHoverMsg:
		if len(msg.lines) == 0 {
			m.msg = m.t("msg.no_hover")
			return m, nil
		}
		m.hoverText = msg.lines
		m.hoverOpen = true
	case lspRefsMsg:
		m.handleRefsMsg(msg)
	case lspOutlineMsg:
		m.handleOutlineMsg(msg)
	case lspDefinitionMsg:
		if msg.err != nil {
			m.msg = "goto def: " + msg.err.Error()
		} else if msg.loc == nil {
			m.msg = m.t("msg.no_definition")
		} else {
			// A jump the user cannot walk back by hand: record where it starts
			// before moving, here rather than in gotoDefinition, because the
			// reply is async and the caret the user stands on when it lands is
			// the position back has to return to.
			m.pushJump()
			m.focusOrOpen(msg.loc.Path)
			if t := m.cur(); t != nil {
				t.buf.SetCursor(msg.loc.Line, msg.loc.Col)
				t.buf.Deselect()
			}
			m.clampScroll()
		}
	case lspRenameMsg:
		m.handleLspRenameMsg(msg)
	case lspActionsMsg:
		m.handleLspActionsMsg(msg)
	case aiModelsMsg:
		m.handleModelsMsg(msg)
		return m, nil
	case agentUndoMsg:
		m.handleAgentUndoMsg(msg)
	case lspFormatMsg:
		m.handleLspFormatMsg(msg)
	case pluginStoreMsg:
		m.storeLoading = false
		if msg.err != nil {
			m.storeErr = msg.err.Error()
		} else {
			for _, it := range msg.items {
				exists := false
				for _, e := range m.storeItems {
					if e.File == it.File {
						exists = true
						break
					}
				}
				if !exists {
					m.storeItems = append(m.storeItems, it)
				}
			}
		}
	case pluginSourceMsg:
		m.pendingStoreInstall = ""
		if msg.err != nil {
			m.msg = "plugin download error: " + msg.err.Error()
		} else if m.plugins != nil {
			m.installFromSource(msg.file, msg.src)
		}
	case dapEventMsg:
		return m, m.handleDAPEventUpdate(msg)
	case dapStartMsg:
		if msg.gen != m.dapGen {
			// A newer stop/restart superseded this start; drop the late adapter.
			if msg.cl != nil {
				msg.cl.Close()
			}
			return m, nil
		}
		m.dapBusy = false
		if msg.err != nil {
			m.dapRunState = dapIdle
			m.msg = msg.err.Error()
			return m, nil
		}
		m.dapClient = msg.cl
		m.dapSupportsConfigDone = msg.supports
		m.msg = "debug: " + msg.adapter + " attached"
		return m, m.dapLaunchCmd()
	case dapLaunchMsg:
		if msg.gen != m.dapGen {
			return m, nil // stale launch result from a superseded session
		}
		m.dapBusy = false
		m.applyDapBPSyncs(msg.bps)
		if msg.err != nil {
			m.dapRunState = dapIdle
			m.msg = "debug launch: " + msg.err.Error()
			// The adapter never produced a debuggee. Release it so F5 can
			// launch again: leaving the client attached while the state reads
			// idle made the "session already attached" guard swallow every
			// later F5 press.
			return m, m.dapRelease()
		}
		m.dapRunState = dapRunning
		m.msg = "debug: running"
	case dapStepMsg:
		if msg.err != nil {
			m.msg = "debug step: " + msg.err.Error()
		} else {
			m.dapRunState = dapRunning
			m.dapCurPath = ""
			m.dapCurLine = 0
		}
	case dapRefreshMsg:
		m.applyDAPRefresh(msg)
		if m.dapFollowPending {
			m.dapFollowPending = false
			return m, m.dapFollowCmd()
		}
		return m, nil
	case dapFollowMsg:
		return m, m.handleDapFollowMsg()
	case dapEvalMsg:
		m.dapAppendConsole("> " + msg.expr)
		if msg.err != nil {
			m.dapAppendConsole("! " + msg.err.Error())
		} else {
			m.dapAppendConsole("= " + msg.out)
		}
	case dapBPSyncMsg:
		if msg.err != nil {
			m.msg = "debug breakpoints: " + msg.err.Error()
			return m, nil
		}
		m.applyDapBPSyncs([]dapBPSyncMsg{msg})
	}
	m.clampScroll()
	// The animation chain asks once per message: something may have brought a
	// picture on screen, so the next frame gets scheduled from here — not from
	// every branch of this switch.
	return m, m.animCmd()
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
	if t.img != nil {
		// A picture's pane offset is its own scroll through the art, clamped by
		// scrollImage. The buffer behind it is one empty line, and letting its
		// geometry clamp here would pin every picture to the top of the pane
		// on the next message that arrives.
		return
	}
	h := m.paneViewHeight(m.activePane)
	cur := t.buf.CurLine()
	tabW := m.tabStop(t)
	// Folding: rows are display rows (folded blocks collapsed to one hint
	// line). A cursor that ended up on a hidden line — buffer edits shifting
	// things around — unfolds the block that hides it first.
	d := m.dispRows(m.activePane, m.paneContentWidth(m.activePane))
	if dispIndexOfLine(d, cur) < 0 {
		t.unfoldThrough(cur, tabW)
		d = m.dispRows(m.activePane, m.paneContentWidth(m.activePane))
	}
	if p.wordWrap {
		expCol := visCol(t.buf.LineAt(cur), t.buf.Col(), tabW)
		row := dispCursorRow(d, cur, expCol)
		if row < p.offsetY {
			p.offsetY = row
		}
		if row >= p.offsetY+h {
			p.offsetY = row - h + 1
		}
		if maxOff := len(d) - h; p.offsetY > maxOff {
			p.offsetY = maxOff
		}
		if p.offsetY < 0 {
			p.offsetY = 0
		}
		p.offsetX = 0
		return
	}
	if h > 0 && len(d) > 0 {
		row := dispCursorRow(d, cur, 0)
		if row < p.offsetY {
			p.offsetY = row
		}
		if row >= p.offsetY+h {
			p.offsetY = row - h + 1
		}
		if maxOff := len(d) - h; p.offsetY > maxOff {
			p.offsetY = maxOff
		}
		if p.offsetY < 0 {
			p.offsetY = 0
		}
	}
	w := m.paneContentWidth(m.activePane)
	if w <= 0 {
		return
	}
	x := visCol(t.buf.LineAt(cur), t.buf.Col(), tabW)
	if x < p.offsetX {
		p.offsetX = x
	}
	if x >= p.offsetX+w {
		p.offsetX = x - w + 1
	}
}

// toggleComment comments or uncomments the current line(s) using the comment
// syntax of the active file type (from the syntax highlighter's lexer), then
// moves the cursor to the next line.
func (m *Model) toggleComment() {
	t := m.cur()
	prefix, suffix := syntax.CommentTokens(t.path, t.buf.Text())
	if prefix == "" {
		m.msg = m.t("msg.no_comment")
		return
	}
	t.buf.ToggleComment(prefix, suffix)
	m.msg = ""
	t.buf.MoveDown()
	m.clampScroll()
}

func (m *Model) toggleWordWrap() {
	p := m.curPane()
	p.wordWrap = !p.wordWrap
	if p.wordWrap {
		p.offsetX = 0
	}
	m.clampScroll()
	if p.wordWrap {
		m.msg = m.t("msg.word_wrap_on")
	} else {
		m.msg = m.t("msg.word_wrap_off")
	}
}

func (m *Model) jumpHunk(dir int) {
	t := m.cur()
	if t.path == "" {
		return
	}
	diff := t.getDiff()
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
	m.openPalette(false)
}

// startSearchEverywhere opens the palette in the JetBrains "search
// everywhere" mode: commands, project files and settings in one list.
func (m *Model) startSearchEverywhere() {
	m.openPalette(true)
}

func (m *Model) openPalette(all bool) {
	m.paletteOpen = true
	m.paletteQ = nil
	m.paletteSel = 0
	m.paletteOffset = 0
	m.paletteAll = all
	if all {
		// The file list is collected once, like the file finder does: the
		// query only filters it.
		m.paletteFiles = collectFiles(m.baseDir(), m.cfg.Editor.SkippedDirs)
	}
	// Unfocus all panels so the palette receives keystrokes (the palette
	// is lower priority than chat/agent/git in the mode-guard chain).
	m.chatFocus = false
	m.agentFocus = false
	m.gitFocus = false
	m.treeFocus = false
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
func (m *Model) handleLangChooser(s string) tea.Cmd {
	langs := i18n.Supported()
	switch m.paneCmd("lang", s) {
	case "lang.close":
		m.langChooserOpen = false
	case "lang.run":
		if m.langChooserSel >= 0 && m.langChooserSel < len(langs) {
			m.setLang(langs[m.langChooserSel].Code)
		}
		m.langChooserOpen = false
	case "lang.up":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel - 1 + len(langs)) % len(langs)
		}
	case "lang.down":
		if len(langs) > 0 {
			m.langChooserSel = (m.langChooserSel + 1) % len(langs)
		}
	}
	return nil
}

func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.mouseDown {
		return nil
	}
	if m.dragChat {
		if row, col, ok := m.chatSelHit(msg.Y, msg.X); ok {
			m.extendChatSelection(row, col)
		}
		return nil
	}
	if m.dragTerm {
		row := msg.Y - m.termStartRow() - 1
		if row >= 0 && row < len(m.termRows) {
			m.extendTermSelection(row, msg.X)
		}
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

	editorRow := y - 1
	ln, rawCol, d := m.clickPosToLineCol(m.activePane, editorRow, x)
	if d.summary {
		// Drag started on a fold summary: expand the block and drag from its
		// start line.
		t := m.cur()
		if t.toggleFoldAt(d.ln, m.tabStop(t)) {
			m.clampScroll()
		}
		ln = d.ln
		rawCol = 0
	}

	if m.dragColumn {
		m.dragColumnTo(ln, rawCol)
		return nil
	}
	m.cur().buf.DragSelect(ln, rawCol)
	return nil
}

// dragColumnTo extends the Shift+drag column selection to the pointer. The
// buffer grows the rectangle's corner toward it; the focus resets match a
// plain click's.
func (m *Model) dragColumnTo(ln, rawCol int) {
	m.treeFocus = false
	m.agentFocus = false
	m.gitFocus = false
	m.gitDiffFocused = false
	m.chatFocus = false
	m.termFocus = false
	m.cur().buf.GrowColumnTo(ln, rawCol)
}

// updateStatusHover tracks which status-bar icon the cursor is over so the
// hovered cell can highlight and a callout can be drawn. Called for motion
// events with no button pressed (requires MouseModeAllMotion).
func (m *Model) updateStatusHover(msg tea.MouseMotionMsg) {
	m.updateSplitHover(msg)
	m.updateChatHover(msg)
	if m.statusIconsVisible() && msg.Y == m.statusBarRow() {
		m.hoverIcon = m.statusIconAt(msg.X)
		return
	}
	m.hoverIcon = actNone
}

// updateChatHover tracks which chat panel button the cursor is over so the
// hovered cell can highlight, mirroring the status-bar icon strip.
func (m *Model) updateChatHover(msg tea.MouseMotionMsg) {
	if m.chatOpen && msg.Y == m.chatButtonsRow() {
		m.hoverChatBtn = m.chatButtonAt(msg.X)
		return
	}
	m.hoverChatBtn = chatBtnNone
}

// toggleDebugPanel mirrors the Ctrl+Alt+D shortcut so the status-bar icon and
// the key behave identically.
func (m *Model) toggleDebugPanel() tea.Cmd {
	m.dapOpen = !m.dapOpen
	if m.dapOpen {
		m.termOpen = false
		m.msg = m.t("msg.debug_panel_opened")
	}
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
// scroll offsets, word wrap, and split layout.
func (m Model) cursorScreenPos() (int, int) {
	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	curLine := t.buf.CurLine()
	curRawCol := t.buf.Col()

	// Expand cursor column for tabs.
	raw := t.buf.LineAt(curLine)
	expCol := 0
	tabW := m.tabStop(t)
	for i := 0; i < curRawCol && i < len(raw); i++ {
		if raw[i] == '\t' {
			expCol += tabW
		} else {
			expCol++
		}
	}

	gw := m.gutterWidthForTab(t)
	leftW := m.leftRailWidth()

	screenRow, segStart := curLine, 0
	d := m.dispRows(m.activePane, m.paneContentWidth(m.activePane))
	if len(d) == 0 {
		return 0, 0
	}
	row := dispCursorRow(d, curLine, expCol)
	screenRow = row
	segStart = d[row].expStart

	// X position within the pane content area.
	paneX := gw + (expCol - (p.offsetX + segStart))

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
	screenY := 1 + (screenRow - p.offsetY)

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
