package editor

import (
	"errors"
	"os"
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

func TestAssistantToolCallRendersAsCard(t *testing.T) {
	m := newChatModel()
	m.chatMsgs = []ai.Message{
		{Role: "assistant", Content: "I'll read it.", ToolCalls: []ai.ToolCall{{Name: "READ", Args: `{"arg":"main.go"}`}}},
	}
	m.rebuildChatRows()
	hasTool := false
	for _, r := range m.chatRows {
		if r.kind == "tool" || r.kind == "label-tool" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("expected tool card rows, got kinds: %v", chatRowKinds(m.chatRows))
	}
}

func TestToolResultRenderedAsCompactCard(t *testing.T) {
	m := newChatModel()
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("some line of file content\n")
	}
	m.chatMsgs = []ai.Message{{Role: "tool", ToolName: "READ", Content: "[READ x.go]\n" + sb.String()}}
	m.rebuildChatRows()
	toolLines := 0
	for _, r := range m.chatRows {
		if r.kind == "tool" {
			toolLines++
		}
	}
	if toolLines > 10 {
		t.Fatalf("tool result must be truncated, got %d tool lines", toolLines)
	}
}

func chatRowKinds(rows []chatRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.kind)
	}
	return out
}

// chatEditHarness builds a model rooted at dir with a fake provider so the
// native tool loop can be driven without touching the network.
func chatEditHarness(t *testing.T, dir string) Model {
	t.Helper()
	m := New()
	m.root = dir
	m.tabs = []tab{{buf: buffer.New()}}
	m.initPanes()
	m.ai = &fakeProvider{}
	return m
}

func TestChatEditApplyWritesFileAndOpensTab(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "existing.go"), "package old\n")

	m := chatEditHarness(t, dir)
	m.chatModel = "test-model"

	cmd := m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "EDIT", Args: `{"path":"existing.go","content":"package new\n"}`}})
	if !m.chatReviewMode {
		t.Fatal("edit must enter review mode")
	}
	if cmd != nil {
		t.Fatal("must pause for review, not continue the loop")
	}

	m.acceptChatReview(false)

	data, err := os.ReadFile(filepath.Join(dir, "existing.go"))
	if err != nil || string(data) != "package new\n" {
		t.Fatalf("file after accept = %q err=%v", string(data), err)
	}
	var found bool
	for _, tb := range m.tabs {
		if tb.path == filepath.Join(dir, "existing.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tab for edited file; tabs=%+v", m.tabs)
	}
	if len(m.chatMsgs) < 2 {
		t.Fatalf("assistant + tool result must be in history, got %d msgs", len(m.chatMsgs))
	}
}

func TestChatEditRejectDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "v1\n")

	m := chatEditHarness(t, dir)
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "EDIT", Args: `{"path":"a.go","content":"v2\n"}`}})
	if !m.chatReviewMode {
		t.Fatal("must enter review mode")
	}
	m.rejectChatReview()

	data, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil || string(data) != "v1\n" {
		t.Fatalf("rejected edit must not write: %q err=%v", string(data), err)
	}
	// The model should learn the edit was rejected.
	var sawRejected bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "rejected") {
			sawRejected = true
		}
	}
	if !sawRejected {
		t.Fatalf("tool result must note rejection: %+v", m.chatMsgs)
	}
}

func TestChatEditFocusOpenTabNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "v1\n")

	m := chatEditHarness(t, dir)
	m.openPath(filepath.Join(dir, "a.go"))
	before := len(m.tabs)

	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "EDIT", Args: `{"path":"a.go","content":"v2\n"}`}})
	m.acceptChatReview(false)

	if len(m.tabs) != before {
		t.Fatalf("editing an open file must not duplicate its tab: %d -> %d", before, len(m.tabs))
	}
	var content string
	for _, tb := range m.tabs {
		if tb.path == filepath.Join(dir, "a.go") {
			content = tb.buf.Text()
		}
	}
	if content != "v2\n" {
		t.Fatalf("open buffer must reload to new content, got %q", content)
	}
}

func TestChatNonEditToolsDoNotEnterReview(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	cmd := m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "RUN", Args: `{"arg":"echo hi"}`}})
	if m.chatReviewMode {
		t.Fatal("non-edit tools must not enter review mode")
	}
	if cmd == nil {
		t.Fatal("should continue the loop after non-edit tools")
	}
}

// TestToolRoundOpensCreatedAndModifiedFiles verifies that files created or
// rewritten during a tool round are auto-opened as tabs and summarized as
// "AI FILES" in the last tool result.
func TestToolRoundOpensCreatedAndModifiedFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "v1\n")

	m := chatEditHarness(t, dir)
	before := snapshotFiles(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []ai.Message{{Role: "tool", ToolName: "RUN", Content: "[RUN] ok"}}
	m.openTouchedFiles(before, results)

	var foundA, foundB bool
	for _, tb := range m.tabs {
		if tb.path == filepath.Join(dir, "a.txt") && tb.buf.Text() == "v2\n" {
			foundA = true
		}
		if tb.path == filepath.Join(dir, "b.txt") && tb.buf.Text() == "new\n" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("touched files must open as tabs; tabs=%+v", m.tabs)
	}
	if !strings.Contains(results[0].Content, "created: b.txt") ||
		!strings.Contains(results[0].Content, "modified: a.txt") {
		t.Fatalf("missing AI FILES summary: %q", results[0].Content)
	}
}

