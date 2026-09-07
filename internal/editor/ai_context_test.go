package editor

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

// TestInlineReviewCyrillicKeys verifies the y/n inline review keys work in the
// Russian layout: «н» (physical Y) accepts, «т» (physical N) discards.
func TestInlineReviewCyrillicKeys(t *testing.T) {
	m := New()
	b := buffer.Load("hello world\n")
	m.cur().buf = b
	m.cur().buf.SetCursor(0, 0)
	m.startInlineRequest()
	m.aiInlineProposal = "hello brave world"
	m.startInlineReview()
	if !m.aiReviewMode {
		t.Fatal("review mode not entered")
	}

	// «т» (physical N) must discard.
	m.handleInlineReview(tea.KeyPressMsg{Text: "т"})
	if m.aiReviewMode {
		t.Fatal("«т» must close the review")
	}
	if m.cur().buf.Text() != "hello world\n" {
		t.Fatalf("«т» must leave the buffer untouched, got %q", m.cur().buf.Text())
	}

	// «н» (physical Y) must accept and apply.
	m.aiInlineProposal = "hello brave world"
	m.startInlineReview()
	m.handleInlineReview(tea.KeyPressMsg{Text: "н"})
	if m.aiReviewMode {
		t.Fatal("«н» must close the review")
	}
	if !strings.Contains(m.cur().buf.Text(), "hello brave world") {
		t.Fatalf("«н» must apply the proposal, got %q", m.cur().buf.Text())
	}
}

// TestInlineCancelWhileBusy verifies Esc aborts a streaming inline request and
// that the stale streamed output arriving afterwards is ignored, not shown as
// an error.
func TestInlineCancelWhileBusy(t *testing.T) {
	m := New()
	m.aiInlineBusy = true
	canceled := false
	m.aiInlineCancel = func() { canceled = true }
	ch := make(chan chatEvent, 1)
	m.aiInlineCh = ch
	m.aiInlineProposal = "partial"
	m.msg = "AI: rewriting..."

	next := press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !canceled {
		t.Fatal("Esc must cancel the running request")
	}
	if next.aiInlineBusy {
		t.Fatal("busy flag cleared on cancel")
	}
	if next.aiInlineCh != nil {
		t.Fatal("channel must be cleared on cancel")
	}
	if next.aiInlineProposal != "" {
		t.Fatalf("proposal must be cleared on cancel, got %q", next.aiInlineProposal)
	}

	// A stale event buffered before the cancel must be swallowed.
	cmd := next.handleInlineOutput(InlineOutputMsg{Err: errors.New("context canceled")})
	if cmd != nil {
		t.Fatalf("stale output must be ignored after cancel, got cmd %v", cmd)
	}
	if next.msg != "" {
		t.Fatalf("stale error must not surface in the status line, got %q", next.msg)
	}
}

// TestInlineCompletesIntoReview verifies that when the stream goroutine closes
// its channel (ChatStream returned nil) the inline request transitions out of
// the busy state and into diff review instead of hanging at "AI: rewriting...".
func TestInlineCompletesIntoReview(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.cur().buf = buffer.Load("hello world\n")
	m.aiInlineOriginal = "hello world"
	m.aiInlineSelStart = [2]int{0, 0}
	m.aiInlineSelEnd = [2]int{0, len("hello world")}
	m.aiInlineBusy = true
	m.msg = "AI: rewriting..."

	// waitForInlineOutput must turn a closed channel into a Done event (this
	// was the bug: it returned nil, so the request hung forever). Feed one and
	// then close to mirror the goroutine's defer-close on success.
	ch := make(chan chatEvent, 1)
	m.aiInlineCh = ch
	cmd := waitForInlineOutput(ch)
	if cmd == nil {
		t.Fatal("waitForInlineOutput should return a cmd for a live channel")
	}
	ch <- chatEvent{delta: "hello cruel world"}
	close(ch)
	msg1 := cmd().(InlineOutputMsg)
	if msg1.Delta != "hello cruel world" || msg1.Done {
		t.Fatalf("first event = %+v", msg1)
	}
	cmd2 := m.handleInlineOutput(msg1)
	if !m.aiInlineBusy {
		t.Fatal("busy must stay set after a partial delta")
	}
	msg2 := cmd2().(InlineOutputMsg)
	if !msg2.Done {
		t.Fatalf("closed channel must yield Done, got %+v", msg2)
	}
	m.handleInlineOutput(msg2)
	if m.aiInlineBusy {
		t.Fatal("busy must clear after stream completes")
	}
	if !m.aiReviewMode {
		t.Fatal("completed stream must enter review mode")
	}
}

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

func TestRunChatToolsOpensRunCreatedFile(t *testing.T) {
	dir := t.TempDir()

	m := New()
	m.root = dir
	m.tabs = []tab{{buf: buffer.New()}}
	m.initPanes()

	newPath := filepath.Join(dir, "made.txt")
	rel := "made.txt"
	newCmd := "cmd /c type nul > " + filepath.ToSlash(rel)
	res, created, modified := m.runChatTools([]toolCall{{name: "RUN", arg: newCmd}})

	if len(created) != 1 || created[0] != "made.txt" {
		t.Fatalf("created = %v", created)
	}
	if len(modified) != 0 {
		t.Fatalf("modified = %v", modified)
	}
	if !strings.Contains(res, "created: made.txt") {
		t.Fatalf("results missing created summary:\n%s", res)
	}
	var found bool
	for _, tb := range m.tabs {
		if tb.path == newPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tab for RUN-created file; tabs=%+v", m.tabs)
	}
}
