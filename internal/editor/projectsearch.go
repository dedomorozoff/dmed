package editor

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
)

// Shared search machinery: one matcher for the in-file search (Ctrl+F) and
// for Find in Project (Ctrl+Shift+F), so smart case and regex mode behave
// identically in both. A literal query with no uppercase rune matches
// case-insensitively (smart case, like grep --smart-case); regex mode is
// toggled in the search prompt with Tab and follows the same rule by
// embedding (?i). An invalid regex pattern matches nothing — the hint says
// so — because silently degrading to a literal match would make the model
// (or the human) click through matches that are not there.

// searchMatcher is the TUI-free matcher used by the buffer search and the
// project walk.

// hasUpper reports whether the query contains an uppercase rune. Find in
// Project and the in-file search share the smart-case rule: a no-uppercase
// query is matched case-insensitively.
func hasUpper(query []rune) bool {
	for _, r := range query {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// foldEqual is the per-rune case-folding comparison of strings.EqualFold
// without folding the line into a new slice, so match columns stay valid.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for fa := unicode.SimpleFold(a); fa != a; fa = unicode.SimpleFold(fa) {
		if fa == b {
			return true
		}
	}
	return false
}

// matchLiteral finds every occurrence of query on one line, case-folded or not.
func matchLiteral(line, query []rune, insensitive bool) []searchMatch {
	if len(query) == 0 || len(line) < len(query) {
		return nil
	}
	var out []searchMatch
	for i := 0; i <= len(line)-len(query); i++ {
		matched := true
		for j, q := range query {
			r := line[i+j]
			if insensitive {
				if !foldEqual(r, q) {
					matched = false
					break
				}
			} else if r != q {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, searchMatch{col: i, length: len(query)})
			i += len(query) - 1
		}
	}
	return out
}

