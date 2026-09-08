package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/vcs"
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
)

type chatRow struct {
	kind string // "label-you" | "user" | "label-ai" | "ai" | "tool" | "hint" | "err"
	text string
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
	if m.ai == nil {
		m.ai = ai.NewProvider(ai.Config{
			Type:   ai.ProviderType(m.cfg.AI.Provider),
			URL:    m.cfg.AI.OllamaURL,
			Model:  m.cfg.AI.Model,
			APIKey: m.cfg.AI.APIKey,
		})
	}
	if m.chatOpen && m.chatModel == "" {
		m.pickChatModel()
	}
	return nil
}

// pickChatModel resolves the model once: DMED_MODEL wins, otherwise the
// first model reported by the server.
func (m *Model) pickChatModel() {
	if m.cfg.AI.Model != "" {
		m.chatModel = m.cfg.AI.Model
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := m.ai.Models(ctx)
	switch {
	case err != nil:
		m.msg = "provider offline (" + err.Error() + ")"
	case len(models) == 0:
		m.msg = "no models available"
	default:
		m.chatModel = models[0]
		m.ai = ai.NewProvider(ai.Config{
			Type:   ai.ProviderType(m.cfg.AI.Provider),
			URL:    m.cfg.AI.OllamaURL,
			Model:  m.chatModel,
			APIKey: m.cfg.AI.APIKey,
		})
	}
}

func (m *Model) handleChat(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.chatOpen = false
		m.chatFocus = false
		m.chatReviewMode = false
		m.msg = ""
	case "enter":
		return m.chatSubmit()
	case "backspace":
		if n := len(m.chatIn); n > 0 {
			m.chatIn = m.chatIn[:n-1]
		}
	case "pgup":
		m.chatScroll += m.paneViewHeight(m.activePane) / 2
		m.clampChatScroll()
	case "pgdn":
		m.chatScroll -= m.paneViewHeight(m.activePane) / 2
		m.clampChatScroll()
	case "ctrl+u": // start a new conversation thread
		m.cancelChat()
		m.chatMsgs = nil
		m.chatReply = ""
		m.chatErr = ""
		m.chatBusy = false
		m.chatToolRound = 0
		m.chatIn = nil
		m.chatScroll = 0
		m.rebuildChatRows()
		m.chatReviewMode = false
	default:
		if len(msg.Text) > 0 {
			m.chatIn = append(m.chatIn, []rune(msg.Text)...)
		}
	}
	return nil
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
			m.chatErr = "no model available; start ollama or run: ollama pull llama3.2"
			m.rebuildChatRows()
			return nil
		}
	}

	m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "user", Content: text})
	m.chatReply = ""
	m.chatErr = ""
	m.chatBusy = true
	m.chatScroll = 0
	m.chatToolRound = 0
	m.rebuildChatRows()

	return m.startChatTurn()
}

