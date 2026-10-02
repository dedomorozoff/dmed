package editor

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/ai"
	"dmed/internal/ask"
)

func TestChatToolDefsIncludePlanTools(t *testing.T) {
	names := map[string]bool{}
	for _, d := range chatToolDefs() {
		names[d.Name] = true
	}
	for _, want := range []string{"TODO_WRITE", "TODO_SET", "TODO_READ", "ASK_USER"} {
		if !names[want] {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}
}

func TestTodoToolsKeepAndTickSteps(t *testing.T) {
	m := Model{root: t.TempDir()}

	res := m.execChatTool(ai.ToolCall{Name: "TODO_WRITE",
		Args: `{"items":[{"text":"read the code"},{"text":"add tests"}]}`})
	if res.Change != nil {
		t.Fatal("a plan change must never touch a file")
	}
	if !containsStr(res.Text, "read the code") {
		t.Fatalf("result = %q, want the plan echoed back", res.Text)
	}

	res = m.execChatTool(ai.ToolCall{Name: "TODO_SET",
		Args: `{"items":[{"text":"read the code","done":true},{"text":"write docs"}]}`})
	if !containsStr(res.Text, "[1] done read the code") {
		t.Fatalf("result = %q, want the step ticked rather than duplicated", res.Text)
	}
	items := m.todoList().Items()
	if len(items) != 3 {
		t.Fatalf("want 3 steps, got %d: %+v", len(items), items)
	}
	if !items[0].Done || items[1].Text != "add tests" {
		t.Fatalf("steps = %+v", items)
	}

	res = m.execChatTool(ai.ToolCall{Name: "TODO_READ"})
	if !containsStr(res.Text, "[3] todo write docs") {
		t.Fatalf("TODO_READ = %q", res.Text)
	}

	res = m.execChatTool(ai.ToolCall{Name: "todo_set", Args: `{"items":[]}`})
	if !containsStr(res.Text, "no items") {
		t.Fatalf("an empty plan must error instead of wiping it: %q", res.Text)
	}
	if m.todoList().Len() != 3 {
		t.Fatal("a rejected call must leave the plan alone")
	}
}

func TestTodoToolsRejectBrokenArgs(t *testing.T) {
	m := Model{root: t.TempDir()}
	res := m.execChatTool(ai.ToolCall{Name: "TODO_WRITE", Args: `{not json`})
	if !containsStr(res.Text, "TODO_WRITE error") {
		t.Fatalf("broken JSON must error, not panic: %q", res.Text)
	}
}

// TestTodoBlockRendersInChat checks the plan is visible in the transcript
// without the user asking for it: a plan the model keeps and the user cannot see
// is worse than no plan.
func TestTodoBlockRendersInChat(t *testing.T) {
	m := New()
	m.rebuildChatRows()
	if hasRowKind(m.chatRows, "label-todo") {
		t.Fatal("no plan means no block")
	}

	m.execChatTool(ai.ToolCall{Name: "TODO_WRITE", Args: `{"items":[{"text":"first"},{"text":"second","done":true}]}`})
	m.rebuildChatRows()

	var label string
	var pending, done []string
	for _, r := range m.chatRows {
		switch r.kind {
		case "label-todo":
			label = r.text
		case "todo":
			pending = append(pending, r.text)
		case "todo-done":
			done = append(done, r.text)
		}
	}
	if !containsStr(label, "first") && strings.TrimSpace(label) == "" {
		t.Fatalf("plan label = %q", label)
	}
	if len(pending) == 0 || len(done) == 0 {
		t.Fatalf("want one pending and one done row, got %v / %v", pending, done)
	}
	if !containsStr(pending[0], "first") {
		t.Fatalf("pending row = %q", pending[0])
	}
	if !containsStr(done[0], "second") {
		t.Fatalf("done row = %q", done[0])
	}
}

// TestAskUserParksAndAnswers is the core of ask_user: the loop stops, the
// question is on screen, and the answer — not a placeholder — is what the model
// reads.
func TestAskUserParksAndAnswers(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())

	cmd := m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "ASK_USER",
		Args: `{"question":"which database?","choices":["postgres","sqlite"]}`}})
	if cmd != nil {
		t.Fatal("the loop must park for the answer")
	}
	if m.askReq == nil {
		t.Fatal("no question on screen")
	}
	if m.askReq.Question != "which database?" || len(m.askReq.Choices) != 2 {
		t.Fatalf("question = %+v", m.askReq)
	}
	if !m.chatOpen {
		t.Fatal("the chat must open, otherwise the question is invisible")
	}
	if m.chatPendingResults[0].Content == "" || !containsStr(m.chatPendingResults[0].Content, "which database?") {
		t.Fatalf("transcript = %q, want the question visible", m.chatPendingResults[0].Content)
	}

	// Typing replaces the highlighted suggestion, so the user can edit it.
	m.handleAskKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m.handleAskKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if string(m.askIn) != "ps" {
		t.Fatalf("input = %q, want typed characters to land in the answer", m.askIn)
	}
	m.handleAskKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.askReq != nil {
		t.Fatal("the question must be closed once answered")
	}
	if m.asks.Len() != 0 {
		t.Fatalf("the question is still pending: %v", m.asks.Pending())
	}
	var answered bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "[ask_user answered] ps") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("the model must learn the answer: %+v", m.chatMsgs)
	}
}

