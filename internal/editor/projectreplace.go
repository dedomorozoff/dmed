package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/agent"
	"dmed/internal/debug"
	"dmed/internal/vcs"
)

// Replace in Project (Ctrl+Shift+R): the counterpart of Find in Project, and the
// human's version of the project rule. It never writes a file itself: the scan
// builds one whole-file agent.Change per matching file, the panel previews that
// list, and a diff review gates the apply through internal/agent.Applier — which
// validates every change against the current on-disk content and writes
// all-or-nothing. A batch rename answered "no", or one that went stale halfway,
// therefore leaves no half-renamed tree behind.

const (
	// projReplMaxFiles bounds the series: past a couple hundred files the diff
	// review is not a review any more. Occurrences are deliberately not capped —
	// a repo-wide rename of a thousand hits is one file list either way.
	projReplMaxFiles = 200
	// projReplBatchSz is how many rows one streamed message may carry.
	projReplBatchSz = 20
)

// Why a matching file stayed out of the series. Such a file keeps its row, with
// its occurrence count and a mark: a preview that quietly omits files is a
// preview the user cannot trust.
const (
	projReplSkipDirty    = "dirty"    // an open tab holds unsaved edits for it
	projReplSkipEncoding = "encoding" // not UTF-8 (a byte-level replace would mangle it)
	projReplSkipRead     = "unreadable"
)

// projReplFile is one preview row. change is nil for an excluded file.
type projReplFile struct {
	path    string // slash-separated, relative to the project root
	count   int    // occurrences the replacement would rewrite
	skipped string // "" or one of the projReplSkip* marks
	change  *agent.Change
}

// projReplStats is what the scan counted, carried on the final message.
type projReplStats struct {
	matches   int  // files holding at least one occurrence, excluded ones too
	excluded  int  // rows kept out of the series
	truncated bool // the file cap stopped the walk
}

// projReplMsg streams one batch of preview rows to the model; done closes the
// scan and carries the totals.
type projReplMsg struct {
	gen   int
	rows  []projReplFile
	stats projReplStats
	done  bool
}

