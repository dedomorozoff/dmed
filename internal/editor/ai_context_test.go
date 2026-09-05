package editor

import (
	"path/filepath"
	"strings"
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

func TestRunChatToolsOpensTabsAndSummarizesFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "existing.go"), "package old\n")

	m := New()
	m.root = dir
	m.tabs = []tab{{buf: buffer.New()}}
	m.initPanes()

	res, created, modified := m.runChatTools([]toolCall{
		{name: "EDIT", arg: "created.go", body: "package created\n"},
		{name: "EDIT", arg: "existing.go", body: "package new\n"},
	})

	if len(created) != 1 || created[0] != "created.go" {
		t.Fatalf("created = %v", created)
	}
	if len(modified) != 1 || modified[0] != "existing.go" {
		t.Fatalf("modified = %v", modified)
	}
	if !strings.Contains(res, "created: created.go") || !strings.Contains(res, "modified: existing.go") {
		t.Fatalf("results missing file summary:\n%s", res)
	}
	// A tab must exist for the created file and show its content.
	var foundCreated, foundModified bool
	for _, tb := range m.tabs {
		if tb.path == filepath.Join(dir, "created.go") && tb.buf.Text() == "package created\n" {
			foundCreated = true
		}
		if tb.path == filepath.Join(dir, "existing.go") && tb.buf.Text() == "package new\n" {
			foundModified = true
		}
	}
	if !foundCreated {
		t.Fatalf("no tab for created file; tabs=%+v", m.tabs)
	}
	if !foundModified {
		t.Fatalf("no tab for modified file; tabs=%+v", m.tabs)
	}
}

func TestRunChatToolsFocusOpenTabNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "v1\n")

	m := New()
	m.root = dir
	m.openPath(filepath.Join(dir, "a.go"))
	before := len(m.tabs)

	m.runChatTools([]toolCall{{name: "EDIT", arg: "a.go", body: "v2\n"}})

	if len(m.tabs) != before {
		t.Fatalf("editing an open file must not duplicate its tab: %d -> %d", before, len(m.tabs))
	}
}