// TestAskUserChoiceSelection covers the suggested-answer path: Enter with an
// untouched input submits the highlighted choice.
func TestAskUserChoiceSelection(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "ASK_USER",
		Args: `{"question":"pick one","choices":["a","b","c"]}`}})

	m.handleAskKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.askSel != 1 {
		t.Fatalf("selection = %d, want 1", m.askSel)
	}
	m.handleAskKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "[ask_user answered] b") {
			return
		}
	}
	t.Fatalf("the chosen option must reach the model: %+v", m.chatMsgs)
}

func TestAskUserCancelTellsTheModel(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "ASK_USER", Args: `{"question":"pick"}`}})

	m.handleAskKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.askReq != nil {
		t.Fatal("Esc must close the question")
	}
	var told bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "ask_user cancelled") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the model must learn the question was skipped: %+v", m.chatMsgs)
	}
}

// TestAskUserQueuesWithOtherParks: a round that asks a question and runs a
// command must ask for both, one at a time, and neither may be lost.
func TestAskUserQueuesWithOtherParks(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.cfg.AI.AllowRun = "ask"
	writeTestFile(t, filepath.Join(m.root, "a.txt"), "a\n")

	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "RUN", Args: `{"arg":"echo hi"}`},
		{ID: "c2", Name: "ASK_USER", Args: `{"question":"continue?"}`},
	})

	if m.chatPark == nil || m.chatPark.park.Kind != ParkRunConfirm {
		t.Fatalf("first park = %+v, want the command", m.chatPark)
	}
	if len(m.chatParks) != 1 {
		t.Fatalf("the question must stay queued, got %d", len(m.chatParks))
	}

	m.finishChatRunConfirm(false)
	if m.chatPark == nil || m.chatPark.park.Kind != ParkAskUser {
		t.Fatalf("second park = %+v, want the question", m.chatPark)
	}
	m.finishChatAsk("yes", false)

	if m.chatPark != nil || len(m.chatParks) != 0 {
		t.Fatalf("all parks must be resolved: %+v / %d", m.chatPark, len(m.chatParks))
	}
	var both bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "not approved") {
			both = true
		}
	}
	if !both {
		t.Fatalf("the declined command must reach the model: %+v", m.chatMsgs)
	}
}

// TestAskOverlayRenders pins the layout contract: the overlay must reserve the
// rows it draws, or it would cover the last line of code.
func TestAskOverlayRenders(t *testing.T) {
	m := New()
	if m.askExtraRows() != 0 || len(m.askOverlay()) != 0 {
		t.Fatal("no question means no overlay")
	}
	m.width = 60
	m.askReq = &ask.Request{ID: "1",
		Question: "which database should the migration target?", Choices: []string{"postgres", "sqlite"}}

	overlay := m.askOverlay()
	extra := m.askExtraRows()
	if len(overlay) != extra-1 {
		t.Fatalf("overlay draws %d rows but reserves %d", len(overlay), extra)
	}
	joined := strings.Join(overlay, "\n")
	if !containsStr(joined, "postgres") || !containsStr(joined, "which database") {
		t.Fatalf("overlay = %q", joined)
	}
	for _, line := range overlay {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("overlay row wider than the screen: %q (%d > %d)", line, w, m.width)
		}
	}
}

// TestResetChatDropsPlanAndQuestion keeps a stale plan or question from
// following the user into a new conversation.
func TestResetChatDropsPlanAndQuestion(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "TODO_WRITE",
		Args: `{"items":[{"text":"keep"}]}`}})
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c2", Name: "ASK_USER", Args: `{"question":"q?"}`}})
	if m.todoList().Len() == 0 || m.asks.Len() == 0 {
		t.Fatal("precondition failed")
	}

	m.resetChatConversation()

	if !m.todoList().Empty() {
		t.Fatal("a new conversation must start without a plan")
	}
	if m.asks.Len() != 0 {
		t.Fatalf("questions from the old conversation must not stay pending: %v", m.asks.Pending())
	}
	if m.askReq != nil {
		t.Fatal("the question overlay must go away")
	}
}

func hasRowKind(rows []chatRow, kind string) bool {
	for _, r := range rows {
		if r.kind == kind {
			return true
		}
	}
	return false
}
