package editor

import (
	"strings"
	"testing"
	"time"

	"dmed/internal/syntax"
)

// runSyntaxPasses drains the highlighting pipeline until it goes quiet: pass
// requests start passes, ready messages are applied to the model — the same
// work Update does, without the timing flakiness of waiting on the UI loop.
func (m *Model) runSyntaxPasses() {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-m.syntaxCh:
			switch mm := msg.(type) {
			case syntaxPassMsg:
				m.runSyntax(mm.tabIdx)
			case syntaxReadyMsg:
				m.applySyntax(mm)
			}
			continue
		default:
		}
		// Quiet and no pending pass: done.
		quiet := true
		for i := range m.tabs {
			if m.tabs[i].syntaxPending {
				quiet = false
				break
			}
		}
		if quiet {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestSyntaxIncrementalCacheAdopted drives two passes through the model and
// checks the tab adopts the cache the first pass produced, so the second pass
// (after an edit) runs incrementally rather than cold.
func TestSyntaxIncrementalCacheAdopted(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "package main\n\nfunc main() {\n}\n")
	m.openPath(p)

	m.requestSyntax()
	m.runSyntaxPasses()
	// The pass ran; the tab holds the cache it produced.
	tab := m.cur()
	if tab.inc == nil {
		t.Fatal("the first pass left no incremental cache on the tab")
	}
	if tab.incLang != "go" {
		t.Fatalf("cache language = %q, want go", tab.incLang)
	}

	// A second pass after an edit must reuse the cache: drive it by hand and
	// count the reused lines.
	text := strings.Replace(tab.buf.Text(), "main()", "main() /* x */", 1)
	_, reused := tab.inc.Update(p, text, m.syn)
	if reused == 0 {
		t.Fatalf("second pass reused %d lines", reused)
	}
}

// TestSyntaxIncrementalLanguageChange checks the cache is dropped when the
// file's language differs from the one the cache was built for.
func TestSyntaxIncrementalLanguageChange(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.py", "def f():\n    pass\n")
	m.openPath(p)
	tab := m.cur()

	// Hand the tab a cache stamped with the wrong language, then run a pass.
	tab.inc = syntax.NewIncremental()
	tab.incLang = "go"
	m.requestSyntax()
	m.runSyntaxPasses()
	if tab.incLang != "python" {
		t.Fatalf("cache language = %q, want python after the re-pass", tab.incLang)
	}
}
