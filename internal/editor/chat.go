package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/ask"
	"dmed/internal/debug"
	"dmed/internal/vcs"

	"github.com/atotto/clipboard"
)

// Right-side AI chat panel backed by a local Ollama server (Alt+A).
// While the panel is open it owns keyboard focus; Esc closes it. A running
// stream keeps appending while the panel is hidden and is cancelled on quit.
//
// The chat model can call native tools (READ/SEARCH/RUN/EDIT) via structured
// function calling. EDIT proposals are shown as a side-by-side diff and only
// applied after the user accepts (y) or rejected (n), going through the agent
// Applier so the write is atomic and stale-proof. This mirrors opencode's
// "propose then review" flow instead of parsing fragile text markers.

var (
	chatUserLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	chatAILabelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("176")).Bold(true)
	chatUserTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	chatAITextStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	chatToolLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	chatToolTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))
	chatTodoLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108")).Bold(true)
	chatTodoTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	chatTodoDoneStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	chatNoticeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	chatUserBubbleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236"))
	chatScrollThumbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("61"))
	chatBtnStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("240"))
	chatSelStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("61"))
)

type chatRow struct {
	kind string // "label-you" | "user" | "label-ai" | "ai" | "ai-code" | "ai-head" | "ai-quote" | "tool" | "label-todo" | "todo" | "todo-done" | "notice" | "hint" | "err"
	text string
	rich []mdSeg
}

type chatEvent struct {
	delta string
	err   error
	done  bool
	gen   uint64
	tools []ai.ToolCall
}

// ChatOutputMsg delivers one event from the streaming chat goroutine.
type ChatOutputMsg struct {
	Delta string
	Err   error
	Done  bool
	Gen   uint64 // conversation generation, stale for cleared threads
	Tools []ai.ToolCall
}

func waitForChatOutput(ch <-chan chatEvent, gen uint64) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ChatOutputMsg{Done: true, Gen: gen}
		}
		return ChatOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done, Gen: gen, Tools: ev.tools}
	}
}

func (m Model) chatPanelWidth() int {
	pct := m.cfg.UI.ChatWidthPct
	if pct <= 0 {
		pct = 40
	}
	w := m.width * pct / 100
	if w < 24 {
		w = 24
	}
	if w > 48 {
		w = 48
	}
	return w
}

func (m Model) chatInnerWidth() int {
	w := m.chatPanelWidth() - 3
	if w < 1 {
		w = 1
	}
	return w
}

// chatInputHeight returns the number of terminal rows the current input
// occupies after word-wrapping to the available width.
func (m Model) chatInputHeight() int {
	w := m.chatPanelWidth() - 4 // " ❯ " (3) + cursor (1)
	if w < 1 {
		w = 1
	}
	n := len(wrapRunes(string(m.chatIn), w))
	if n < 1 {
		n = 1
	}
	return n
}

func (m Model) rightRailWidth() int {
	if m.chatOpen {
		return m.chatPanelWidth()
	}
	return 0
}

func (m *Model) toggleChat() tea.Cmd {
	m.chatOpen = !m.chatOpen
	m.chatFocus = m.chatOpen
	m.gitFocus = false
	m.msg = ""
	if m.chatOpen && !m.chatHistLoaded {
		m.loadChatPanelHistory()
	}
	if m.chatOpen {
		// The chat may delegate a task to a sub-agent, which needs the agent
		// queue; wire it now so the model is offered SUB_AGENT right away
		// instead of after the user happens to open the agent panel.
		m.ensureAgent()
	}
	if m.chatOpen && m.chatModel == "" {
		// Resolve the model before the provider is built: building first would
		// cache a client with an empty model, and the next turn would send
		// model: "" — Pollinations answers that with "Model not found".
		m.pickChatModel()
	}
	m.ai = m.aiProvider(m.chatModel)
	return nil
}

