package editor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
)

func TestSmartCaseLiteralMatching(t *testing.T) {
	line := []rune("Hello hello HELLO")
	low := newSearchMatcher([]rune("hello"), false)
	got := low.match(line)
	if len(got) != 3 {
		t.Fatalf("smart case: want 3 matches for %q, got %d", line, len(got))
	}
	for i, want := range []int{0, 6, 12} {
		if got[i].col != want {
			t.Fatalf("col[%d] = %d, want %d", i, got[i].col, want)
		}
		if got[i].length != 5 {
			t.Fatalf("len[%d] = %d, want 5", i, got[i].length)
		}
	}
	// A query with an uppercase rune counts as case sensitive.
	up := newSearchMatcher([]rune("Hello"), false)
	got = up.match(line)
	if len(got) != 1 || got[0].col != 0 {
		t.Fatalf("case-sensitive query: want 1 match at col 0, got %v", got)
	}
}

func TestRegexMatcherAndInvalidPattern(t *testing.T) {
	line := []rune("a1b2c3")
	re := newSearchMatcher([]rune("[0-9]+"), true)
	got := re.match(line)
	if len(got) != 3 || got[0].col != 1 || got[2].col != 5 {
		t.Fatalf("regex matches: want cols 1,3,5; got %v", got)
	}
	for _, s := range got {
		if s.length != 1 {
			t.Fatalf("length = %d, want 1", s.length)
		}
	}
	// A zero-width pattern (possible only by hand in regex mode) highlights
	// nothing instead of producing a column with zero length.
	zero := newSearchMatcher([]rune("x*"), true)
	if got := zero.match(line); got != nil {
		t.Fatalf("zero-width pattern: want no matches, got %v", got)
	}
	// An invalid pattern matches nothing and flags itself.
	bad := newSearchMatcher([]rune("(["), true)
	if !bad.invalid || bad.match(line) != nil {
		t.Fatalf("invalid pattern: invalid=%v matches=%v", bad.invalid, bad.match(line))
	}
}

func TestSearchProjectWalkAndSkip(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "hello\nnope\nhello world\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, filepath.Join("sub", "b.txt"), "nothing\nhello again\n")
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, filepath.Join("node_modules", "x", "c.txt"), "hello hidden\n")

	skip := newProjectSkipFor(nil)
	sm := newSearchMatcher([]rune("hello"), false)
	var hits []projHit
	_ = searchProject(dir, skip, sm, func(rel string, ln int, line []rune, ms []searchMatch) bool {
		for _, m := range ms {
			hits = append(hits, projHit{path: rel, line: ln, col: m.col, text: string(line)})
		}
		return len(hits) < projSearchMaxHits
	})
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d: %v", len(hits), hits)
	}
	// Skipped directories stay out of the results.
	for _, h := range hits {
		if strings.Contains(h.path, "node_modules") {
			t.Fatalf("skipped dir leaked into results: %s", h.path)
		}
	}
	// Order follows the walk: a.txt first, sub/b.txt last.
	if hits[0].path != "a.txt" || hits[0].line != 0 || hits[0].col != 0 {
		t.Fatalf("first hit: %+v", hits[0])
	}
	if hits[2].path != "sub/b.txt" || hits[2].line != 1 || hits[2].col != 0 {
		t.Fatalf("last hit: %+v", hits[2])
	}
}

func TestSearchProjectCapStopsTheWalk(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "x\nx\n")
	writeTemp(t, dir, "b.txt", "x\n")
	skip := newProjectSkipFor(nil)
	sm := newSearchMatcher([]rune("x"), false)
	calls := 0
	err := searchProject(dir, skip, sm, func(rel string, ln int, line []rune, ms []searchMatch) bool {
		calls += len(ms)
		return calls < 2 // cap reached after the first x
	})
	if err == nil {
		t.Fatal("cap must stop the walk with an error")
	}
	if calls != 2 {
		t.Fatalf("visit ran %d times past the cap", calls)
	}
}

