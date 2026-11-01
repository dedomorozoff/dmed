package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/agent"
)

func TestSubstituteLiteralHonoursSmartCase(t *testing.T) {
	line := "Hello hello HELLO"
	low := newSearchMatcher([]rune("hello"), false)
	got, n := low.substitute(line, "bye")
	if n != 3 {
		t.Fatalf("smart-case count = %d, want 3", n)
	}
	if got != "bye bye bye" {
		t.Fatalf("smart-case text = %q", got)
	}
	// A query with an uppercase rune is case sensitive, exactly as in match.
	up := newSearchMatcher([]rune("Hello"), false)
	got, n = up.substitute(line, "bye")
	if n != 1 || got != "bye hello HELLO" {
		t.Fatalf("case-sensitive: n=%d text=%q", n, got)
	}
	// The count is the number of splices, never the number of attempts.
	if got, n := low.substitute("nothing here", "x"); n != 0 || got != "nothing here" {
		t.Fatalf("no match: n=%d text=%q", n, got)
	}
	// An empty or invalid query rewrites nothing.
	if got, n := newSearchMatcher(nil, false).substitute(line, "x"); n != 0 || got != line {
		t.Fatalf("empty query: n=%d text=%q", n, got)
	}
	if got, n := newSearchMatcher([]rune("(["), true).substitute(line, "x"); n != 0 || got != line {
		t.Fatalf("invalid pattern: n=%d text=%q", n, got)
	}
}

func TestSubstituteRegexExpandsGroupsAndSkipsZeroWidth(t *testing.T) {
	re := newSearchMatcher([]rune(`([a-z]+)-([0-9]+)`), true)
	got, n := re.substitute("ab-12 cd-34", "X-$2")
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if got != "X-12 X-34" {
		t.Fatalf("text = %q, want \"X-12 X-34\"", got)
	}
	// ${1} is the same group in its braced form.
	got, n = re.substitute("ab-12", "[${1}${2}]")
	if n != 1 || got != "[ab12]" {
		t.Fatalf("braced group: n=%d text=%q", n, got)
	}
	// A zero-width pattern reports no matches in the Find panel, so the
	// replace skips those hits too: the count must equal the effect.
	got, n = newSearchMatcher([]rune(`x*`), true).substitute("a1b2c3", "-")
	if n != 0 || got != "a1b2c3" {
		t.Fatalf("zero-width: n=%d text=%q", n, got)
	}
}

func TestReplaceBytesKeepsEndingsAndExactBytes(t *testing.T) {
	sm := newSearchMatcher([]rune("hello"), false)

	// CRLF stays CRLF, byte for byte: the matcher never sees the terminator.
	got, n := replaceBytes([]byte("hello\r\nnope\r\nhello world\r\n"), sm, "hi")
	if n != 2 {
		t.Fatalf("CRLF count = %d, want 2", n)
	}
	if string(got) != "hi\r\nnope\r\nhi world\r\n" {
		t.Fatalf("CRLF text = %q", got)
	}
	// A file without a final newline does not gain one.
	got, n = replaceBytes([]byte("hello\nlast hello"), sm, "hi")
	if n != 2 || string(got) != "hi\nlast hi" {
		t.Fatalf("no final newline: n=%d text=%q", n, got)
	}
	// No match returns the very same bytes, so the caller can drop the file.
	data := []byte("nothing\n")
	if got, n := replaceBytes(data, sm, "hi"); n != 0 || string(got) != string(data) {
		t.Fatalf("no match: n=%d text=%q", n, got)
	}
	// Replacing a query with itself still counts the occurrences it rewrote.
	if got, n := replaceBytes([]byte("hello\n"), sm, "hello"); n != 1 || string(got) != "hello\n" {
		t.Fatalf("self replacement: n=%d text=%q", n, got)
	}
	// Multi-byte runes survive the rune-wise splice.
	got, n = replaceBytes([]byte("привет hello мир\n"), sm, "hi")
	if n != 1 || string(got) != "привет hi мир\n" {
		t.Fatalf("unicode: n=%d text=%q", n, got)
	}
}