// pickChatModel resolves the model once: DMED_MODEL wins, otherwise the
// configured model, otherwise the first model reported by the server. A fresh
// install with no working provider falls back to a keyless one (and says so)
// rather than showing a dead panel. The cached provider is rebuilt whenever the
// model changes, because a client built earlier (e.g. in toggleChat before the
// model was known) would keep sending model: "" for every later turn.
func (m *Model) pickChatModel() {
	if m.cfg.AI.Model != "" {
		m.chatModel = m.cfg.AI.Model
		m.ai = m.aiProvider(m.chatModel)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	prov := m.aiProvider(m.chatModel)
	models, err := prov.Models(ctx)
	switch {
	case err != nil:
		if m.tryFreeFallback() {
			m.msg = m.t("chat.fallback_status")
			m.rebuildChatRows()
			return
		}
		m.msg = "provider offline (" + err.Error() + ")"
	case len(models) == 0:
		m.msg = "no models available"
	default:
		m.chatModel = models[0]
		// Rebuild for the freshly picked model.
		m.ai = m.aiProvider(m.chatModel)
	}
}

func (m *Model) handleChat(msg tea.KeyPressMsg) tea.Cmd {
	s := msg.String()
	// While a tool call is parked for a human decision (e.g. a RUN command with
	// allow_run = ask) the input is parked too: the decision is fed back as the
	// tool result and the loop continues.
	if m.chatPark != nil {
		return m.handleChatParkKey(msg)
	}
	switch s {
	case "esc":
		// While streaming, Esc stops the generation instead of closing the chat.
		if m.chatBusy {
			return m.chatStop()
		}
		m.closeChat()
	case "ctrl+q", "ctrl+c":
		// Ctrl+C stops a running stream; if idle it closes the chat.
		if m.chatBusy {
			return m.chatStop()
		}
		m.closeChat()
	case "enter":
		return m.chatSubmit()
	case "backspace":
		if n := len(m.chatIn); n > 0 {
			m.chatIn = m.chatIn[:n-1]
		}
	case "pgup":
		m.chatScroll += m.paneViewHeight(m.activePane) / 2
		m.clampChatScroll()
	case "pgdown":
		m.chatScroll -= m.paneViewHeight(m.activePane) / 2
		m.clampChatScroll()
	case "up": // recall a previously sent prompt
		m.chatHistoryPrev()
	case "down": // back toward the newest prompt / draft
		m.chatHistoryNext()
	case "ctrl+n": // newer thread (from the newest one: a new thread)
		m.switchChatThread(-1)
	case "ctrl+u": // start a new conversation thread
		m.newChatThread()
	case "ctrl+l": // clear all chat history (press twice to confirm)
		m.armChatClear()
	case "ctrl+y": // copy the last error / AI reply to the system clipboard
		m.copyChatLast()
	case "r", "R": // retry: re-send the last user prompt (only when input is empty)
		if len(m.chatIn) == 0 && !m.chatBusy && !m.chatReviewMode {
			return m.chatRetry()
		}
		fallthrough
	default:
		m.chatClearArm = false
		if len(msg.Text) > 0 {
			m.chatIn = append(m.chatIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// closeChat hides the rail; shared by Esc, Ctrl+C/Ctrl+Q and the close button.
func (m *Model) closeChat() {
	m.chatOpen = false
	m.chatFocus = false
	m.chatReviewMode = false
	m.chatClearArm = false
	m.msg = ""
}

// newChatThread starts a fresh conversation, saving the current one; shared
// by Ctrl+U and the new-thread button.
func (m *Model) newChatThread() {
	m.saveChatThread()
	m.chatThreadPos = -1
	m.chatClearArm = false
	m.resetChatConversation()
}

// ---- Chat action buttons ---------------------------------------------------

// The hint bar under the transcript is a row of pseudo-buttons in the same
// clickable-cell style as the status-bar icon strip: each renders as
// " label ", highlights on hover and reacts to a mouse click.

type chatButtonAction int

const (
	chatBtnNone chatButtonAction = iota
	chatBtnNew
	chatBtnCopy
	chatBtnClear
	chatBtnClose
)

var chatButtons = []struct {
	act chatButtonAction
	tip string // i18n key for the hover callout
}{
	{chatBtnNew, "chat.tip_new"},
	{chatBtnCopy, "chat.tip_copy"},
	{chatBtnClear, "chat.tip_clear"},
	{chatBtnClose, "chat.tip_close"},
}

type chatBtnCell struct {
	act   chatButtonAction
	start int // column inside the panel
	w     int
}

// chatButtonCells lays the buttons out right to left so the strip hugs the
// right edge of the panel, one column clear of the border; buttons that
// would not fit are dropped, narrow rails keep the rightmost ones.
func (m Model) chatButtonCells() []chatBtnCell {
	w := m.chatPanelWidth()
	cells := make([]chatBtnCell, 0, len(chatButtons))
	pos := w - 1
	for i := len(chatButtons) - 1; i >= 0; i-- {
		b := chatButtons[i]
		cw := lipgloss.Width(" " + m.chatButtonGlyph(b.act) + " ")
		if pos-cw < chatBodyPad {
			break
		}
		cells = append(cells, chatBtnCell{b.act, pos - cw, cw})
		pos -= cw
	}
	for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
		cells[i], cells[j] = cells[j], cells[i]
	}
	return cells
}

// chatButtonGlyph returns the icon cell content for a chat button.
func (m Model) chatButtonGlyph(a chatButtonAction) string {
	switch a {
	case chatBtnNew:
		return m.g.btnNew
	case chatBtnCopy:
		return m.g.btnCopy
	case chatBtnClear:
		return m.g.btnClear
	case chatBtnClose:
		return m.g.cross
	}
	return "?"
}

// chatTip returns the hover callout text (label + shortcut) for a button.
func (m Model) chatTip(a chatButtonAction) string {
	for _, b := range chatButtons {
		if b.act == a {
			return m.t(b.tip)
		}
	}
	return ""
}

// chatButtonsRow returns the screen row the button strip occupies, or -1 when
// the panel is too short to show it. The panel starts at screen row 1 (under
// the tab bar), so its strip sits one row lower than the panel-local index.
func (m Model) chatButtonsRow() int {
	h := m.viewHeight()
	if h < 5 {
		return -1
	}
	return h - m.chatInputHeight()
}

// chatButtonAt maps a screen column on the button row to an action.
func (m Model) chatButtonAt(x int) chatButtonAction {
	local := x - (m.width - m.rightRailWidth())
	for _, c := range m.chatButtonCells() {
		if local >= c.start && local < c.start+c.w {
			return c.act
		}
	}
	return chatBtnNone
}

// activateChatButton runs the action behind a clicked chat button.
func (m *Model) activateChatButton(a chatButtonAction) tea.Cmd {
	if m.chatPark != nil {
		return nil // a parked decision owns the input; keys and buttons wait
	}
	switch a {
	case chatBtnNew:
		m.newChatThread()
	case chatBtnCopy:
		m.copyChatLast()
	case chatBtnClear:
		m.armChatClear()
	case chatBtnClose:
		m.closeChat()
	}
	return nil
}

// chatButtonsString renders the button strip as one row of raised icon
// cells right-aligned over an unstyled background so the strip does not
// blend into the input line below it.
func (m Model) chatButtonsString() string {
	w := m.chatPanelWidth()
	var b strings.Builder
	pos := 0
	for _, c := range m.chatButtonCells() {
		b.WriteString(strings.Repeat(" ", c.start-pos))
		pos = c.start
		st := chatBtnStyle
		if m.hoverChatBtn == c.act {
			st = statusHiStyle
		}
		b.WriteString(st.Render(" " + m.chatButtonGlyph(c.act) + " "))
		pos += c.w
	}
	b.WriteString(strings.Repeat(" ", maxInt(0, w-pos)))
	return b.String()
}

// overlayChatTooltip draws a one-row floating callout directly above the chat
// button strip, anchored to the hovered button: label plus key combination,
// mirroring overlayStatusTooltip. The rail is only composed in the plain
// editor mode, so the overlay must not fire in full-screen modes.
func (m Model) overlayChatTooltip(rows []string) []string {
	if m.hoverChatBtn == chatBtnNone || !m.chatOpen {
		return rows
	}
	switch {
	case m.diffViewOpen, m.aiReviewMode, m.aiFixReviewMode, m.agentReviewMode,
		m.chatReviewMode, m.conflictOpen, m.aiCfgOpen, m.dapCfgOpen, m.helpOpen:
		return rows
	}
	row := m.chatButtonsRow() - 1
	if row < 1 || row >= len(rows) {
		return rows
	}
	tip := m.chatTip(m.hoverChatBtn)
	if tip == "" {
		return rows
	}
	text := statusHiStyle.Render(" " + tip + " ")
	w := lipgloss.Width(text)
	x := -1
	for _, c := range m.chatButtonCells() {
		if c.act == m.hoverChatBtn {
			x = m.width - m.rightRailWidth() + c.start
		}
	}
	if x < 0 {
		return rows
	}
	if x+w > m.width {
		x = m.width - w
	}
	if x < 0 {
		x = 0
	}
	fill := m.width - x - w
	if fill < 0 {
		fill = 0
	}
	rows[row] = strings.Repeat(" ", x) + text + strings.Repeat(" ", fill)
	return rows
}
// copyChatLast puts the most useful chunk of the transcript into the system
// clipboard: the last error if there is one, otherwise the last assistant
// reply. It is the way to grab AI output (error text included) since the
// transcript itself is not mouse-selectable inside the TUI.
func (m *Model) copyChatLast() {
	text := m.chatErr
	if text == "" {
		for i := len(m.chatMsgs) - 1; i >= 0; i-- {
			if m.chatMsgs[i].Role == "assistant" && m.chatMsgs[i].Content != "" {
				text = m.chatMsgs[i].Content
				break
			}
		}
	}
	if text == "" {
		m.msg = m.t("chat.nothing_copy")
		return
	}
	m.clipboard = text
	if err := clipboard.WriteAll(text); err != nil {
		m.msg = "clipboard: " + err.Error()
		return
	}
	m.msg = m.t("msg.copied")
}

// chatSubmit sends the current input to the AI. It returns a tea.Cmd that
// reads the stream only if a request actually went out; when the input is
// empty or a stream is already running it returns nil and leaves the input
// intact.
func (m *Model) chatSubmit() tea.Cmd {
	text := strings.TrimSpace(string(m.chatIn))
	if text == "" || m.chatBusy {
		return nil
	}
	m.chatIn = nil
	if m.chatModel == "" {
		m.pickChatModel()
		if m.chatModel == "" {
			m.chatErr = "no model available — Ctrl+P → 'AI: Preferences' to set one up, or run: ollama pull llama3.2"
			m.rebuildChatRows()
			return nil
		}
	}

	m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "user", Content: text})
	m.recordChatPrompt(text)
	m.saveChatThread()
	m.chatReply = ""
	m.chatErr = ""
	m.chatBusy = true
	m.chatScroll = 0
	m.chatToolRound = 0
	m.rebuildChatRows()

	return m.startChatTurn()
}

// chatRetry re-sends the most recent user prompt, discarding the assistant
// reply (if any) that followed it. This lets the user rerun a request after
// stopping a stream or correcting analysis without retyping the prompt.
func (m *Model) chatRetry() tea.Cmd {
	var prompt string
	for i := len(m.chatMsgs) - 1; i >= 0; i-- {
		if m.chatMsgs[i].Role == "user" {
			prompt = m.chatMsgs[i].Content
			m.chatMsgs = m.chatMsgs[:i+1]
			break
		}
	}
	if prompt == "" {
		m.msg = "nothing to retry"
		return nil
	}
	m.chatReply = ""
	m.chatErr = ""
	m.chatBusy = true
	m.chatScroll = 0
	m.chatToolRound = 0
	m.rebuildChatRows()
	return m.startChatTurn()
}

// chatStop aborts an in-flight stream. It bumps the conversation generation so
// any buffered events from the dying goroutine are dropped as stale, cancels the
// provider call, and returns the chat to an idle state. Any partial text already
// streamed into chatReply is kept in the transcript so the user sees what the
// model produced before they interrupted.
func (m *Model) chatStop() tea.Cmd {
	if !m.chatBusy {
		return nil
	}
	m.chatGen++
	if m.chatCancel != nil {
		m.chatCancel()
		m.chatCancel = nil
	}
	m.chatBusy = false
	m.chatCh = nil
	if m.chatReply != "" {
		m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "assistant", Content: m.chatReply})
		m.chatReply = ""
	}
	m.saveChatThread()
	m.rebuildChatRows()
	m.msg = m.t("chat.stopped")
	return nil
}

