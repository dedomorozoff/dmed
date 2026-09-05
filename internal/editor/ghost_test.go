package editor

import (
	"context"
	"strings"
	"testing"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

// fakeProvider streams a fixed list of deltas then returns, standing in for an
// LLM provider so ghost-stream tests never touch the network.
type fakeProvider struct {
	pieces []string
}

func (f *fakeProvider) Models(_ context.Context) ([]string, error) { return nil, nil }

func (f *fakeProvider) ChatStream(_ context.Context, _ []ai.Message, onDelta func(string)) error {
	for _, p := range f.pieces {
		onDelta(p)
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
	if cmd := m.ghostTrigger(); cmd != nil {
		t.Fatal("ghostTrigger with nil provider should return nil command")
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
