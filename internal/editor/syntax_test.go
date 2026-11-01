package editor

import (
	"fmt"
	"strings"
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

// drainSyntax runs the highlighting pipeline like the editor does: a pass
// request wakes the UI thread, which starts the pass when the text has
// settled, and the finished pass arrives as a message.
func drainSyntax(m *Model, wait time.Duration) (syntaxReadyMsg, bool) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case msg := <-m.syntaxCh:
			switch mm := msg.(type) {
			case syntaxPassMsg:
				m.runSyntax(mm.tabIdx)
			case syntaxReadyMsg:
				return mm, true
			}
			continue
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	return syntaxReadyMsg{}, false
}

// drainSyntaxPart returns the next part of a highlighting pass.
func drainSyntaxPart(m *Model, wait time.Duration) (syntaxReadyMsg, bool) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case msg := <-m.syntaxCh:
			switch mm := msg.(type) {
			case syntaxPassMsg:
				m.runSyntax(mm.tabIdx)
			case syntaxReadyMsg:
				return mm, true
			}
			continue
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}
	return syntaxReadyMsg{}, false
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

	msg, ok := drainSyntax(&m, 5*time.Second)
	if !ok {
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

// TestSyntaxHighlightArrivesInParts: a pass publishes the lines it has
// tokenized while it runs, so the visible part of a big file gets its colors
// without waiting for the whole file.
func TestSyntaxHighlightArrivesInParts(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	m := New()
	m.width, m.height = 80, 24
	m.tabs = []tab{{buf: buffer.Load(sb.String()), path: "parts.go"}}
	m.initPanes()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	// The first part of the pass: the cache holds part of the lines and is
	// not complete yet.
	part, ok := drainSyntaxPart(&m, 5*time.Second)
	if !ok {
		t.Fatal("no part of the pass arrived")
	}
	if part.complete {
		t.Fatal("the first part must not be complete")
	}
	if len(part.lines) == 0 {
		t.Fatal("the part must carry lines")
	}
	next, _ = m.Update(part)
	m = next.(Model)
	if len(m.tabs[0].syntaxCached) == 0 {
		t.Fatal("the lines of the part must be stored")
	}
	if m.tabs[0].syntaxComplete {
		t.Fatal("the cache must not be complete yet")
	}

	// The rest of the pass completes the cache.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := drainSyntaxPart(&m, 100*time.Millisecond)
		if !ok {
			continue
		}
		next, _ = m.Update(msg)
		m = next.(Model)
		if msg.complete {
			if !m.tabs[0].syntaxComplete {
				t.Fatal("the cache must be complete after the last part")
			}
			if len(m.tabs[0].syntaxCached) < 2000 {
				t.Fatalf("the cache must cover the whole file, got %d lines", len(m.tabs[0].syntaxCached))
			}
			return
		}
	}
	t.Fatal("the pass never completed")
}
func TestSyntaxHighlightWaitsForQuiet(t *testing.T) {
	m := syntaxTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	// Type while the wait is running: every keystroke moves the text.
	deadline := time.Now().Add(syntaxPassDelay * 3)
	for time.Now().Before(deadline) {
		m.tabs[0].buf.InsertText("x")
		if _, ok := drainSyntax(&m, 10*time.Millisecond); ok {
			t.Fatal("a pass must not run while the buffer keeps changing")
		}
	}
	if m.tabs[0].syntaxCached != nil {
		t.Fatal("no highlight must have been computed while typing")
	}

	// Once the typing stops, the pass runs and lands.
	msg, ok := drainSyntax(&m, 5*time.Second)
	if !ok {
		t.Fatal("no highlighting pass arrived after the typing stopped")
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.tabs[0].syntaxCached == nil {
		t.Fatal("the finished pass must be stored")
	}
}