// TestToolRoundSkipsBinaryArtifact verifies that files with NUL bytes are not
// auto-opened as tabs and stay out of the AI FILES summary.
func TestToolRoundSkipsBinaryArtifact(t *testing.T) {
	dir := t.TempDir()
	m := chatEditHarness(t, dir)
	before := snapshotFiles(dir)
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("abc\x00def"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []ai.Message{{Role: "tool", ToolName: "RUN", Content: "[RUN] ok"}}
	m.openTouchedFiles(before, results)

	for _, tb := range m.tabs {
		if tb.path == filepath.Join(dir, "bin.dat") {
			t.Fatalf("binary artifact must not open a tab; tabs=%+v", m.tabs)
		}
	}
}

// TestAIFixCompletesIntoReview verifies that when the fix stream goroutine
// closes its channel the request transitions out of the busy state and into
// diff review instead of hanging.
func TestAIFixCompletesIntoReview(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.cur().buf = buffer.Load("hello world\n")
	m.aiFixOriginal = "hello world\n"
	m.aiFixBusy = true
	m.msg = "AI fixing..."

	ch := make(chan chatEvent, 1)
	m.aiFixCh = ch
	cmd := waitForFixOutput(ch)
	if cmd == nil {
		t.Fatal("waitForFixOutput should return a cmd for a live channel")
	}
	ch <- chatEvent{delta: "hello cruel world"}
	close(ch)
	msg1 := cmd().(FixOutputMsg)
	if msg1.Delta != "hello cruel world" || msg1.Done {
		t.Fatalf("first event = %+v", msg1)
	}
	cmd2 := m.handleFixOutput(msg1)
	if !m.aiFixBusy {
		t.Fatal("busy must stay set after a partial delta")
	}
	msg2 := cmd2().(FixOutputMsg)
	if !msg2.Done {
		t.Fatalf("closed channel must yield Done, got %+v", msg2)
	}
	m.handleFixOutput(msg2)
	if m.aiFixBusy {
		t.Fatal("busy must clear after stream completes")
	}
	if !m.aiFixReviewMode {
		t.Fatal("completed stream must enter review mode")
	}
}

// TestAIFixCancelWhileBusy verifies Esc aborts a streaming fix request and
// that a stale streamed output arriving afterwards is ignored.
func TestAIFixCancelWhileBusy(t *testing.T) {
	m := New()
	m.aiFixBusy = true
	canceled := false
	m.aiFixCancel = func() { canceled = true }
	ch := make(chan chatEvent, 1)
	m.aiFixCh = ch
	m.aiFixProposal = "partial"

	next := press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !canceled {
		t.Fatal("Esc must cancel the running request")
	}
	if next.aiFixBusy {
		t.Fatal("busy flag cleared on cancel")
	}
	if next.aiFixCh != nil {
		t.Fatal("channel must be cleared on cancel")
	}
	if next.aiFixProposal != "" {
		t.Fatalf("proposal must be cleared on cancel, got %q", next.aiFixProposal)
	}

	cmd := next.handleFixOutput(FixOutputMsg{Err: errors.New("context canceled")})
	if cmd != nil {
		t.Fatalf("stale output must be ignored after cancel, got cmd %v", cmd)
	}
}

// TestAIFixReviewCyrillicKeys verifies the y/n fix-review keys work in the
// Russian layout: «н» (physical Y) accepts, «т» (physical N) discards.
func TestAIFixReviewCyrillicKeys(t *testing.T) {
	m := New()
	m.cur().buf = buffer.Load("hello world\n")
	m.aiFixOriginal = "hello world\n"
	m.aiFixProposal = "hello brave world"
	m.startFixReview()
	if !m.aiFixReviewMode {
		t.Fatal("review mode not entered")
	}

	// «т» (physical N) must discard.
	m.handleFixReview(tea.KeyPressMsg{Text: "т"})
	if m.aiFixReviewMode {
		t.Fatal("«т» must close the review")
	}
	if m.cur().buf.Text() != "hello world\n" {
		t.Fatalf("«т» must leave the buffer untouched, got %q", m.cur().buf.Text())
	}

	// «н» (physical Y) must accept and apply.
	m.aiFixProposal = "hello brave world"
	m.startFixReview()
	m.handleFixReview(tea.KeyPressMsg{Text: "н"})
	if m.aiFixReviewMode {
		t.Fatal("«н» must close the review")
	}
	if !strings.Contains(m.cur().buf.Text(), "hello brave world") {
		t.Fatalf("«н» must apply the proposal, got %q", m.cur().buf.Text())
	}
}

// TestAIFixPromptTyping verifies the fix prompt accepts typed input and clears
// on Ctrl+U, and that Esc cancels without submitting.
func TestAIFixPromptTyping(t *testing.T) {
	m := New()
	m.cfg.AI.Model = "test-model"
	m.cur().buf = buffer.Load("x\n")
	m.cur().path = "fixture.go"
	m.startFixRequest()
	if !m.aiFixOpen {
		t.Fatal("fix prompt should be open")
	}
	for _, r := range "fix bugs" {
		m.handleFixRequest(tea.KeyPressMsg{Text: string(r)})
	}
	if string(m.aiFixInput) != "fix bugs" {
		t.Fatalf("input = %q", string(m.aiFixInput))
	}
	m.handleFixRequest(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.aiFixInput != nil {
		t.Fatal("Ctrl+U must clear the input")
	}
	m.handleFixRequest(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.aiFixOpen {
		t.Fatal("Esc must close the prompt")
	}
}