// matchRegexpLine converts the regexp's byte offsets into rune columns, which
// is what cursor/highlight code uses everywhere else.
func matchRegexpLine(re *regexp.Regexp, line string) []searchMatch {
	locs := re.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]searchMatch, 0, len(locs))
	prevByte, prevRune := 0, 0
	for _, loc := range locs {
		if loc[0] > prevByte {
			prevRune += utf8.RuneCountInString(line[prevByte:loc[0]])
		}
		// A zero-width match (e.g. `a*`) would highlight nothing; anchor
		// patterns silently produce those, so skip them.
		if loc[1] > loc[0] {
			out = append(out, searchMatch{
				col:    prevRune,
				length: utf8.RuneCountInString(line[loc[0]:loc[1]]),
			})
		}
		prevByte = loc[0]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// searchMatcher is the TUI-free matcher used by the buffer search and the
// project walk.
type searchMatcher struct {
	query   []rune
	insens  bool
	re      *regexp.Regexp // non-nil in regex mode with a valid pattern
	invalid bool           // regex mode with an un-compilable pattern
}

// newSearchMatcher builds the matcher for one query. In regex mode with an
// invalid pattern the matcher matches nothing and reports the reason.
func newSearchMatcher(query []rune, regex bool) *searchMatcher {
	sm := &searchMatcher{query: query, insens: !hasUpper(query)}
	if regex {
		pattern := string(query)
		if sm.insens {
			pattern = "(?i)" + pattern
		}
		if re, err := regexp.Compile(pattern); err == nil {
			sm.re = re
		} else {
			sm.invalid = true
		}
	}
	return sm
}

// match returns every match on one line (without a line number; callers add it).
func (sm *searchMatcher) match(line []rune) []searchMatch {
	if len(sm.query) == 0 || sm.invalid {
		return nil
	}
	if sm.re != nil {
		return matchRegexpLine(sm.re, string(line))
	}
	return matchLiteral(line, sm.query, sm.insens)
}

// matchRanges is the in-file searcher's view of one line: it uses the memoized
// regex (a regexp compiles once per query, not per rendered line).
func (m *Model) matchRanges(line []rune) []searchMatch {
	if len(m.searchQuery) == 0 {
		return nil
	}
	if m.searchRegex {
		if re := m.searchRegexCompiled(); re != nil {
			return matchRegexpLine(re, string(line))
		}
		return nil
	}
	return matchLiteral(line, m.searchQuery, !hasUpper(m.searchQuery))
}

// searchRegexCompiled memoizes the compiled query regex. It was worth the two
// fields: rendering compiles free of per-line regexp.Compile calls even inside
// a 10k-line find-in-file scan.
func (m *Model) searchRegexCompiled() *regexp.Regexp {
	q := string(m.searchQuery)
	if q == "" {
		return nil
	}
	if m.searchReQuery == q {
		return m.searchRe
	}
	pattern := q
	if !hasUpper(m.searchQuery) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	m.searchReQuery = q
	if err != nil {
		m.searchRe = nil
	} else {
		m.searchRe = re
	}
	return m.searchRe
}

// searchRegexBad reports whether regex mode is on with an un-compilable
// pattern, so the hint can say why nothing matches.
func (m Model) searchRegexBad() bool {
	return m.searchRegex && len(m.searchQuery) > 0 && m.searchRegexCompiled() == nil
}

// ---------- Find in Project ----------

const (
	// projSearchMaxHits caps the total result set: past that the panel stops
	// being human-navigable and the model cannot read it either.
	projSearchMaxHits = 500
	// projSearchBatchSz is how many hits one streamed message may carry.
	projSearchBatchSz = 60
	// projSearchVisible is the top of the results list shown at once.
	projSearchVisible = 10
	// projSearchMaxLine bounds one scanned line (bufio.Scanner needs an upper
	// buffer size; 1 MiB generously covers generated code).
	projSearchMaxLine = 1 << 20
)

// projSearchStop is only a stop sentinel for searchProject (filepath.Walk
// propagates fn errors); searchProject callers ignore it.
var projSearchStop = errors.New("project search: hit cap reached")

type projHit struct {
	path string // slash-separated path relative to the project root
	line int    // 0-based
	col  int    // rune column of the first match
	text string // the full matching line (truncated at render time)
}

// projSearchMsg streams one batch of hits to the model; done closes the scan.
type projSearchMsg struct {
	gen  int
	hits []projHit
	done bool
}

// startProjectSearch opens the panel in prompt mode, keeping any previous
// results visible until Enter re-runs them.
func (m *Model) startProjectSearch() {
	m.projSearchOpen = true
	m.projSearchInput = true
	m.projSearchSel = 0
	m.projSearchOff = 0
}

// runProjectSearch scans the project tree in the background and streams hits
// through projSearchCh. It never blocks the UI thread; a second search bumps
// the generation and orphans the first one, whose late batches are dropped.
func (m *Model) runProjectSearch() tea.Cmd {
	if len(m.projSearchQ) == 0 {
		return nil
	}
	m.projSearchGen++
	gen := m.projSearchGen
	m.projSearchHits = nil
	m.projSearchSel = 0
	m.projSearchOff = 0
	m.projSearchInput = false
	m.projSearchScan = true
	query := string(m.projSearchQ)
	regex := m.projSearchRegex
	skip := m.newProjectSkip()
	base := m.baseDir()
	ch := make(chan projSearchMsg, 8)
	m.projSearchCh = ch
	go debug.CapturePanicReport(func() {
		sm := newSearchMatcher([]rune(query), regex)
		var batch []projHit
		count := 0
		flush := func(done bool) {
			select {
			case ch <- projSearchMsg{gen: gen, hits: batch, done: done}:
			case <-time.After(2 * time.Second):
			}
			batch = nil
		}
		_ = searchProject(base, skip, sm, func(rel string, ln int, line []rune, hits []searchMatch) bool {
			for _, hit := range hits {
				batch = append(batch, projHit{
					path: rel,
					line: ln,
					col:  hit.col,
					text: string(line),
				})
				count++
			}
			if len(batch) >= projSearchBatchSz {
				flush(false)
			}
			return count < projSearchMaxHits
		})
		flush(true)
	})
	return waitForProjSearch(ch)
}

func waitForProjSearch(ch <-chan projSearchMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (m *Model) handleProjSearchMessage(msg projSearchMsg) {
	// Resending projSearchMsg without a generation guard would leave two
	// minutes of UI churn after Ctrl+Shift+F one query apart.
	if msg.gen != m.projSearchGen {
		return
	}
	m.projSearchHits = append(m.projSearchHits, msg.hits...)
	if msg.done {
		m.projSearchScan = false
		// Stop re-arming the reader on a finished channel: a later
		// waitForProjSearch(m.projSearchCh) would block forever on nothing.
		m.projSearchCh = nil
	}
}

// searchProject scans every readable file under root with the matcher, calling
// visit per matching line. It shares the walk and skip rules with the AI
// filesystem tools (walkProject + projectSkip). visit returns false to stop
// the scan; the stop reason is surfaced as the error, matching how
// filepath.Walk carries fn errors.
func searchProject(base string, skip projectSkip, sm *searchMatcher, visit func(rel string, ln int, line []rune, hits []searchMatch) bool) error {
	var stop error
	_ = walkProject(base, skip, func(p string, info os.FileInfo) error {
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil // unreadable: skip, keep walking
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), projSearchMaxLine)
		ln := 0
		for sc.Scan() {
			text := sc.Text()
			line := []rune(text)
			hits := sm.match(line)
			if len(hits) > 0 {
				if !visit(filepath.ToSlash(rel), ln, line, hits) {
					stop = projSearchStop
					return projSearchStop
				}
			}
			ln++
		}
		return nil
	})
	return stop
}

