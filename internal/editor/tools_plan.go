package editor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/ai"
	"dmed/internal/ask"
	"dmed/internal/todo"
)

// Plan tools: the model's own checklist (TODO_WRITE/TODO_SET/TODO_READ) and one
// tool that parks the loop for a human answer (ASK_USER). They live together
// because both are about the model talking to the person rather than editing
// code, and neither may touch a buffer or a file.

// todoDefs returns the three checklist tool definitions. Their parameters are
// shared because a model that gets three slightly different schemas gets them
// wrong more often than one that gets the same shape three times.
func todoDefs() []ai.ToolDef {
	itemsSchema := map[string]any{
		"type":        "array",
		"description": "the steps of the plan",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "one step"},
				"done": map[string]any{"type": "boolean", "description": "whether the step is finished"},
			},
			"required": []string{"text"},
		},
	}
	return []ai.ToolDef{
		{
			Name: "TODO_WRITE",
			Description: "Replace the whole plan with the steps you intend to take. Use it when you (re)plan, " +
				"and mark a step done:true once you finished it. Keep the list short (under 8 steps).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"items": itemsSchema},
				"required":   []string{"items"},
			},
		},
		{
			Name: "TODO_SET",
			Description: "Add new steps to the existing plan without rewriting it, or tick a step off by " +
				"repeating its text with done:true.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"items": itemsSchema},
				"required":   []string{"items"},
			},
		},
		{
			Name:        "TODO_READ",
			Description: "Read the current plan back (use after TODO_WRITE/TODO_SET to confirm the state).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name: "SWITCH_MODE",
			Description: "Ask the user to switch from plan mode to act mode so you may edit files and run " +
				"commands. Only valid while in plan mode; the user can always decline. " +
				"mode must be \"act\". Changes still go through the diff review afterwards.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{"type": "string", "enum": []string{"act"}, "description": "the mode to ask for"},
				},
				"required": []string{"mode"},
			},
		},
		{
			Name: "ASK_USER",
			Description: "Ask the user a question and wait for the answer. Use it when the task is genuinely " +
				"ambiguous or a decision is the user's to make — never to confirm something you can read yourself. " +
				"The loop pauses until they answer.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{"type": "string", "description": "the question to ask"},
					"choices": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "optional suggested answers",
					},
				},
				"required": []string{"question"},
			},
		},
	}
}

// todoList returns the model's checklist, creating it on first use. Tests build
// a Model literal without going through New, so this cannot be done in the
// constructor alone.
func (m *Model) todoList() *todo.List {
	if m.todo == nil {
		m.todo = todo.New()
	}
	return m.todo
}

// askQueue returns the pending-question queue, created on first use.
func (m *Model) askQueue() *ask.Queue {
	if m.asks == nil {
		m.asks = ask.NewQueue()
	}
	return m.asks
}

