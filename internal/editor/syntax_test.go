package editor

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
	"dmed/internal/syntax"
)

// syntaxTestModel builds a model with one Go file open.
func syntaxTestModel(t *testing.T) Model {
	t.Helper()
	m := New()
	m.width, m.height = 80, 24
	src := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	m.tabs = []tab{{buf: buffer.Load(src), path: "syntax_probe.go"}}
	m.initPanes()
	return m
}

// TestSyntaxHighlightIsAsynchronous: the editor must not tokenize the file
// on the UI thread. After an update the highlight is still missing, and it
// arrives later through the syntax channel.
func TestSyntaxHighlightIsAsynchronous(t *testing.T) {
	m := syntaxTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	if m.tabs[0].syntaxCached != nil {
		t.Fatal("the highlight must not be computed on the UI thread")
	}
	if !m.tabs[0].syntaxPending {
		t.Fatal("a background pass must have been requested")
	}

	// The pass lands as a message; the waiter delivers it.
	deadline := time.Now().Add(5 * time.Second)
	var msg syntaxReadyMsg
	for time.Now().Before(deadline) {
		select {
		case msg = <-m.syntaxCh:
		default:
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	if msg.text == "" {
		t.Fatal("no highlighting pass arrived")
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.tabs[0].syntaxCached == nil {
		t.Fatal("the finished pass must be stored")
	}
	if m.tabs[0].syntaxText != m.tabs[0].buf.Text() {
		t.Fatal("the stored highlight must match the buffer text")
	}
	if m.tabs[0].syntaxPending {
		t.Fatal("the tab must not be pending after the pass landed")
	}
}

// TestSyntaxHighlightFollowsEdits: an edit invalidates the highlight and a
// new pass is requested; a pass for outdated text is dropped instead of
// being painted on the wrong lines.
func TestSyntaxHighlightFollowsEdits(t *testing.T) {
	m := syntaxTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	// Pretend a pass landed for the current text.
	m.tabs[0].syntaxCached = []syntax.HighlightedLine{}
	m.tabs[0].syntaxText = m.tabs[0].buf.Text()
	m.tabs[0].syntaxPending = false

	// An edit makes the cached highlight outdated.
	m.tabs[0].buf.InsertText("// hello\n")
	next, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
	m = next.(Model)
	if !m.tabs[0].syntaxPending {
		t.Fatal("an edit must request a new pass")
	}

	// A pass for the old text arrives: it must be dropped.
	stale := syntaxReadyMsg{tabIdx: 0, text: "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n", lines: nil}
	next, _ = m.Update(stale)
	m = next.(Model)
	if m.tabs[0].syntaxCached != nil {
		t.Fatal("a pass for outdated text must be dropped")
	}
}