// startChatTurn kicks off a streaming turn with the full conversation plus the
// native tool definitions. When the stream finishes, tool calls (if any) are
// delivered together with the Done marker on the channel.
func (m *Model) startChatTurn() tea.Cmd {
	if m.ai == nil {
		// The settings wizard nils the cached provider whenever the [ai]
		// config changes; the next turn must rebuild it here, or the stream
		// goroutine below dies on a nil interface call.
		m.ai = m.aiProvider(m.chatModel)
	}
	msgs := m.chatRequestMessages()
	if m.chatCancel != nil {
		m.chatCancel()
	}
	m.chatGen++
	gen := m.chatGen
	m.chatBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	m.chatCancel = cancel

	ch := make(chan chatEvent, 64)
	m.chatCh = ch
	go debug.CapturePanicReport(func() {
		defer close(ch)
		var tools []ai.ToolCall
		err := m.ai.ChatStream(ctx, ai.Request{Messages: msgs, Tools: m.activeChatToolDefs(), Options: m.aiRequestOptions()}, ai.Handler{
			Delta: func(d string) {
				select {
				case ch <- chatEvent{delta: d, gen: gen}:
				case <-ctx.Done():
				}
			},
			ToolCalls: func(calls []ai.ToolCall) {
				tools = append(tools, calls...)
			},
		})
		if err != nil {
			select {
			case ch <- chatEvent{err: err, gen: gen}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case ch <- chatEvent{done: true, tools: tools}:
		case <-ctx.Done():
		}
	})
	return waitForChatOutput(ch, gen)
}

// chatRequestMessages snapshots the conversation with a system prompt that
// carries the active file (or selection) as context. The file is embedded only
// on the first turn of a thread — subsequent turns already carry it (and the
// model's own tool results) in the transcript, so re-sending it on every turn
// would just bloat the context window for no benefit.
func (m *Model) chatRequestMessages() []ai.Message {
	var b strings.Builder
	b.WriteString(m.cfg.AI.SystemPrompt)
	if notice := m.modeNotice(); notice != "" {
		// Plan mode is enforced by which tools exist, but the model still has to
		// be told, or it will keep trying to edit and reporting failure.
		b.WriteString("\n\n" + notice)
	}
	if len(m.chatMsgs) == 0 {
		if t := m.cur(); t != nil && t.path != "" {
			b.WriteString("\n\nCurrent file: " + t.path + "\n```")
			content := t.buf.Text()
			truncated := false
			if r := []rune(content); len(r) > m.cfg.AI.ContextMax {
				content = string(r[:m.cfg.AI.ContextMax])
				truncated = true
			}
			b.WriteString(content + "\n```")
			if truncated {
				b.WriteString("\n(file truncated for context)")
			}
			if sel := t.buf.SelectedText(); sel != "" {
				sr := []rune(sel)
				if len(sr) > m.cfg.AI.ContextMax {
					sr = sr[:m.cfg.AI.ContextMax]
				}
				b.WriteString("\n\nSelected text:\n" + string(sr))
			}
		}
	}
	msgs := make([]ai.Message, 0, len(m.chatMsgs)+1)
	trimmed := m.trimChatHistory()
	msgs = append(msgs, ai.Message{Role: "system", Content: b.String()})
	msgs = append(msgs, trimmed...)
	return msgs
}

// estMsgSize is a rough per-message size used for budget trimming.
func estMsgSize(msg ai.Message) int {
	return len(msg.Content) + len(msg.ToolCallID) + len(msg.ToolName) + 32
}

// chatHistoryBudget bounds the conversation sent to the model.
func (m *Model) chatHistoryBudget() int {
	b := m.cfg.AI.ContextMax
	if b <= 0 {
		b = 6000
	}
	return b
}

// trimChatHistory applies a token budget to the stored transcript so a long
// session cannot silently overflow a small local model's context window. It
// never mutates the transcript (the on-screen history and threads are kept);
// it only trims what gets sent. Oldest messages are dropped first and older
// tool results are condensed into a one-line placeholder, while a window of
// the newest turns is always kept whole.
func (m *Model) trimChatHistory() []ai.Message {
	const keepLast = 8
	const condenseAfter = 4 // keep this many newest tool results verbatim
	const condenseLen = 512

	budget := m.chatHistoryBudget()
	hist := m.chatMsgs

	var tally int
	for _, msg := range hist {
		tally += estMsgSize(msg)
	}

	// Drop whole oldest messages until we fit the budget (but always keep at
	// least the newest keepLast so the model sees the recent conversation).
	drop := 0
	for drop < len(hist)-keepLast && tally > budget {
		tally -= estMsgSize(hist[drop])
		drop++
	}
	window := hist[drop:]

	// Condense older READ/RUN tool dumps that are large.
	out := make([]ai.Message, 0, len(window))
	for i, msg := range window {
		m2 := msg
		if msg.Role == "tool" && i < len(window)-condenseAfter && len(msg.Content) > condenseLen {
			m2.Content = fmt.Sprintf("[tool result truncated: %d chars from %s]", len(msg.Content), msg.ToolName)
		}
		out = append(out, m2)
	}
	return out
}

const maxChatToolRounds = 6

// chatToolCap returns the configured tool-loop depth (falling back to the
// built-in cap of 6 when unset).
func (m *Model) chatToolCap() int {
	if m.cfg.AI.ToolRounds > 0 {
		return m.cfg.AI.ToolRounds
	}
	return maxChatToolRounds
}

// chatEditTrack maps one proposed EDIT to its slot in the pending results so
// the accept/reject decision can amend the message the model sees.
type chatEditTrack struct {
	resultIdx int
	label     string
}

// toolPark is one parked tool call waiting for a human decision, together with
// the slot in the pending results whose placeholder gets replaced when the
// decision is made. Parks are queued rather than handled one at a time inline
// so a single round may park on several commands without losing any of them.
type toolPark struct {
	park      Park
	resultIdx int
}

// handleChatToolsDone runs a batch of native tool calls after a stream ends.
// Non-edit results are finalised immediately; EDIT proposals pause the loop
// for a side-by-side diff review. It returns a tea.Cmd that continues the
// conversation, or nil when waiting on human review or a parked decision.
func (m *Model) handleChatToolsDone(content string, tools []ai.ToolCall) tea.Cmd {
	before := snapshotFiles(m.root)
	results := make([]ai.Message, 0, len(tools))
	var pending []agent.Change
	var tracks []chatEditTrack
	var parks []toolPark
	for _, tc := range tools {
		res := m.execChatTool(tc)
		if res.Change != nil {
			pending = append(pending, *res.Change)
			tracks = append(tracks, chatEditTrack{resultIdx: len(results), label: shortenPath(m.baseDir(), res.Change.Path)})
		}
		if res.Park.Kind != ParkNone {
			parks = append(parks, toolPark{park: res.Park, resultIdx: len(results)})
		}
		results = append(results, ai.Message{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Content: res.Text})
	}

	// Open every file the tools created or rewrote (e.g. by RUN) in tabs and
	// summarize them in the transcript. EDIT proposals are still pending human
	// review, so they open when accepted (see acceptChatReview).
	m.openTouchedFiles(before, results)

	m.chatPendingAssistant = ai.Message{Role: "assistant", Content: content, ToolCalls: tools}
	m.chatPendingResults = results
	m.chatPendingChanges = pending
	m.chatEditTracks = tracks
	m.chatParks = parks
	m.rebuildChatRows()

	if m.startNextPark() {
		return nil
	}
	if len(pending) > 0 {
		m.startChatReview()
		return nil
	}
	return m.finalizeChatTools()
}

// startNextPark activates the next parked tool call (if any) and puts the
// decision in front of the user. It reports whether the loop is now parked.
// This is the single place that suspends the tool loop, so every park kind
// behaves the same way in the transcript, in the status line and in key
// handling.
func (m *Model) startNextPark() bool {
	if len(m.chatParks) == 0 {
		m.chatPark = nil
		return false
	}
	p := m.chatParks[0]
	m.chatParks = m.chatParks[1:]
	m.chatPark = &p
	switch p.park.Kind {
	case ParkRunConfirm:
		m.msg = "RUN: " + p.park.Text + "  (y run / n deny)"
		return true
	case ParkAskUser:
		m.startAsk(p.park.Ask)
		return true
	case ParkSwitchMode:
		m.msg = m.t("chat.switch_status")
		return true
	case ParkSubAgent:
		// A delegation is already running in the background; the status line
		// shows the wait and Esc cancels it.
		m.msg = m.t("chat.subagent_status")
		return true
	default:
		m.chatPark = nil
		return false
	}
}

// startAsk puts a question on screen: the chat is opened (a question the user
// cannot see is a hang, not a decision), and the input is cleared so the answer
// starts from nothing. The chosen suggestion defaults to the first one.
func (m *Model) startAsk(req *ask.Request) {
	m.askReq = req
	m.askIn = nil
	m.askSel = 0
	m.chatOpen = true
	m.chatFocus = true
	m.msg = m.t("chat.ask_status")
}

// finishChatRunConfirm resolves a parked RUN confirmation: true executes the
// held command via runCommand (capped output), false records a decline. The
// placeholder in the pending results is replaced with the real outcome so the
// model sees exactly what happened, then the loop continues (with the next
// parked call, if any, or with the next turn).
func (m *Model) finishChatRunConfirm(run bool) tea.Cmd {
	park := m.chatPark
	m.chatPark = nil
	if park == nil {
		return m.finalizeChatTools()
	}
	cmd := park.park.Text
	var out string
	if run {
		out = runCommand(m.root, cmd)
	} else {
		out = "[RUN not approved by user] " + cmd
	}
	resolveParkedResult(m.chatPendingResults, park.resultIdx, out)
	m.msg = ""
	m.rebuildChatRows()
	if m.startNextPark() {
		return nil
	}
	return m.finalizeChatTools()
}

// clearChatParks drops every parked decision and the active one. Used when the
// conversation is reset or a new thread is loaded, so a stale park can never be
// resumed into an unrelated conversation.
func (m *Model) clearChatParks() {
	m.chatPark = nil
	m.chatParks = nil
}

// openTouchedFiles diffs on-disk snapshots taken before and after a tool round
// and opens every file a tool created or rewrote in a tab (binary/build
// artifacts are skipped), so AI's work is immediately visible. An "AI FILES"
// summary is appended to the last tool result so it shows in the transcript.
func (m *Model) openTouchedFiles(before map[string]fileState, results []ai.Message) {
	var created, modified []string
	for p, cur := range snapshotFiles(m.root) {
		prev, existed := before[p]
		if existed && prev.size == cur.size && prev.mod == cur.mod {
			continue
		}
		if !isPlausibleText(p) {
			continue
		}
		label := shortenPath(m.baseDir(), p)
		if existed {
			modified = append(modified, label)
		} else {
			created = append(created, label)
		}
		m.openAiFile(label, true)
	}
	if len(created) == 0 && len(modified) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n=== AI FILES ===")
	for _, p := range created {
		b.WriteString("\ncreated: " + p)
	}
	for _, p := range modified {
		b.WriteString("\nmodified: " + p)
	}
	if n := len(results); n > 0 {
		results[n-1].Content += b.String()
	}
}

// handleChatParkKey routes keys while the tool loop is parked. Every park kind
// gets its own decision here, and keys the active decision does not understand
// are ignored so a stray keystroke cannot approve anything.
func (m *Model) handleChatParkKey(msg tea.KeyPressMsg) tea.Cmd {
	park := m.chatPark
	if park == nil {
		return nil
	}
	switch park.park.Kind {
	case ParkRunConfirm:
		switch gitKeyName(msg) {
		case "y", "enter":
			return m.finishChatRunConfirm(true)
		case "n", "esc", "r":
			return m.finishChatRunConfirm(false)
		}
		return nil // ignore other keys while parked
	case ParkAskUser:
		return m.handleAskKey(msg)
	case ParkSwitchMode:
		switch gitKeyName(msg) {
		case "y", "enter":
			return m.finishChatSwitchMode(true)
		case "n", "esc", "r":
			return m.finishChatSwitchMode(false)
		}
		return nil
	case ParkSubAgent:
		// Nothing to answer: the loop resumes by itself when the task ends.
		// Esc is the escape hatch, or the wait could last minutes.
		if gitKeyName(msg) == "esc" {
			m.cancelParkedSubAgent()
		}
		return nil
	default:
		return nil
	}
}

// finalizeChatTools commits the pending assistant + tool-result messages into
// the conversation and starts the next turn, capping the tool loop depth.
func (m *Model) finalizeChatTools() tea.Cmd {
	m.chatMsgs = append(m.chatMsgs, m.chatPendingAssistant)
	m.chatMsgs = append(m.chatMsgs, m.chatPendingResults...)
	m.chatPendingAssistant = ai.Message{}
	m.chatPendingResults = nil
	m.chatPendingChanges = nil
	m.chatEditTracks = nil
	m.clearChatParks()
	m.chatToolRound++
	m.saveChatThread()
	m.rebuildChatRows()

	if m.chatToolRound > m.chatToolCap() {
		m.chatErr = "tool loop exceeded max iterations"
		m.rebuildChatRows()
		return nil
	}
	if m.chatToolRound+1 >= m.chatToolCap() {
		// Last allowed round: nudge the model to wrap up without more tools.
		m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "system", Content: "This is the final tool round. Answer the user directly now; do not call any more tools."})
	}
	return m.startChatTurn()
}

