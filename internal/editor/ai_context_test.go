package editor

import (
	"testing"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

func TestSurroundingContextSingleLine(t *testing.T) {
	b := buffer.Load("l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	before, after := surroundingContext(b, 4, 4)
	if before != "l1\nl2\nl3" {
		t.Fatalf("before = %q", before)
	}
	if after != "l5\nl6\nl7" {
		t.Fatalf("after = %q", after)
	}
}

func TestSurroundingContextClampsAtEdges(t *testing.T) {
	b := buffer.Load("l0\nl1\nl2\nl3\nl4\nl5\n")
	before, after := surroundingContext(b, 1, 1)
	if before != "l0" {
		t.Fatalf("before at start = %q", before)
	}
	if after != "l2\nl3\nl4" {
		t.Fatalf("after = %q", after)
	}

	before, after = surroundingContext(b, 5, 5)
	if before != "l2\nl3\nl4" {
		t.Fatalf("before at end = %q", before)
	}
	if after != "" {
		t.Fatalf("after at end = %q", after)
	}
}

func TestSurroundingContextMultiLineRegion(t *testing.T) {
	b := buffer.Load("a\nb\nc\nd\ne\nf\ng\n")
	before, after := surroundingContext(b, 2, 4)
	if before != "a\nb" {
		t.Fatalf("before = %q", before)
	}
	if after != "f\ng" {
		t.Fatalf("after = %q", after)
	}
}

func TestParseToolCallsIgnoresMarkdownFenceCode(t *testing.T) {
	// Code shown in markdown fences must not be mistaken for tool calls.
	reply := "```go\n=== TOOL: READ: x.go ===\n```\nThat was just code."
	tools := parseToolCalls(reply)
	if len(tools) != 0 {
		t.Fatalf("want no tools from fenced code, got %+v", tools)
	}
}

func TestChatToolResultRenderedAsToolTurn(t *testing.T) {
	m := newChatModel()
	m.chatMsgs = []ai.Message{
		{Role: "user", Content: "=== TOOL RESULT: READ: main.go ===\n[READ main.go]\nhi"},
	}
	m.rebuildChatRows()
	hasTool := false
	for _, r := range m.chatRows {
		if r.kind == "tool" || r.kind == "label-tool" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("expected tool kind rows, got kinds: %v", chatRowKinds(m.chatRows))
	}
}

func chatRowKinds(rows []chatRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.kind)
	}
	return out
}