func (m *Model) handleProjSearch(msg tea.KeyPressMsg) tea.Cmd {
	if m.projSearchInput {
		return m.handleProjSearchInput(msg)
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.stopProjectSearch()
	case "up", "k", "ctrl+p":
		if n := len(m.projSearchHits); n > 0 {
			m.jumpProjSel((m.projSearchSel - 1 + n) % n)
		}
	case "pgup":
		m.jumpProjSel(m.projSearchSel - projSearchVisible)
	case "pgdown":
		m.jumpProjSel(m.projSearchSel + projSearchVisible)
	case "down", "j", "ctrl+n":
		if n := len(m.projSearchHits); n > 0 {
			m.jumpProjSel((m.projSearchSel + 1) % n)
		}
	case "enter":
		if i := m.projSearchSel; len(m.projSearchHits) > 0 && i < len(m.projSearchHits) {
			m.openProjHit(m.projSearchHits[i])
		}
	default:
		// Typing over the results goes back into the prompt with the query.
		if len(msg.Text) > 0 {
			m.projSearchInput = true
			m.projSearchQ = append(m.projSearchQ, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) handleProjSearchInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.stopProjectSearch()
	case "tab":
		m.projSearchRegex = !m.projSearchRegex
	case "enter":
		return m.runProjectSearch()
	case "backspace":
		if n := len(m.projSearchQ); n > 0 {
			m.projSearchQ = m.projSearchQ[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.projSearchQ = append(m.projSearchQ, []rune(msg.Text)...)
		}
	}
	return nil
}

// stopProjectSearch closes the panel. An in-flight scan is orphaned (its
// generation) rather than cancelled: the sweep is cheap and the model does not
// need to sit around waiting on a goroutine address.
func (m *Model) stopProjectSearch() {
	m.projSearchOpen = false
	m.projSearchGen++
	m.projSearchCh = nil
	m.projSearchScan = false
}

// jumpProjSel moves the selection to sel and scrolls the visible window so it
// stays in view.
func (m *Model) jumpProjSel(sel int) {
	n := len(m.projSearchHits)
	if sel < 0 {
		sel = 0
	} else if sel >= n {
		sel = n - 1
	}
	m.projSearchSel = sel
	if n <= projSearchVisible {
		m.projSearchOff = 0
		return
	}
	if m.projSearchSel > n-projSearchVisible {
		m.projSearchOff = n - projSearchVisible
	}
	if m.projSearchSel < m.projSearchOff {
		m.projSearchOff = m.projSearchSel
	}
	if m.projSearchSel >= m.projSearchOff+projSearchVisible {
		m.projSearchOff = m.projSearchSel - projSearchVisible + 1
	}
}

// openProjHit puts the cursor on the match and leaves the panel open: the
// next hit stays one Enter away.
func (m *Model) openProjHit(h projHit) {
	full := normalizePath(m.baseDir(), filepath.FromSlash(h.path))
	m.focusOrOpen(full)
	if t := m.cur(); t != nil {
		t.buf.SetCursor(h.line, h.col)
		t.buf.Deselect()
	}
	m.clampScroll()
}

// statusHint is the prompt-row suffix showing why the list looks as it does.
func (m Model) projSearchStatus() string {
	if m.projSearchScan {
		return m.t("psearch.scanning")
	}
	if len(m.projSearchHits) > 0 {
		return fmt.Sprintf(" [%d]", len(m.projSearchHits))
	}
	return m.t("psearch.no_matches")
}

func (m Model) projSearchHint() string {
	if m.projSearchInput {
		return m.t("psearch.hint_input")
	}
	return m.t("psearch.hint_results")
}