func TestBufferReplaceAllRegexGroups(t *testing.T) {
	b := buffer.Load("ab-12 cd-34\nnone here\n")
	re, err := regexp.Compile(`[a-z]-([0-9]+)`)
	if err != nil {
		t.Fatal(err)
	}
	count := b.ReplaceAllRE(re, "X-$1")
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	// The pattern matches "b-12" and "d-34"; $1 keeps the digit group.
	if got := b.Text(); got != "aX-12 cX-34\nnone here\n" {
		t.Fatalf("text = %q", got)
	}
	// The whole replacement is one undo step.
	b.Undo()
	if got := b.Text(); got != "ab-12 cd-34\nnone here\n" {
		t.Fatalf("after undo: %q", got)
	}
	// A pattern with no matches leaves the buffer untouched and returns 0.
	if count := b.ReplaceAllRE(regexp.MustCompile(`q9z-[0-9]+`), "no"); count != 0 {
		t.Fatalf("no-match count = %d", count)
	}
}

func TestRegexReplaceAllViaReplacePrompt(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "re.txt", "ab-12 cd-34\n")
	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m = typeStr(m, `([a-z]+)-([0-9]+)`)
	// Tab toggles regex mode; the matcher switches from literal (0 matches —
	// the raw pattern is not in the file) to regex (2).
	if m.searchTotalMatches != 0 {
		t.Fatalf("literal count before Tab: matches = %d, want 0", m.searchTotalMatches)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.searchRegex {
		t.Fatal("Tab must toggle regex mode on")
	}
	if m.searchTotalMatches != 2 {
		t.Fatalf("regex matches = %d, want 2", m.searchTotalMatches)
	}
	m = press(m, tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if !m.replaceOpen {
		t.Fatal("expected replace prompt open after Ctrl+H")
	}
	// startReplace puts the focus on the replace field already.
	m = typeStr(m, `X-$2`)
	m = press(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if got := m.cur().buf.Text(); got != "X-12 X-34\n" {
		t.Fatalf("regex replace all: %q", got)
	}
}

func TestProjectSearchPanelFlow(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "hello\n")
	writeTemp(t, dir, "b.txt", "bye\n")

	m := New(dir) // directory root: the finder indexes the whole project
	m.width, m.height = 80, 24

	// Open via the palette (the Ctrl+Shift+F binding is not reproducible on
	// legacy terminals, the palette route is the portable one).
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeStr(m, "find in project")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "proj_search" {
		titles := make([]string, 0, 3)
		for _, h := range hits[:min(3, len(hits))] {
			titles = append(titles, m.cmdTitle(h))
		}
		t.Fatalf("palette top hits: %v", titles)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.projSearchOpen || !m.projSearchInput {
		t.Fatal("panel must open in prompt mode")
	}

	// Run the search; Enter returns the streaming command.
	m = typeStr(m, "hello")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, ok := next.(Model)
	if !ok {
		t.Fatal("expected Model back")
	}
	if !m.projSearchScan {
		t.Fatal("a scan must start")
	}
	for i := 0; cmd != nil && i < 100; i++ {
		msg := cmd()
		if pm, ok := msg.(projSearchMsg); ok {
			next, cmd = m.Update(pm)
			m, ok = next.(Model)
			if !ok {
				t.Fatal("expected Model back")
			}
			continue
		}
		break
	}
	if m.projSearchScan {
		t.Fatal("scan must be done")
	}
	if len(m.projSearchHits) != 1 || m.projSearchHits[0].path != filepath.FromSlash("a.txt") ||
		m.projSearchHits[0].line != 0 || m.projSearchHits[0].col != 0 {
		t.Fatalf("hits: %+v", m.projSearchHits)
	}

	// Open the hit and land on the match; the panel stays available for the
	// next hit.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	tt := m.cur()
	if tt == nil || tt.buf.CurLine() != 0 || tt.buf.Col() != 0 {
		t.Fatalf("cursor after open: cur=%v line=%d col=%d", tt != nil, tt.buf.CurLine(), tt.buf.Col())
	}
	if !m.projSearchOpen {
		t.Fatal("panel stays open for the next hit")
	}

	// Esc closes, and a scan cut short does not leak batches into the UI.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.projSearchOpen {
		t.Fatal("Esc must close the panel")
	}
}