// Counting occurrences is what an excluded preview row shows, so it has to
// follow exactly the rules the replacement uses.
func TestCountInMatchesTheReplace(t *testing.T) {
	sm := newSearchMatcher([]rune("hello"), false)
	data := []byte("hello\nnope\nhello hello\r\n")
	if got := sm.countIn(data); got != 3 {
		t.Fatalf("countIn = %d, want 3", got)
	}
	if _, n := replaceBytes(data, sm, "x"); n != 3 {
		t.Fatalf("replaceBytes counted %d, countIn counted 3", n)
	}
}

func writeTempBytes(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReplaceProjectBuildsSeriesAndMarksExclusions(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "hello\nnope\nhello world\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, filepath.Join("sub", "b.txt"), "hello there")
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, filepath.Join("vendor", "v.txt"), "hello vendored\n")
	writeTemp(t, dir, "pic.png", "hello in a png\n")
	writeTemp(t, dir, "dirty.txt", "hello unsaved\n")
	// UTF-16LE: the BOM makes it pass the text heuristics, but a byte-level
	// replace would shred it, so the scan keeps it out of the series.
	writeTempBytes(t, dir, "utf16.txt",
		append([]byte{0xFF, 0xFE}, []byte("h\x00e\x00l\x00l\x00o\x00\n\x00")...))

	skip := newProjectSkipFor(nil)
	sm := newSearchMatcher([]rune("hello"), false)
	var rows []projReplFile
	st := replaceProject(dir, skip, sm, "hi", func(rel string) bool {
		return rel == "dirty.txt"
	}, func(row projReplFile, _ *agent.Change) { rows = append(rows, row) })

	byPath := map[string]projReplFile{}
	for _, r := range rows {
		byPath[r.path] = r
	}
	// The vendored and binary files never reach the preview at all: the walk
	// already prunes them.
	if _, ok := byPath["vendor/v.txt"]; ok {
		t.Fatal("vendor/ leaked into the preview")
	}
	if _, ok := byPath["pic.png"]; ok {
		t.Fatal("a binary extension leaked into the preview")
	}
	if len(rows) != 4 {
		t.Fatalf("rows: %+v", rows)
	}
	a := byPath["a.txt"]
	if a.change == nil || a.count != 2 || a.skipped != "" {
		t.Fatalf("a.txt row: %+v", a)
	}
	// The change carries the exact disk bytes on both sides.
	if a.change.Orig != "hello\nnope\nhello world\n" || a.change.New != "hi\nnope\nhi world\n" {
		t.Fatalf("a.txt change: %+v", a.change)
	}
	if a.change.Path != "a.txt" || filepath.IsAbs(a.change.Path) {
		t.Fatalf("the change path must stay project-relative: %q", a.change.Path)
	}
	b := byPath["sub/b.txt"]
	if b.change == nil || b.count != 1 || b.change.New != "hi there" {
		t.Fatalf("sub/b.txt change: %+v", b.change)
	}
	d := byPath["dirty.txt"]
	if d.change != nil || d.skipped != projReplSkipDirty || d.count != 1 {
		t.Fatalf("a dirty file stays listed, excluded and counted: %+v", d)
	}
	u := byPath["utf16.txt"]
	if u.change != nil || u.skipped != projReplSkipEncoding {
		t.Fatalf("utf16 row: %+v", u)
	}
	if st.matches != 4 || st.excluded != 2 || st.truncated {
		t.Fatalf("stats: %+v", st)
	}

	// A replacement equal to the query rewrites nothing, so nothing is listed;
	// a dirty tab that holds none of the query is no noise either.
	clean := t.TempDir()
	writeTemp(t, clean, "a.txt", "hello\n")
	writeTemp(t, clean, "quiet.txt", "nothing to see\n")
	rows = nil
	st = replaceProject(clean, skip, sm, "hello", func(rel string) bool { return rel == "quiet.txt" },
		func(row projReplFile, _ *agent.Change) { rows = append(rows, row) })
	if len(rows) != 0 || st.matches != 0 || st.excluded != 0 {
		t.Fatalf("a no-op replacement: %+v stats=%+v", rows, st)
	}
}

func TestReplaceProjectTruncatesTheFileCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < projReplMaxFiles+5; i++ {
		writeTemp(t, dir, fmt.Sprintf("f%03d.txt", i), "x\n")
	}
	sm := newSearchMatcher([]rune("x"), false)
	var rows []projReplFile
	st := replaceProject(dir, newProjectSkipFor(nil), sm, "y", nil,
		func(row projReplFile, _ *agent.Change) { rows = append(rows, row) })
	if len(rows) != projReplMaxFiles {
		t.Fatalf("rows = %d, want the cap %d", len(rows), projReplMaxFiles)
	}
	if !st.truncated || st.matches != projReplMaxFiles {
		t.Fatalf("truncation must be reported, never silent: %+v", st)
	}
}