// ---- chat diff review ----

func (m *Model) startChatReview() {
	m.chatReviewMode = true
	m.chatReviewIdx = 0
	m.loadChatChange(0)
}

func (m *Model) loadChatChange(idx int) {
	if idx < 0 || idx >= len(m.chatPendingChanges) {
		return
	}
	c := m.chatPendingChanges[idx]
	orig, prop := c.Orig, c.New
	if !strings.HasSuffix(orig, "\n") && orig != "" {
		orig += "\n"
	}
	if !strings.HasSuffix(prop, "\n") && prop != "" {
		prop += "\n"
	}
	m.chatReviewRows = vcs.SideBySide(orig, prop)
	m.chatReviewLeft = strings.Split(strings.TrimRight(orig, "\n"), "\n")
	m.chatReviewRight = strings.Split(strings.TrimRight(prop, "\n"), "\n")
	m.chatReviewOffY = 0
	m.chatReviewOffX = 0
}

// handleChatReview handles keys while a chat EDIT diff is shown. y accepts,
// n rejects; Tab cycles between multiple pending files.
func (m *Model) handleChatReview(msg tea.KeyPressMsg) tea.Cmd {
	n := len(m.chatPendingChanges)
	switch gitKeyName(msg) {
	case "y", "enter":
		return m.acceptChatReview(true) // apply only the current file
	case "a":
		return m.acceptChatReview(false) // apply every pending file
	case "n", "esc", "r":
		return m.rejectChatReview()
	case "tab":
		if n > 0 {
			m.chatReviewIdx = (m.chatReviewIdx + 1) % n
			m.loadChatChange(m.chatReviewIdx)
		}
	case "shift+tab":
		if n > 0 {
			m.chatReviewIdx = (m.chatReviewIdx - 1 + n) % n
			m.loadChatChange(m.chatReviewIdx)
		}
	case "up", "k":
		if m.chatReviewOffY > 0 {
			m.chatReviewOffY--
		}
	case "down", "j":
		if m.chatReviewOffY < len(m.chatReviewRows)-1 {
			m.chatReviewOffY++
		}
	case "pgup":
		m.chatReviewOffY -= m.paneViewHeight(m.activePane) / 2
		if m.chatReviewOffY < 0 {
			m.chatReviewOffY = 0
		}
	case "pgdown":
		m.chatReviewOffY += m.paneViewHeight(m.activePane) / 2
		if maxOff := len(m.chatReviewRows) - 1; m.chatReviewOffY > maxOff {
			m.chatReviewOffY = maxOff
		}
	case "home", "g":
		m.chatReviewOffY = 0
	case "end", "G":
		m.chatReviewOffY = len(m.chatReviewRows) - 1
	case "left", "h":
		m.chatReviewOffX -= 8
		if m.chatReviewOffX < 0 {
			m.chatReviewOffX = 0
		}
	case "right", "l":
		m.chatReviewOffX += 8
	}
	return nil
}

