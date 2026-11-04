package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
)

func TestChatUsageCardAttachedToReply(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.chatModel = "test-model"
	m.chatIn = []rune("hi")
	m.ai = &fakeProvider{pieces: []string{"answer"}, usage: &ai.Usage{PromptTokens: 1200, CompletionTokens: 30, TotalTokens: 1230}}

	cmd := m.chatSubmit()
	if cmd == nil {
		t.Fatal("submit must start a turn")
	}
	// Drive the stream through the model's own update branch, as bubbletea
	// would: the fake provider fills the channel before the waiter reads it.
	for i := 0; i < 32; i++ {
		raw := cmd()
		msg, ok := raw.(ChatOutputMsg)
		if !ok {
			t.Fatalf("expected ChatOutputMsg, got %#v", raw)
		}
		mdl, next := m.update(raw)
		if mm, ok := mdl.(Model); ok {
			m = mm
		}
		if msg.Done {
			break
		}
		if next == nil {
			t.Fatal("stream ended without a Done event")
		}
		cmd = next
	}

	last := m.chatMsgs[len(m.chatMsgs)-1]
	if last.Role != "assistant" || last.Usage == nil || last.Usage.PromptTokens != 1200 {
		t.Fatalf("usage not attached: %+v", m.chatMsgs)
	}
	if m.chatSessionUsage.PromptTokens != 1200 {
		t.Fatalf("session total = %+v", m.chatSessionUsage)
	}
	// The turn accumulator is drained into the card, not double-counted.
	if !m.chatTurnUsage.Empty() {
		t.Fatalf("turn usage was not reset: %+v", m.chatTurnUsage)
	}
}

func TestFormatUsageCard(t *testing.T) {
	got := formatUsage(ai.Usage{PromptTokens: 1234, CompletionTokens: 30})
	if !strings.Contains(got, "1.2k in") || !strings.Contains(got, "30 out") {
		t.Fatalf("card = %q", got)
	}
	if got := formatUsage(ai.Usage{PromptTokens: 999, CompletionTokens: 2}); !strings.HasPrefix(got, "999 in") {
		t.Fatalf("small numbers must stay exact: %q", got)
	}
}

func TestQueueSetUsageAddsRounds(t *testing.T) {
	q := agent.NewQueue(nil)
	task := q.Enqueue("work")
	q.SetUsage(task.ID, ai.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	q.SetUsage(task.ID, ai.Usage{PromptTokens: 150, CompletionTokens: 10, TotalTokens: 160})
	got, _ := q.Find(task.ID)
	if got.Usage.PromptTokens != 250 || got.Usage.CompletionTokens != 30 {
		t.Fatalf("rounds must add (each re-bills the context): %+v", got.Usage)
	}
	// An empty report changes nothing.
	q.SetUsage(task.ID, ai.Usage{})
	got, _ = q.Find(task.ID)
	if got.Usage.PromptTokens != 250 {
		t.Fatalf("an empty report must not reset the total: %+v", got.Usage)
	}
}

func TestAgentPanelRowShowsUsage(t *testing.T) {
	m := storeHarness(t)
	tk := m.agentQueue.Enqueue("work")
	m.agentQueue.SetUsage(tk.ID, ai.Usage{PromptTokens: 2000, CompletionTokens: 40})
	m.initPanes()
	m.width, m.height = 120, 30
	rows := m.agentPanel(10)
	var found bool
	for _, r := range rows {
		if strings.Contains(r, "2.0k in") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the task row must carry the card: %+v", rows)
	}
}

func TestModelPickFlow(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.chatModel = "old-model"

	// A reply for the current generation opens the list.
	m.handleModelsMsg(aiModelsMsg{gen: m.modelPickGen, models: []string{"a", "b", "old-model"}})
	if !m.modelPickOpen || len(m.modelPick) != 3 {
		t.Fatalf("panel = %v %v", m.modelPickOpen, m.modelPick)
	}

	// Stale replies cannot open a panel over a newer request.
	m2 := chatEditHarness(t, t.TempDir())
	m2.modelPickGen = 7
	m2.handleModelsMsg(aiModelsMsg{gen: 6, models: []string{"x"}})
	if m2.modelPickOpen {
		t.Fatal("a superseded fetch must not open")
	}
	// An error answers on the status bar, not as an empty box.
	m2.handleModelsMsg(aiModelsMsg{gen: 7, err: "connection refused"})
	if m2.modelPickOpen || !strings.Contains(m2.msg, "connection refused") {
		t.Fatalf("error handling: open=%v msg=%q", m2.modelPickOpen, m2.msg)
	}
	// An empty list says so instead of opening a box to click through.
	m4 := chatEditHarness(t, t.TempDir())
	m4.handleModelsMsg(aiModelsMsg{gen: m4.modelPickGen})
	if m4.modelPickOpen {
		t.Fatal("no models must not open a panel")
	}

	// Two steps down the list lands on "old-model" — the active one — and
	// Enter is then a no-op that only closes: switching to what is already
	// on the header must not rewrite the config file for nothing.
	before := m.cfg.AI.Model
	m.handleModelPick("j", tea.KeyPressMsg{})
	m.handleModelPick("j", tea.KeyPressMsg{})
	m.handleModelPick("enter", tea.KeyPressMsg{})
	if m.modelPickOpen {
		t.Fatal("Enter closes the panel")
	}
	if m.chatModel != "old-model" || m.cfg.AI.Model != before {
		t.Fatalf("the active pick must survive: chat=%q cfg=%q want %q", m.chatModel, m.cfg.AI.Model, before)
	}

	// Switching to another model writes the config key: the harness root is
	// a temp dir, so the write is contained, and the reload must show it.
	m5 := chatEditHarness(t, t.TempDir())
	m5.root = t.TempDir()
	m5.chatModel = "old"
	m5.handleModelsMsg(aiModelsMsg{gen: m5.modelPickGen, models: []string{"fresh"}})
	m5.handleModelPick("enter", tea.KeyPressMsg{})
	if m5.chatModel != "fresh" {
		t.Fatalf("chat model = %q", m5.chatModel)
	}
	if m5.cfg.AI.Model != "fresh" {
		t.Fatalf("the config model did not follow: %q", m5.cfg.AI.Model)
	}
}

func TestChatUsageRowsRender(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.chatMsgs = []ai.Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a", Usage: &ai.Usage{PromptTokens: 500, CompletionTokens: 7}},
	}
	m.rebuildChatRows()
	var found bool
	for _, r := range m.chatRows {
		if r.kind == "notice" && strings.Contains(r.text, "500 in") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the card was not rendered: %+v", m.chatRows)
	}
}