// replaceProject walks the project and builds the series. It is TUI-free so the
// whole replacement rule can be tested without a terminal. dirty reports the
// files an open tab still holds unsaved edits for, and visit sees one row per
// matching file (with a nil change for an excluded one).
func replaceProject(base string, skip projectSkip, sm *searchMatcher, repl string,
	dirty func(rel string) bool, visit func(projReplFile, *agent.Change)) projReplStats {
	var st projReplStats
	_ = walkProject(base, skip, func(p string, info os.FileInfo) error {
		if st.matches >= projReplMaxFiles {
			st.truncated = true
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		// The exact bytes matter twice: the replacement must not restripe the
		// line endings, and Applier.Validate compares Orig against the disk.
		data, err := os.ReadFile(p)
		if err != nil {
			st.matches++
			st.excluded++
			visit(projReplFile{path: rel, skipped: projReplSkipRead}, nil)
			return nil
		}
		if _, enc := detectFileInfo(data); enc != "utf-8" {
			// Listed with no count: for UTF-16 the query bytes never appear
			// contiguously, so countIn would read "no occurrences" for a file
			// that does hold them. A file this scan refuses to touch is always
			// worth seeing.
			st.matches++
			st.excluded++
			visit(projReplFile{path: rel, skipped: projReplSkipEncoding}, nil)
			return nil
		}
		if dirty != nil && dirty(rel) {
			// A dirty tab is UTF-8 by definition here, so the count is
			// trustworthy and an unrelated file stays out of the way.
			if n := sm.countIn(data); n > 0 {
				st.matches++
				st.excluded++
				visit(projReplFile{path: rel, count: n, skipped: projReplSkipDirty}, nil)
			}
			return nil
		}
		out, count := replaceBytes(data, sm, repl)
		if count == 0 || string(out) == string(data) {
			return nil
		}
		st.matches++
		change := &agent.Change{Path: rel, Orig: string(data), New: string(out)}
		visit(projReplFile{path: rel, count: count, change: change}, change)
		return nil
	})
	return st
}

// replaceBytes applies the matcher to one file, line by line, and returns the
// new bytes with the number of occurrences rewritten. Lines keep their own
// terminator: a project-wide replace must not turn a CRLF file into LF, and the
// matcher never sees the ending, exactly as Find in Project scans.
func replaceBytes(data []byte, sm *searchMatcher, repl string) ([]byte, int) {
	var (
		out   strings.Builder
		count int
	)
	out.Grow(len(data))
	for _, chunk := range splitKeepEndings(data) {
		body, ending := splitEnding(chunk)
		nb, n := sm.substitute(body, repl)
		count += n
		out.WriteString(nb)
		out.WriteString(ending)
	}
	if count == 0 {
		return data, 0
	}
	return []byte(out.String()), count
}

// splitKeepEndings breaks content into lines with their terminator attached.
func splitKeepEndings(data []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i+1]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// splitEnding separates one line chunk into its text and its terminator.
func splitEnding(chunk string) (body, ending string) {
	switch {
	case strings.HasSuffix(chunk, "\r\n"):
		return chunk[:len(chunk)-2], chunk[len(chunk)-2:]
	case strings.HasSuffix(chunk, "\n"):
		return chunk[:len(chunk)-1], chunk[len(chunk)-1:]
	default:
		return chunk, ""
	}
}

// ---------- Preview scan ----------

// startProjectReplace opens the shared panel in replace mode. The query comes
// over from the last Find in Project (or the in-file search), because a replace
// almost always follows a search the user has just read.
func (m *Model) startProjectReplace() {
	m.projSearchOpen = true
	m.projSearchInput = true
	m.projSearchRepl = true
	m.projReplStep = 0
	if len(m.projSearchQ) == 0 && len(m.searchQuery) > 0 {
		m.projSearchQ = append([]rune(nil), m.searchQuery...)
	}
	// One generation covers both scans, and switching flavours orphans whatever
	// was in flight: a late find batch must not append hits into the replace
	// preview (or the other way round), because the row renderer would then
	// index the wrong list.
	m.projSearchGen++
	m.projSearchCh = nil
	m.projReplCh = nil
	m.projSearchScan = false
	m.projReplScan = false
	m.projSearchHits = nil
	m.projSearchSel = 0
	m.projSearchOff = 0
	m.projReplFiles = nil
	m.projReplStats = projReplStats{}
	m.projReplOrder = nil
	m.projReplReview = false
	m.projReplRows = nil
	m.projReplLeft = nil
	m.projReplRight = nil
}

// runProjectReplace scans the tree in the background and streams the preview.
func (m *Model) runProjectReplace() tea.Cmd {
	if len(m.projSearchQ) == 0 {
		return nil
	}
	m.projSearchGen++
	gen := m.projSearchGen
	m.projSearchCh = nil
	m.projReplFiles = nil
	m.projReplStats = projReplStats{}
	m.projReplOrder = nil
	m.projSearchSel = 0
	m.projSearchOff = 0
	m.projSearchInput = false
	m.projReplStep = 2
	m.projReplScan = true
	query := string(m.projSearchQ)
	repl := string(m.projReplWith)
	regex := m.projSearchRegex
	skip := m.newProjectSkip()
	base := m.baseDir()
	dirty := m.tabDirtyFor
	ch := make(chan projReplMsg, 8)
	m.projReplCh = ch
	go debug.CapturePanicReport(func() {
		sm := newSearchMatcher([]rune(query), regex)
		var rows []projReplFile
		var st projReplStats
		flush := func(done bool) {
			select {
			case ch <- projReplMsg{gen: gen, rows: rows, stats: st, done: done}:
			case <-time.After(2 * time.Second):
			}
			rows = nil
		}
		st = replaceProject(base, skip, sm, repl, dirty, func(row projReplFile, c *agent.Change) {
			rows = append(rows, row)
			if len(rows) >= projReplBatchSz {
				flush(false)
			}
		})
		flush(true)
	})
	return waitForProjRepl(ch)
}

func waitForProjRepl(ch <-chan projReplMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

// handleProjReplMessage merges one streamed batch. The generation guard is the
// shared projSearchGen: both panel flavours are stamped from it, so a superseded
// scan is dropped whole.
func (m *Model) handleProjReplMessage(msg projReplMsg) {
	if msg.gen != m.projSearchGen {
		return
	}
	m.projReplFiles = append(m.projReplFiles, msg.rows...)
	m.rebuildProjReplOrder()
	if msg.done {
		m.projReplStats = msg.stats
		m.projReplScan = false
		m.projReplCh = nil
	}
}

// rebuildProjReplOrder recomputes which preview rows carry a change: the review
// overlay walks that subset, so it has to follow the list after a partial apply.
func (m *Model) rebuildProjReplOrder() {
	m.projReplOrder = m.projReplOrder[:0]
	for i := range m.projReplFiles {
		if m.projReplFiles[i].change != nil {
			m.projReplOrder = append(m.projReplOrder, i)
		}
	}
}

// tabDirtyFor reports whether an open tab holds unsaved edits for this
// project-relative path. Such a file is kept out of the series: the Applier
// compares against the disk, so writing it would land under a buffer that still
// believes it owns the file, and the watcher would answer with a conflict prompt.
func (m *Model) tabDirtyFor(rel string) bool {
	full := normalizePath(m.baseDir(), filepath.FromSlash(rel))
	for i := range m.tabs {
		t := &m.tabs[i]
		if t.path == "" || t.buf == nil || !t.buf.Dirty() {
			continue
		}
		if normalizePath(m.baseDir(), t.path) == full {
			return true
		}
	}
	return false
}

// ---------- Panel keys ----------

// handleProjReplInput drives the two-step prompt: the query first, then the
// replacement. Tab stays the regex toggle it is in Find in Project so both
// panels behave alike; the steps are what separates the fields.
func (m *Model) handleProjReplInput(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("proj-repl-input", s) {
	case "proj-repl-input.close":
		m.stopProjectSearch()
	case "proj-repl-input.toggle-regex":
		m.projSearchRegex = !m.projSearchRegex
	case "proj-repl-input.run":
		if m.projReplStep == 0 {
			if len(m.projSearchQ) == 0 {
				return nil
			}
			m.projReplStep = 1
			return nil
		}
		return m.runProjectReplace()
	case "proj-repl-input.erase":
		if m.projReplStep == 0 {
			if n := len(m.projSearchQ); n > 0 {
				m.projSearchQ = m.projSearchQ[:n-1]
			}
		} else if n := len(m.projReplWith); n > 0 {
			m.projReplWith = m.projReplWith[:n-1]
		}
	case paneUnbound:
		return nil
	default:
		if len(msg.Text) > 0 {
			if m.projReplStep == 0 {
				m.projSearchQ = append(m.projSearchQ, []rune(msg.Text)...)
			} else {
				m.projReplWith = append(m.projReplWith, []rune(msg.Text)...)
			}
		}
	}
	return nil
}

// handleProjReplList navigates the preview. Nothing here writes: Enter opens the
// diff review of the selected file, and the y/a answer belongs to that review.
func (m *Model) handleProjReplList(s string, msg tea.KeyPressMsg) tea.Cmd {
	n := len(m.projReplFiles)
	switch m.paneCmd("proj-repl-list", s) {
	case "proj-repl-list.close":
		m.stopProjectSearch()
	case "proj-repl-list.up":
		if n > 0 {
			m.jumpProjSel((m.projSearchSel - 1 + n) % n)
		}
	case "proj-repl-list.down":
		if n > 0 {
			m.jumpProjSel((m.projSearchSel + 1) % n)
		}
	case "proj-repl-list.page-up":
		m.jumpProjSel(m.projSearchSel - projSearchVisible)
	case "proj-repl-list.page-down":
		m.jumpProjSel(m.projSearchSel + projSearchVisible)
	case "proj-repl-list.open":
		if idx := m.projSearchSel; n > 0 && idx < n {
			m.startProjReplReview(idx)
		}
	case paneUnbound:
		return nil
	default:
		// Typing over the preview goes back to the prompt with the query kept.
		if len(msg.Text) > 0 {
			m.projSearchInput = true
			m.projReplStep = 0
			m.projSearchQ = append(m.projSearchQ, []rune(msg.Text)...)
		}
	}
	return nil
}

// ---------- Diff review ----------

// startProjReplReview opens the side-by-side review of one previewed file.
func (m *Model) startProjReplReview(row int) {
	if row < 0 || row >= len(m.projReplFiles) || m.projReplFiles[row].change == nil {
		m.msg = m.t("msg.projr_nothing")
		return
	}
	idx := m.seriesIndexOf(row)
	if idx < 0 {
		return
	}
	m.projReplRevIdx = idx
	m.projReplReview = true
	m.loadProjReplChange(idx)
}

// seriesIndexOf maps a preview row to its position in the series (the rows that
// carry a change), or -1.
func (m *Model) seriesIndexOf(row int) int {
	for i, r := range m.projReplOrder {
		if r == row {
			return i
		}
	}
	return -1
}

// projReplChange returns the change at one position of the series.
func (m *Model) projReplChange(idx int) *agent.Change {
	if idx < 0 || idx >= len(m.projReplOrder) {
		return nil
	}
	return m.projReplFiles[m.projReplOrder[idx]].change
}

// loadProjReplChange renders the diff of one change in the series.
func (m *Model) loadProjReplChange(idx int) {
	c := m.projReplChange(idx)
	if c == nil {
		return
	}
	orig, prop := c.Orig, c.New
	// The diff is rendered line-wise, so a file without a final newline gains
	// one here only: Orig and New keep their exact bytes for the Applier.
	if !strings.HasSuffix(orig, "\n") {
		orig += "\n"
	}
	if !strings.HasSuffix(prop, "\n") {
		prop += "\n"
	}
	m.projReplRows = vcs.SideBySide(orig, prop)
	m.projReplLeft = strings.Split(strings.TrimRight(orig, "\n"), "\n")
	m.projReplRight = strings.Split(strings.TrimRight(prop, "\n"), "\n")
	m.projReplOffY = 0
	m.projReplOffX = 0
}

func (m *Model) handleProjReplReview(s string) tea.Cmd {
	n := len(m.projReplOrder)
	switch m.paneCmd("proj-repl-review", s) {
	case "proj-repl-review.accept":
		m.acceptProjRepl(true)
	case "proj-repl-review.accept-all":
		m.acceptProjRepl(false)
	case "proj-repl-review.reject":
		m.rejectProjRepl()
	case "proj-repl-review.next-file":
		if n > 0 {
			m.projReplRevIdx = (m.projReplRevIdx + 1) % n
			m.loadProjReplChange(m.projReplRevIdx)
			m.msg = ""
		}
	case "proj-repl-review.prev-file":
		if n > 0 {
			m.projReplRevIdx = (m.projReplRevIdx - 1 + n) % n
			m.loadProjReplChange(m.projReplRevIdx)
			m.msg = ""
		}
	case paneUnbound:
		return nil
	default:
		m.scrollProjReplReview(s)
	}
	return nil
}

// scrollProjReplReview is the pan/scroll part of the review keys. It stays local
// to this pane instead of being shared with the four older review panes: those
// keep their own scroll arithmetic, and pulling them together is its own change.
func (m *Model) scrollProjReplReview(s string) {
	half := m.paneViewHeight(m.activePane) / 2
	maxOff := len(m.projReplRows) - 1
	switch m.paneCmd("proj-repl-scroll", s) {
	case "proj-repl-scroll.up":
		if m.projReplOffY > 0 {
			m.projReplOffY--
		}
	case "proj-repl-scroll.down":
		if m.projReplOffY < maxOff {
			m.projReplOffY++
		}
	case "proj-repl-scroll.page-up":
		m.projReplOffY -= half
		if m.projReplOffY < 0 {
			m.projReplOffY = 0
		}
	case "proj-repl-scroll.page-down":
		m.projReplOffY += half
		if m.projReplOffY > maxOff {
			m.projReplOffY = maxOff
		}
	case "proj-repl-scroll.top":
		m.projReplOffY = 0
	case "proj-repl-scroll.bottom":
		if maxOff > 0 {
			m.projReplOffY = maxOff
		}
	case "proj-repl-scroll.scroll-left":
		m.projReplOffX -= 8
		if m.projReplOffX < 0 {
			m.projReplOffX = 0
		}
	case "proj-repl-scroll.scroll-right":
		m.projReplOffX += 8
	}
}

// acceptProjRepl lands the series through the Applier: one file when only is
// true, every pending file otherwise. Applied rows leave the preview, so a
// partial answer still shows what is left to review.
func (m *Model) acceptProjRepl(only bool) {
	var series []agent.Change
	var rows []int
	occurrences := 0
	for i, rowIdx := range m.projReplOrder {
		if only && i != m.projReplRevIdx {
			continue
		}
		c := m.projReplFiles[rowIdx].change
		if c == nil {
			continue
		}
		series = append(series, *c)
		rows = append(rows, rowIdx)
		occurrences += m.projReplFiles[rowIdx].count
	}
	if len(series) == 0 {
		m.endProjReplReview()
		return
	}

	ap := m.projReplApplier
	if ap == nil {
		ap = agent.NewApplier()
		// The same containment the agent tasks get: every path in the series is
		// relative to the project root, so nothing lands outside it.
		ap.Root = m.baseDir()
		m.projReplApplier = ap
	}
	if err := ap.Apply(series); err != nil {
		// Nothing was written: Validate rejected the series (or a write failed
		// and the earlier ones rolled back). The preview stays up, because the
		// fix is usually to save the file the message names and re-run.
		m.msg = m.t("msg.projr_apply_fail", err.Error())
		m.endProjReplReview()
		return
	}

	for i := range series {
		m.openAiFile(filepath.ToSlash(series[i].Path), true)
	}
	m.dropProjReplRows(rows)
	m.refreshGitFiles()
	m.msg = m.t("msg.projr_applied", len(series), occurrences)
	m.endProjReplReview()
}

// dropProjReplRows removes applied rows from the preview by row index, highest
// first so the remaining indices stay valid.
func (m *Model) dropProjReplRows(rows []int) {
	sorted := append([]int(nil), rows...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	for _, r := range sorted {
		if r < 0 || r >= len(m.projReplFiles) {
			continue
		}
		m.projReplFiles = append(m.projReplFiles[:r], m.projReplFiles[r+1:]...)
	}
	m.rebuildProjReplOrder()
	m.jumpProjSel(m.projSearchSel)
}

// rejectProjRepl discards the pending series and returns to the preview list.
func (m *Model) rejectProjRepl() {
	m.endProjReplReview()
	m.msg = m.t("msg.projr_discarded")
}

func (m *Model) endProjReplReview() {
	m.projReplReview = false
	m.projReplRows = nil
	m.projReplLeft = nil
	m.projReplRight = nil
	m.projReplOffY = 0
	m.projReplOffX = 0
}

// projReplOccurrences is how many rewrites the pending series carries.
func (m Model) projReplOccurrences() int {
	n := 0
	for _, i := range m.projReplOrder {
		n += m.projReplFiles[i].count
	}
	return n
}

// projReplExcluded counts the preview rows kept out of the series.
func (m Model) projReplExcluded() int {
	n := 0
	for i := range m.projReplFiles {
		if m.projReplFiles[i].change == nil {
			n++
		}
	}
	return n
}

// ---------- Panel text ----------

func (m Model) projReplStatus() string {
	if m.projReplScan {
		return m.t("psearch.repl_scanning")
	}
	if len(m.projReplFiles) == 0 {
		if len(m.projSearchQ) > 0 && m.projReplStep == 2 {
			return m.t("psearch.no_matches")
		}
		return ""
	}
	status := m.t("psearch.repl_status", len(m.projReplOrder), m.projReplOccurrences())
	if excl := m.projReplExcluded(); excl > 0 {
		status += m.t("psearch.repl_skipped", excl)
	}
	if m.projReplStats.truncated {
		status += m.t("psearch.repl_truncated", projReplMaxFiles)
	}
	return status
}

func (m Model) projReplHint() string {
	if m.projSearchInput {
		if m.projReplStep == 0 {
			return m.t("psearch.hint_repl_query")
		}
		return m.t("psearch.hint_repl_with")
	}
	return m.t("psearch.hint_repl_files")
}

// projReplSkipLabel names why a file is in the list but not in the series.
func (m Model) projReplSkipLabel(skipped string) string {
	switch skipped {
	case projReplSkipDirty:
		return m.t("psearch.skip_dirty")
	case projReplSkipEncoding:
		return m.t("psearch.skip_encoding")
	case projReplSkipRead:
		return m.t("psearch.skip_unreadable")
	default:
		return skipped
	}
}

func (m Model) projReplRow(i int) string {
	f := m.projReplFiles[i]
	text := fmt.Sprintf(" %s  ×%d", f.path, f.count)
	if f.skipped != "" {
		text += " [" + m.projReplSkipLabel(f.skipped) + "]"
	}
	label := padTo(text, m.width)
	switch {
	case i == m.projSearchSel:
		return statusHiStyle.Render(label)
	case f.skipped != "" && f.count > 0:
		// An excluded file is painted as a hint: it is listed to be seen, not
		// to be applied.
		return hintStyle.Render(label)
	default:
		return statusStyle.Render(label)
	}
}

// projReplPromptLine renders the replace-with field.
func (m Model) projReplPromptLine() string {
	line := statusHiStyle.Render(m.t("psearch.repl_with")) + statusStyle.Render(string(m.projReplWith))
	if m.projSearchInput && m.projReplStep == 1 {
		line += cursorStyle.Render(" ")
	} else {
		line += statusStyle.Render(" ")
	}
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// ---------- Review bottom row ----------

func (m Model) projReplReviewBottom() string {
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.projReplRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	var path string
	if c := m.projReplChange(m.projReplRevIdx); c != nil {
		path = c.Path
	}
	n := len(m.projReplOrder)
	title := " " + m.t("projr.review_title") + ": " + m.fitPath(path, 30)
	if n > 1 {
		title += fmt.Sprintf(" (%d/%d)", m.projReplRevIdx+1, n)
	}
	line := statusHiStyle.Render(title) +
		hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render(m.t("projr.review_hint"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}