// startChatTurn kicks off a streaming turn with the full conversation plus the
// native tool definitions. When the stream finishes, tool calls (if any) are
// delivered together with the Done marker on the channel.
func (m *Model) startChatTurn() tea.Cmd {
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
	go func() {
		defer close(ch)
		var tools []ai.ToolCall
		var once sync.Once
		err := m.ai.ChatStream(ctx, ai.Request{Messages: msgs, Tools: chatToolDefs()}, ai.Handler{
			Delta: func(d string) {
				select {
				case ch <- chatEvent{delta: d, gen: gen}:
				case <-ctx.Done():
				}
			},
			ToolCalls: func(calls []ai.ToolCall) {
				once.Do(func() { tools = calls })
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
	}()
	return waitForChatOutput(ch, gen)
}

// chatRequestMessages snapshots the conversation with a system prompt that
// carries the active file (or selection) as context.
func (m *Model) chatRequestMessages() []ai.Message {
	var b strings.Builder
	b.WriteString(m.cfg.AI.SystemPrompt)
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
	msgs := make([]ai.Message, 0, len(m.chatMsgs)+1)
	msgs = append(msgs, ai.Message{Role: "system", Content: b.String()})
	msgs = append(msgs, m.chatMsgs...)
	return msgs
}

const maxChatToolRounds = 6

// chatEditTrack maps one proposed EDIT to its slot in the pending results so
// the accept/reject decision can amend the message the model sees.
type chatEditTrack struct {
	resultIdx int
	label     string
}

// handleChatToolsDone runs a batch of native tool calls after a stream ends.
// Non-edit results are finalised immediately; EDIT proposals pause the loop
// for a side-by-side diff review. It returns a tea.Cmd that continues the
// conversation, or nil when waiting on human review.
func (m *Model) handleChatToolsDone(content string, tools []ai.ToolCall) tea.Cmd {
	before := snapshotFiles(m.root)
	results := make([]ai.Message, 0, len(tools))
	var pending []agent.Change
	var tracks []chatEditTrack
	for _, tc := range tools {
		res, chg := m.execChatTool(tc)
		if chg != nil {
			pending = append(pending, *chg)
			tracks = append(tracks, chatEditTrack{resultIdx: len(results), label: shortenPath(m.baseDir(), chg.Path)})
		}
		results = append(results, ai.Message{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Content: res})
	}

	// Open every file the tools created or rewrote (e.g. by RUN) in tabs and
	// summarize them in the transcript. EDIT proposals are still pending human
	// review, so they open when accepted (see acceptChatReview).
	m.openTouchedFiles(before, results)

	m.chatPendingAssistant = ai.Message{Role: "assistant", Content: content, ToolCalls: tools}
	m.chatPendingResults = results
	m.chatPendingChanges = pending
	m.chatEditTracks = tracks
	m.rebuildChatRows()

	if len(pending) > 0 {
		m.startChatReview()
		return nil
	}
	return m.finalizeChatTools()
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

// finalizeChatTools commits the pending assistant + tool-result messages into
// the conversation and starts the next turn, capping the tool loop depth.
func (m *Model) finalizeChatTools() tea.Cmd {
	m.chatMsgs = append(m.chatMsgs, m.chatPendingAssistant)
	m.chatMsgs = append(m.chatMsgs, m.chatPendingResults...)
	m.chatPendingAssistant = ai.Message{}
	m.chatPendingResults = nil
	m.chatPendingChanges = nil
	m.chatEditTracks = nil
	m.chatToolRound++
	m.rebuildChatRows()

	if m.chatToolRound > maxChatToolRounds {
		m.chatErr = "tool loop exceeded max iterations"
		m.rebuildChatRows()
		return nil
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
	case "y", "enter", "a":
		return m.acceptChatReview()
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
	case "pgdn":
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
func (m *Model) acceptChatReview() tea.Cmd {
	for _, c := range m.chatPendingChanges {
		_ = os.MkdirAll(filepath.Dir(c.Path), 0o755)
	}
	applier := agent.NewApplier()
	if err := applier.Apply(m.chatPendingChanges); err != nil {
		m.chatErr = "edit apply failed: " + err.Error()
		m.amendChatEditResults(false)
	} else {
		for _, c := range m.chatPendingChanges {
			m.openAiFile(filepath.ToSlash(c.Path), true)
		}
		m.amendChatEditResults(true)
	}
	return m.endChatReview()
}

// rejectChatReview discards the pending EDIT changes and continues the loop.
func (m *Model) rejectChatReview() tea.Cmd {
	m.amendChatEditResults(false)
	return m.endChatReview()
}

func (m *Model) endChatReview() tea.Cmd {
	m.chatReviewMode = false
	m.chatReviewRows = nil
	m.chatReviewLeft = nil
	m.chatReviewRight = nil
	return m.finalizeChatTools()
}

// amendChatEditResults updates the tool messages for accepted/rejected edits
// so the model learns whether each proposed change landed.
func (m *Model) amendChatEditResults(applied bool) {
	for _, tr := range m.chatEditTracks {
		if tr.resultIdx < 0 || tr.resultIdx >= len(m.chatPendingResults) {
			continue
		}
		if applied {
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
	line := statusHiStyle.Render(" AI edit "+multi+fitPath(name, 24)) +
		hintStyle.Render(fmt.Sprintf(" +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render("   y:apply  n:discard"+tabHint(m.chatPendingChanges))
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
	bodyH := h - 2
	if h >= 5 {
		bodyH = h - 3
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
					addText(" ai", "label-ai", "ai", msg.Content)
				}
				for _, tc := range msg.ToolCalls {
					add("label-tool", " ⛏ "+tc.Name+" "+toolArgSummary(tc))
				}
				add("hint", "")
			} else {
				addText(" ai", "label-ai", "ai", msg.Content)
			}
		}
	}
	if m.chatBusy && m.chatReply == "" {
		add("hint", " thinking...")
	}
	if m.chatReply != "" {
		addText(" ai", "label-ai", "ai", m.chatReply)
	}
	if m.chatErr != "" {
		for _, l := range wrapRunes("[error] "+m.chatErr, inner) {
			add("err", l)
		}
	}
	if len(rows) == 0 {
		add("hint", " Local AI via Ollama: free and offline.")
		add("hint", " Type a question, Enter sends.")
	}
	m.chatRows = rows
}

// addToolResultRow renders a tool result as a compact card instead of dumping
// the raw output (which is still sent to the model as context).
func (m Model) addToolResultRow(rows *[]chatRow, add func(string, string), msg ai.Message, inner int) {
	add("label-tool", " ⛏ "+msg.ToolName)
	for _, l := range compactLines(msg.Content, inner, 6) {
		add("tool", l)
	}
	add("hint", "")
}

// compactLines keeps the first few lines of a tool result and caps line
// length so a READ or RUN dump never floods the chat panel.
func compactLines(s string, w, maxLines int) []string {
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
			out = append(out, " … ("+strconv.Itoa(len(lines)-maxLines)+" more lines)")
			break
		}
		if r := []rune(ln); len(r) > w {
			ln = string(r[:w]) + "…"
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