// acceptChatReview applies the pending EDIT changes through the agent Applier
// (atomic, stale-proof) and opens the touched files, then continues the loop.
// When only is true and more than one file is pending, only the currently
// previewed file is applied; otherwise the whole series is applied.
func (m *Model) acceptChatReview(only bool) tea.Cmd {
	var apply []agent.Change
	if only && len(m.chatPendingChanges) > 1 {
		apply = append(apply, m.chatPendingChanges[m.chatReviewIdx])
	} else {
		apply = m.chatPendingChanges
	}
	for _, c := range apply {
		_ = os.MkdirAll(filepath.Dir(c.Path), 0o755)
	}
	applier := agent.NewApplier()
	if err := applier.Apply(apply); err != nil {
		m.chatErr = "edit apply failed: " + err.Error()
		m.amendChatEditResults(nil)
	} else {
		for _, c := range apply {
			m.openAiFile(filepath.ToSlash(c.Path), true)
		}
		m.amendChatEditResults(&apply)
	}
	return m.endChatReview()
}

// rejectChatReview discards the pending EDIT changes and continues the loop.
func (m *Model) rejectChatReview() tea.Cmd {
	m.amendChatEditResults(nil)
	return m.endChatReview()
}

func (m *Model) endChatReview() tea.Cmd {
	m.chatReviewMode = false
	m.chatReviewRows = nil
	m.chatReviewLeft = nil
	m.chatReviewRight = nil
	return m.finalizeChatTools()
}