// startProjReplScan opens the panel the way the palette does, fills both
// fields and runs the scan, then drains the streamed preview.
func startProjReplScan(t *testing.T, query, repl string, m Model) Model {
	t.Helper()
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeStr(m, "replace in project")
	hits := m.filterPalette()
	idx := -1
	for i, h := range hits {
		if h.id == "proj_replace" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no replace-in-project command in the palette: %v", paletteIDs(hits))
	}
	m.paletteSel = idx
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.projSearchOpen || !m.projSearchInput || !m.projSearchRepl {
		t.Fatal("the panel must open in replace mode, on the query step")
	}
	m = typeStr(m, query)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.projReplStep != 1 {
		t.Fatalf("Enter on the query moves to the replacement step, step=%d", m.projReplStep)
	}
	m = typeStr(m, repl)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm, ok := next.(Model)
	if !ok {
		t.Fatal("expected Model back")
	}
	return drainProjRepl(t, mm, cmd)
}

func TestTabDirtyForExcludesUnsavedTabs(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "hello\n")
	writeTemp(t, dir, "b.txt", "hello\n")

	m := New(dir)
	m.width, m.height = 100, 30
	m.focusOrOpen(a)
	if m.cur() == nil || m.cur().path != a {
		t.Fatalf("the file must open as the active tab: %+v", m.cur())
	}
	m = typeStr(m, "X")
	if !m.cur().buf.Dirty() {
		t.Fatal("the edit must dirty the tab")
	}
	if !m.tabDirtyFor("a.txt") {
		t.Fatal("an edited tab must keep its own file out of the series")
	}
	if m.tabDirtyFor("b.txt") {
		t.Fatal("a file no tab holds stays in the series")
	}
	m = press(m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.tabDirtyFor("a.txt") {
		t.Fatal("a saved tab must not exclude its file")
	}
}

func TestReplaceProjectRegexGroups(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "codes.txt", "ab-12 cd-34\nnone\n")
	sm := newSearchMatcher([]rune(`([a-z]+)-([0-9]+)`), true)
	var rows []projReplFile
	replaceProject(dir, newProjectSkipFor(nil), sm, "X-$2", nil,
		func(row projReplFile, _ *agent.Change) { rows = append(rows, row) })
	if len(rows) != 1 || rows[0].change == nil {
		t.Fatalf("rows: %+v", rows)
	}
	if got := rows[0].change.New; got != "X-12 X-34\nnone\n" {
		t.Fatalf("group expansion in a file: %q", got)
	}
	if rows[0].count != 2 {
		t.Fatalf("count = %d, want 2", rows[0].count)
	}
}

