package editor

import (
	"context"
	"strings"
	"testing"
	"time"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

// fakeProvider streams a fixed list of deltas then returns, standing in for an
// LLM provider so ghost-stream tests never touch the network.
type fakeProvider struct {
	pieces []string
	// usage, when set, is reported once per stream — the shape a server
	// answers with token counts takes.
	usage *ai.Usage
}

func (f *fakeProvider) Models(_ context.Context) ([]string, error) { return nil, nil }

func (f *fakeProvider) ChatStream(_ context.Context, _ ai.Request, h ai.Handler) error {
	for _, p := range f.pieces {
		if h.Delta != nil {
			h.Delta(p)
		}
	}
	if f.usage != nil && h.Usage != nil {
		h.Usage(*f.usage)
	}
	return nil
}

// TestGhostTriggerStreamsAndShows verifies ghostTrigger actually delivers
// GhostOutputMsg deltas into the model (the trigger is a tea.Cmd that reads the
// stream) and marks the ghost visible once the stream finishes.
func TestGhostTriggerStreamsAndShows(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.tabs = []tab{{buf: buffer.Load("func foo() {\n")}}
	m.initPanes()
	m.ai = &fakeProvider{pieces: []string{"fmt.Println()", "\n}"}}

	cmd := m.ghostTrigger()
	if cmd == nil {
		t.Fatal("ghostTrigger returned nil command")
	}

	for i := 0; i < 10; i++ {
		if cmd == nil {
			break
		}
		raw := cmd()
		if raw == nil {
			break
		}
		gm, ok := raw.(GhostOutputMsg)
		if !ok {
			t.Fatalf("trigger produced %T, want GhostOutputMsg", raw)
		}
		cmd = m.handleGhostOutput(gm)
	}

	if !m.ghostVisible {
		t.Fatalf("ghost should be visible after stream; lines=%v", m.ghostLines)
	}
	if got := strings.Join(m.ghostLines, "\n"); got != "fmt.Println()\n}" {
		t.Fatalf("ghost text = %q", got)
	}
}

// TestGhostTriggerNilProvider verifies ghostTrigger is a no-op when no AI
// provider is configured rather than crashing.
func TestGhostTriggerNilProvider(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.tabs = []tab{{buf: buffer.Load("x\n")}}
	m.initPanes()
	m.ai = nil // simulate a model without a provider
	if cmd := m.ghostTrigger(); cmd != nil {
		t.Fatal("ghostTrigger with nil provider should return nil command")
	}
}

// TestNewCreatesAIProvider verifies a fresh model has a provider configured so
// chat, inline, and ghost all work before the chat panel is ever opened.
func TestNewCreatesAIProvider(t *testing.T) {
	m := New()
	if m.ai == nil {
		t.Fatal("New() should create an AI provider")
	}
}

// TestApplyGhostInsertsContinuation verifies Tab (applyGhost) inserts the
// ghost continuation after the cursor.
func TestApplyGhostInsertsContinuation(t *testing.T) {
	m := New()
	tb := buffer.Load("func foo() {\n\n")
	m.tabs = []tab{{buf: tb}}
	m.initPanes()
	tb.SetCursor(1, 0)
	m.ghostRow = 1
	m.ghostCol = 0
	m.ghostLines = []string{"fmt.Println()", "}"}
	m.ghostVisible = true

	m.applyGhost()

	if m.ghostVisible || len(m.ghostLines) != 0 {
		t.Fatalf("ghost state not cleared after apply")
	}
	if got := tb.Text(); got != "func foo() {\nfmt.Println()\n}\n" {
		t.Fatalf("buffer after apply = %q", got)
	}
}

func TestIsCodeModel(t *testing.T) {
	for _, m := range []string{"qwen2.5-coder:7b", "deepseek-coder-v2", "codestral-latest", "Qwen3-Coder"} {
		if !isCodeModel(m) {
			t.Errorf("%s must read as a code model", m)
		}
	}
	for _, m := range []string{"llama3.2", "gpt-4o", "deepseek-chat"} {
		if isCodeModel(m) {
			t.Errorf("%s must not read as a code model", m)
		}
	}
}

func TestGhostMessagesFIMShape(t *testing.T) {
	msgs := ghostMessages("func foo() {\n  ", "\n}", "qwen2.5-coder")
	if len(msgs) != 2 {
		t.Fatalf("msgs = %+v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "PREFIX:\nfunc foo() {") {
		t.Fatalf("prefix lost: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "SUFFIX:\n\n}") {
		t.Fatalf("suffix lost: %q", msgs[1].Content)
	}
	// A chat model keeps the old prose frame: no SUFFIX section for a model
	// that was not trained to read one.
	msgs = ghostMessages("abc", "xyz", "llama3.2")
	if strings.Contains(msgs[1].Content, "SUFFIX") {
		t.Fatalf("chat model got the FIM frame: %q", msgs[1].Content)
	}
	if !strings.HasPrefix(msgs[1].Content, "Continue this code:") {
		t.Fatalf("chat model frame changed: %q", msgs[1].Content)
	}
}

func TestGhostContextSplit(t *testing.T) {
	m := New()
	m.tabs = []tab{{buf: buffer.Load("a\nb\nfunc f() {\n\tcursor here\nrest\n\nafter blank\n")}}
	m.initPanes()
	t2 := &m.tabs[0]
	// Place the caret mid "cursor here": line 3 is "	cursor here", so the
	// caret after the tab plus "cur" sits at rune column 4.
	t2.buf.SetCursor(3, 4)
	prefix, suffix := m.ghostContext()
	if !strings.HasSuffix(prefix, "cur") {
		t.Fatalf("prefix must end at the caret, got %q", prefix)
	}
	if !strings.HasPrefix(suffix, "sor here") {
		t.Fatalf("suffix must start at the caret, got %q", suffix)
	}
	if strings.Contains(suffix, "after blank") {
		t.Fatalf("the suffix must stop at the blank line, got %q", suffix)
	}
}

func TestGhostWindowFollowsRhythm(t *testing.T) {
	m := Model{}
	// No rhythm yet: the middle tier.
	if w := m.ghostWindow(); w != ghostMedWindow {
		t.Fatalf("cold window = %v", w)
	}
	fast := []time.Time{time.Now(), time.Now().Add(50 * time.Millisecond), time.Now().Add(90 * time.Millisecond)}
	m.ghostStamps = fast
	if w := m.ghostWindow(); w != ghostFastWindow {
		t.Fatalf("fast window = %v, want the long pause", w)
	}
	slow := []time.Time{time.Now(), time.Now().Add(700 * time.Millisecond), time.Now().Add(1500 * time.Millisecond)}
	m.ghostStamps = slow
	if w := m.ghostWindow(); w != ghostSlowWindow {
		t.Fatalf("slow window = %v, want the short pause", w)
	}
}

func TestScheduleGhostStampsAndGuards(t *testing.T) {
	m := New()
	m.tabs = []tab{{buf: buffer.Load("x\n")}}
	m.initPanes()
	m.ai = &fakeProvider{pieces: []string{"y"}}
	// GhostAuto default true comes from cfg; New loaded Defaults.
	cmd := m.scheduleGhost()
	if cmd == nil {
		t.Fatal("scheduleGhost with a provider must return the window timer")
	}
	gen := m.ghostGen
	// A stale tick does nothing; the current one asks for a suggestion.
	if c := m.handleGhostWait(ghostWaitMsg{gen: gen - 1}); c != nil {
		t.Fatal("a stale generation must not fire")
	}
	c := m.handleGhostWait(ghostWaitMsg{gen: gen})
	if c == nil {
		t.Fatal("the current generation must fire the request")
	}
	// A stale stream message must not append to the ghost.
	m.ghostText = "keep"
	m.handleGhostOutput(GhostOutputMsg{Delta: "stale", Gen: gen - 1})
	if m.ghostText != "keep" {
		t.Fatalf("stale delta painted: %q", m.ghostText)
	}

	// ghost_auto = false puts the typing trigger away.
	m.cfg.AI.GhostAuto = false
	if cmd := m.scheduleGhost(); cmd != nil {
		t.Fatal("ghost_auto = false must not schedule")
	}
	// The manual chord still works.
	m.ai = &fakeProvider{pieces: []string{"y"}}
	if cmd := m.ghostTrigger(); cmd == nil {
		t.Fatal("the explicit request must answer regardless")
	}
}