// amendChatEditResults updates the tool messages so the model learns which
// proposed edits landed. When applied is nil all edits are rejected; otherwise
// edits whose path matches an entry in applied are marked "[EDIT applied]" and
// the rest "[EDIT rejected]".
func (m *Model) amendChatEditResults(applied *[]agent.Change) {
	accepted := make(map[string]bool, 0)
	if applied != nil {
		for _, c := range *applied {
			accepted[shortenPath(m.baseDir(), c.Path)] = true
		}
	}
	for _, tr := range m.chatEditTracks {
		if tr.resultIdx < 0 || tr.resultIdx >= len(m.chatPendingResults) {
			continue
		}
		if applied != nil && accepted[tr.label] {
			m.chatPendingResults[tr.resultIdx].Content = "[EDIT applied] " + tr.label
		} else {
			m.chatPendingResults[tr.resultIdx].Content = "[EDIT rejected] " + tr.label
		}
	}
}

func (m *Model) chatReviewBottom() string {
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.chatReviewRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	var name string
	if len(m.chatPendingChanges) > 0 {
		name = m.chatPendingChanges[m.chatReviewIdx].Path
	}
	multi := ""
	if len(m.chatPendingChanges) > 1 {
		multi = fmt.Sprintf(" %d/%d ", m.chatReviewIdx+1, len(m.chatPendingChanges))
	}
	allHint := ""
	if len(m.chatPendingChanges) > 1 {
		allHint = "  a:all"
	}
	line := statusHiStyle.Render(" AI edit "+multi+m.fitPath(name, 24)) +
		hintStyle.Render(fmt.Sprintf(" +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render("   y:apply"+allHint+"  n:discard"+tabHint(m.chatPendingChanges))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func tabHint(changes []agent.Change) string {
	if len(changes) > 1 {
		return "  tab:next"
	}
	return ""
}

// cancelChat stops any running stream (called from shutdown).
func (m *Model) cancelChat() {
	if m.chatCancel != nil {
		m.chatCancel()
		m.chatCancel = nil
	}
}

// ---- rendering ----

// clampChatScroll keeps chatScroll within [0, len(chatRows)-bodyH] using the
// same body height the chat panel renders with (header + input, plus the hint
// bar when there is room for it).
func (m *Model) clampChatScroll() {
	h := m.viewHeight()
	inputH := m.chatInputHeight()
	bodyH := h - 1 - inputH
	if h >= 5 {
		bodyH = h - 2 - inputH // also account for the hint bar
	}
	if bodyH < 1 {
		bodyH = 1
	}
	if maxBack := len(m.chatRows) - bodyH; m.chatScroll > maxBack {
		m.chatScroll = maxInt(0, maxBack)
	}
	if m.chatScroll < 0 {
		m.chatScroll = 0
	}
}

// chatBodyRange returns the first visible chatRows index and the body height;
// shared by the panel renderer, scroll clamping and selection hit-testing so
// all three agree on the geometry.
func (m Model) chatBodyRange(total int) (start, bodyH int) {
	h := m.viewHeight()
	inputH := m.chatInputHeight()
	bodyH = h - 1 - inputH // header + multi-line input
	if h >= 5 {
		bodyH = h - 2 - inputH // also reserve room for the button strip
	}
	if bodyH < 1 {
		bodyH = 1
	}
	start = total - bodyH + m.chatScroll
	if start > total-bodyH {
		start = total - bodyH
	}
	if start < 0 {
		start = 0
	}
	return start, bodyH
}

func (m *Model) rebuildChatRows() {
	inner := m.chatInnerWidth()
	rows := make([]chatRow, 0, 64)
	add := func(kind, text string) { rows = append(rows, chatRow{kind: kind, text: text}) }

	addText := func(label, labelKind, textKind, content string) {
		add(labelKind, label)
		for _, l := range wrapRunes(content, inner) {
			add(textKind, l)
		}
		add("hint", "")
	}

	for _, msg := range m.chatMsgs {
		switch msg.Role {
		case "user":
			addText(" you", "label-you", "user", msg.Content)
		case "tool":
			m.addToolResultRow(&rows, add, msg, inner)
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if msg.Content != "" {
					m.addAIRows(&rows, msg.Content)
				}
				for _, tc := range msg.ToolCalls {
					add("label-tool", " "+m.g.iconTool+" "+tc.Name+" "+toolArgSummary(tc))
				}
				add("hint", "")
			} else {
				m.addAIRows(&rows, msg.Content)
			}
		}
	}
	if m.chatBusy && m.chatReply == "" {
		add("hint", " thinking...")
	}
	if m.chatReply != "" {
		m.addAIRows(&rows, m.chatReply)
	}
	if m.chatErr != "" {
		for _, l := range wrapRunes("[error] "+m.chatErr, inner) {
			add("err", l)
		}
	}
	if m.chatNotice != "" {
		for _, l := range wrapRunes(m.chatNotice, inner) {
			add("notice", l)
		}
		add("hint", "")
	}
	// The plan goes last so it stays visible without scrolling back.
	m.todoBlock(add, inner)
	if len(rows) == 0 {
		add("hint", " AI works through presets: Ollama, OpenAI, DeepSeek,")
		add("hint", " Groq, LM Studio, vLLM, Unsloth or any OpenAI-compatible server.")
		if m.chatModel == "" {
			add("hint", " No model yet — quick fix: Ctrl+P → 'AI: Preferences',")
			add("hint", " pick a provider, press t to test, Ctrl+S to save.")
			add("hint", " Free local route: install ollama, run `ollama pull")
			add("hint", " llama3.2`, keep it running — then just type below.")
		} else {
			add("hint", " Type a question, Enter sends.")
		}
	}
	m.chatRows = rows
}

// addToolResultRow renders a tool result as a compact card instead of dumping
// the raw output (which is still sent to the model as context).
func (m Model) addToolResultRow(rows *[]chatRow, add func(string, string), msg ai.Message, inner int) {
	add("label-tool", " "+m.g.iconTool+" "+msg.ToolName)
	for _, l := range m.compactLines(msg.Content, inner, 6) {
		add("tool", l)
	}
	add("hint", "")
}

// compactLines keeps the first few lines of a tool result and caps line
// length so a READ or RUN dump never floods the chat panel.
func (m Model) compactLines(s string, w, maxLines int) []string {
	if w < 1 {
		w = 1
	}
	if maxLines < 1 {
		maxLines = 1
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, maxLines+1)
	for i, ln := range lines {
		if i >= maxLines {
			out = append(out, " "+m.g.ellipsis+" ("+strconv.Itoa(len(lines)-maxLines)+" more lines)")
			break
		}
		if r := []rune(ln); len(r) > w {
			ln = string(r[:w]) + m.g.ellipsis
		}
		out = append(out, ln)
	}
	return out
}

// toolArgSummary renders the relevant argument of a tool call for the card.
func toolArgSummary(tc ai.ToolCall) string {
	if tc.Name == "EDIT" {
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		if a.Path != "" {
			return a.Path
		}
	}
	var a struct {
		Arg string `json:"arg"`
	}
	_ = json.Unmarshal([]byte(tc.Args), &a)
	if a.Arg != "" {
		return a.Arg
	}
	return tc.Args
}

// wrapRunes greedily wraps text to width w, hard-breaking words longer
// than w. Existing newlines are honored.
func wrapRunes(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, word := range strings.Split(para, " ") {
			if r := []rune(word); len(r) > w {
				if cur != "" { // flush pending line before hard-breaking
					out = append(out, cur)
					cur = ""
				}
				for len(r) > w {
					out = append(out, string(r[:w]))
					r = r[w:]
				}
				word = string(r)
			}
			switch {
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				out = append(out, cur)
				cur = word
			}
		}
		out = append(out, cur)
	}
	return out
}