func TestProjectReplacePanelFlowAppliesThroughApplier(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "hello\n")
	writeTemp(t, dir, "b.txt", "bye hello\n")

	base := New(dir)
	base.width, base.height = 100, 30
	m := startProjReplScan(t, "hello", "hi", base)
	if len(m.projReplFiles) != 2 || len(m.projReplOrder) != 2 {
		t.Fatalf("preview: %+v order=%v", m.projReplFiles, m.projReplOrder)
	}
	if !strings.Contains(m.projReplStatus(), "2 file") {
		t.Fatalf("the status row must count files and occurrences: %q", m.projReplStatus())
	}

	// A fake Applier proves the panel never writes by itself: the series lands
	// in the map while the disk keeps its original bytes.
	reads, writes := map[string]string{}, map[string]string{}
	for _, name := range []string{"a.txt", "b.txt"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		reads[filepath.Join(dir, name)] = string(b)
	}
	m.projReplApplier = &agent.Applier{
		Root:  dir,
		Read:  func(p string) (string, error) { return reads[p], nil },
		Write: func(p, content string) error { writes[p] = content; return nil },
	}

	// The panel and the review render through the new View() branches.
	list := stripANSI(m.View().Content)
	if !strings.Contains(list, "a.txt") || !strings.Contains(list, "×1") {
		t.Fatalf("the preview list must render the files and their counts:\n%s", list)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.projReplReview || len(m.projReplRows) == 0 {
		t.Fatalf("Enter must open the side-by-side review (review=%v rows=%d)",
			m.projReplReview, len(m.projReplRows))
	}
	screen := stripANSI(m.View().Content)
	if !strings.Contains(screen, "Replace preview") || !strings.Contains(screen, "hi") {
		t.Fatalf("the review must show the diff and its title:\n%s", screen)
	}
	if len(writes) != 0 {
		t.Fatalf("the review alone must not write: %v", writes)
	}
	if !strings.Contains(m.projReplReviewBottom(), "a.txt") {
		t.Fatalf("the bottom row names the file under review: %q", m.projReplReviewBottom())
	}

	// tab walks the series, y applies just the file under review.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.projReplRevIdx != 1 {
		t.Fatalf("tab moves to the next file, idx=%d", m.projReplRevIdx)
	}
	m = press(m, tea.KeyPressMsg{Code: 'y'})
	if len(writes) != 1 || writes[filepath.Join(dir, "b.txt")] != "bye hi\n" {
		t.Fatalf("y applies one file: %v", writes)
	}
	if len(m.projReplFiles) != 1 || len(m.projReplOrder) != 1 {
		t.Fatalf("an applied row leaves the preview: %+v", m.projReplFiles)
	}
	if m.projReplReview {
		t.Fatal("the review closes after an apply")
	}

	// n discards, then a runs the whole remaining series.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = press(m, tea.KeyPressMsg{Code: 'n'})
	if len(writes) != 1 {
		t.Fatalf("n must write nothing: %v", writes)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = press(m, tea.KeyPressMsg{Code: 'a'})
	if writes[filepath.Join(dir, "a.txt")] != "hi\n" {
		t.Fatalf("a applies every pending file: %v", writes)
	}
	if len(m.projReplFiles) != 0 {
		t.Fatalf("every applied row left the preview: %+v", m.projReplFiles)
	}
	for name, want := range map[string]string{"a.txt": "hello\n", "b.txt": "bye hello\n"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Fatalf("%s on disk = %q, want the untouched %q", name, b, want)
		}
	}

	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.projSearchOpen || m.projSearchRepl {
		t.Fatal("Esc must close the replace panel")
	}
}

func TestProjectReplaceStalePatchWritesNothing(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "hello\n")
	b := writeTemp(t, dir, "b.txt", "hello too\n")

	m := New(dir)
	m.width, m.height = 100, 30
	m = startProjReplScan(t, "hello", "hi", m)
	if len(m.projReplFiles) != 2 {
		t.Fatalf("preview: %+v", m.projReplFiles)
	}

	// The file moves on under the preview: one edit between the scan and the
	// answer is enough to reject the whole series.
	if err := os.WriteFile(a, []byte("hello edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.projReplReview {
		t.Fatal("expected the review to be open")
	}
	m = press(m, tea.KeyPressMsg{Code: 'a'})
	if !strings.Contains(m.msg, "stale patch") {
		t.Fatalf("the apply must report the Applier's refusal: %q", m.msg)
	}
	if m.projReplReview {
		t.Fatal("a refused series returns to the preview")
	}
	if len(m.projReplFiles) != 2 {
		t.Fatalf("the preview stays up so it can be re-run: %+v", m.projReplFiles)
	}
	for name, want := range map[string]string{"a.txt": "hello edited\n", "b.txt": "hello too\n"} {
		p := a
		if name == "b.txt" {
			p = b
		}
		content, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != want {
			t.Fatalf("a rejected series writes nothing; %s = %q, want %q", name, content, want)
		}
	}
}

// drainProjRepl runs the streaming command the scan returned, feeding every
// batch back through Update the way the program would.
func drainProjRepl(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 200; i++ {
		msg := cmd()
		pm, ok := msg.(projReplMsg)
		if !ok {
			t.Fatalf("expected a projReplMsg, got %T", msg)
		}
		next, more := m.Update(pm)
		nm, ok := next.(Model)
		if !ok {
			t.Fatal("expected Model back")
		}
		m = nm
		if !m.projReplScan {
			return m
		}
		cmd = more
	}
	t.Fatal("the scan never finished")
	return m
}

func paletteIDs(items []commandItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}