// execTodoTool runs one of the plan tools. TODO_WRITE replaces the list, the
// others add to it, and every one of them returns the whole resulting plan —
// that single result is what the model reads and what the chat renders, so the
// two can never disagree.
func (m *Model) execTodoTool(name, args string) ToolResult {
	switch name {
	case "TODO_READ":
		if m.todo == nil || m.todo.Empty() {
			return ToolResult{Text: "[TODO_READ] (no plan yet)"}
		}
		return ToolResult{Text: "[TODO_READ]\n" + m.todo.Render()}
	case "TODO_WRITE", "TODO_SET":
		var a struct {
			Items []struct {
				Text string `json:"text"`
				Done bool   `json:"done"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return ToolResult{Text: "[" + name + " error] " + err.Error()}
		}
		if len(a.Items) == 0 {
			return ToolResult{Text: "[" + name + " error] no items given"}
		}
		items := make([]todo.Item, 0, len(a.Items))
		for _, it := range a.Items {
			items = append(items, todo.Item{Text: it.Text, Done: it.Done})
		}
		list := m.todoList()
		if name == "TODO_WRITE" {
			kept, dropped := list.Replace(items)
			if dropped > 0 {
				return ToolResult{Text: fmt.Sprintf("[TODO_WRITE] kept %d step(s), %d over the limit of %d\n%s",
					kept, dropped, todo.MaxItems, list.Render())}
			}
			return ToolResult{Text: fmt.Sprintf("[TODO_WRITE] %d step(s)\n%s", kept, list.Render())}
		}
		merged := list.Merge(items)
		return ToolResult{Text: fmt.Sprintf("[TODO_SET] %d step(s)\n%s", len(merged), todo.Render(merged))}
	}
	return ToolResult{Text: "[unknown tool " + name + "]"}
}

// execAskUser turns a question into a parked tool call: the loop stops until the
// user answers, and the answer becomes the tool result the model reads.
func (m *Model) execAskUser(question string, choices []string) ToolResult {
	question = strings.TrimSpace(question)
	if question == "" {
		return ToolResult{Text: "[ASK_USER error] empty question"}
	}
	req := m.askQueue().Next(question, choices, time.Now())
	return ToolResult{
		Text: askPlaceholder(req),
		Park: Park{Kind: ParkAskUser, Ask: &req},
	}
}

// askPlaceholder is what the transcript shows while the question is on screen:
// the question itself, so scrolling back tells the user what they are looking at.
func askPlaceholder(req ask.Request) string {
	out := "[ASK_USER] " + req.Question
	if len(req.Choices) > 0 {
		out += "\nchoices: " + strings.Join(req.Choices, " | ")
	}
	return out + "\n(waiting for your answer)"
}

// resolveAsk turns the user's input into the text the model receives: the
// chosen suggestion or what they typed, plus a marker when they walked away.
func resolveAsk(input string, cancelled bool) string {
	switch {
	case cancelled:
		return "[ask_user cancelled — the user did not answer, continue without it]"
	case strings.TrimSpace(input) == "":
		return "[ask_user answered with an empty reply — continue without it]"
	default:
		return "[ask_user answered] " + strings.TrimSpace(input)
	}
}

// handleAskKey handles keys while an ASK_USER question is on screen. The chat
// input is parked, so typed characters go to the answer instead of the
// conversation — there is exactly one cursor and it is on the question.
func (m *Model) handleAskKey(msg tea.KeyPressMsg) tea.Cmd {
	req := m.askReq
	if req == nil {
		return nil
	}
	switch gitKeyName(msg) {
	case "esc":
		return m.finishChatAsk("", true)
	case "enter":
		answer := strings.TrimSpace(string(m.askIn))
		if answer == "" && len(req.Choices) > 0 && m.askSel >= 0 && m.askSel < len(req.Choices) {
			answer = req.Choices[m.askSel]
		}
		return m.finishChatAsk(answer, false)
	case "up", "k":
		if len(req.Choices) > 0 {
			m.askSel--
			if m.askSel < 0 {
				m.askSel = len(req.Choices) - 1
			}
		}
		return nil
	case "down", "j":
		if len(req.Choices) > 0 {
			m.askSel++
			if m.askSel >= len(req.Choices) {
				m.askSel = 0
			}
		}
		return nil
	case "tab":
		// Tab picks the highlighted choice into the input, so it can be edited
		// before submitting.
		if len(req.Choices) > 0 && m.askSel >= 0 && m.askSel < len(req.Choices) {
			m.askIn = []rune(req.Choices[m.askSel])
		}
		return nil
	case "backspace":
		if n := len(m.askIn); n > 0 {
			m.askIn = m.askIn[:n-1]
		}
		return nil
	}
	if len(msg.Text) > 0 {
		m.askIn = append(m.askIn, []rune(msg.Text)...)
	}
	return nil
}

// finishChatAsk answers the on-screen question: the answer replaces the
// placeholder, the queue entry is resolved, and the tool loop resumes with the
// next parked question or the next turn.
func (m *Model) finishChatAsk(answer string, cancelled bool) tea.Cmd {
	park := m.chatPark
	m.askReq = nil
	m.askIn = nil
	m.askSel = 0
	m.chatPark = nil
	m.msg = ""
	if park != nil && park.park.Ask != nil {
		if cancelled {
			m.askQueue().Cancel(park.park.Ask.ID)
		} else {
			m.askQueue().Answer(park.park.Ask.ID, answer)
		}
		resolveParkedResult(m.chatPendingResults, park.resultIdx, resolveAsk(answer, cancelled))
	}
	m.rebuildChatRows()
	if m.startNextPark() {
		return nil
	}
	return m.finalizeChatTools()
}

// ---- rendering ----

// todoBlock adds the plan to the chat transcript as its own labelled block,
// below the conversation, so the current state is one glance away.
func (m *Model) todoBlock(add func(kind, text string), inner int) {
	if m.todo == nil || m.todo.Empty() {
		return
	}
	add("label-todo", " "+m.g.bookmark+" "+m.t("chat.todo"))
	for i, it := range m.todo.Items() {
		mark := m.g.breakptO
		kind := "todo"
		if it.Done {
			mark = m.g.check
			kind = "todo-done"
		}
		for j, l := range wrapRunes(fmt.Sprintf("%s %d. %s", mark, i+1, it.Text), inner) {
			if j == 0 {
				l = " " + l
			}
			add(kind, l)
		}
	}
	add("hint", "")
}

// askLine renders the bottom line while a question is on screen.
func (m Model) askLine() string {
	line := statusHiStyle.Render(m.t("chat.ask_label"))
	hint := m.t("chat.ask_hint")
	if m.askReq != nil && len(m.askReq.Choices) > 0 {
		hint = m.t("chat.ask_hint_choices")
	}
	line += hintStyle.Render(" " + hint)
	if fill := m.width - lipgloss.Width(line); fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// askExtraRows is the height the question overlay occupies above the status
// bar: the question itself (wrapped) plus the answer input and choices.
func (m Model) askExtraRows() int {
	if m.askReq == nil {
		return 0
	}
	w := m.width - 4
	if w < 1 {
		w = 1
	}
	rows := len(wrapRunes(m.askReq.Question, w))
	if len(m.askReq.Choices) > 0 {
		rows += len(m.askReq.Choices)
	}
	input := strings.TrimSpace(string(m.askIn))
	if input == "" && len(m.askReq.Choices) > 0 && m.askSel >= 0 && m.askSel < len(m.askReq.Choices) {
		input = m.askReq.Choices[m.askSel]
	}
	rows += len(wrapRunes(input, w))
	if rows < 1 {
		rows = 1
	}
	return rows + 1 // the bottom hint line
}

// askOverlay renders the question block drawn just above the status bar.
func (m Model) askOverlay() []string {
	req := m.askReq
	if req == nil {
		return nil
	}
	w := m.width - 2
	if w < 1 {
		w = 1
	}
	var out []string
	for _, l := range wrapRunes(m.t("chat.ask_label")+" "+req.Question, w) {
		out = append(out, statusStyle.Render(" "+l))
	}
	choiceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	selStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	for i, c := range req.Choices {
		prefix := "   "
		st := choiceStyle
		if i == m.askSel {
			prefix = " > "
			st = selStyle
		}
		for j, l := range wrapRunes(c, w-3) {
			if j == 0 {
				out = append(out, st.Render(prefix+l))
			} else {
				out = append(out, st.Render("   "+l))
			}
		}
	}
	answer := strings.TrimSpace(string(m.askIn))
	if answer == "" && len(req.Choices) > 0 && m.askSel >= 0 && m.askSel < len(req.Choices) {
		answer = req.Choices[m.askSel]
	}
	inputLines := wrapRunes(answer, w-3)
	if len(inputLines) == 0 {
		inputLines = []string{""}
	}
	for _, l := range inputLines {
		out = append(out, statusStyle.Render(" > "+l)+cursorStyle.Render(" "))
	}
	return out
}
