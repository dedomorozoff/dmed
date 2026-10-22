package editor

import "sort"

// Indentation-based code folding. A foldable block starts at any non-blank
// line whose next non-blank line is indented deeper, and ends at the last
// non-blank line before the indentation drops back to the start level —
// the same heuristic most lightweight editors use when no syntax tree is
// available. Folding is display-only state: it never touches the buffer,
// so undo/redo are untouched and applied agent diffs go through review as
// before.
//
// The display pipeline renders every pane from a *display row list* (see
// dispRows): the buffer lines that are visible under the folded set, plus a
// synthetic summary row after every folded block's start line. Rendering,
// mouse hit-testing, wheel scrolling and cursor clamping all share that one
// list so screen rows and buffer lines never disagree.

// foldRange is one foldable block: lines start+1..end are hidden when the
// block is folded; the start line stays visible and carries the toggle.
type foldRange struct {
	start, end int // 0-based buffer lines, inclusive
}

// foldRanges returns the foldable blocks of the tab's buffer. Results are
// cached against the buffer text and tab width exactly like the wrap
// segments, so an unchanged buffer re-uses the previous scan.
func (t *tab) foldRanges(tabW int) []foldRange {
	text := t.buf.Text()
	if t.foldCached != nil && t.foldText == text && t.foldTabW == tabW {
		return t.foldCached
	}
	n := t.buf.LineCount()
	// Expanded indent of each line, or -1 when the line is blank. The width
	// is read in place: copying every line out of the buffer would allocate
	// once per line of the file on every edit.
	ind := make([]int, n)
	for i := 0; i < n; i++ {
		ind[i] = t.buf.IndentWidth(i, tabW)
	}
	// One pass with a stack of open blocks: a block starts at a line whose
	// next non-blank line is indented deeper and ends at the last non-blank
	// line before the indentation falls back to its own level. Pushing and
	// popping keeps the scan linear even in deeply nested files.
	type open struct {
		start, indent int
	}
	var stack []open
	var rs []foldRange
	lastNonBlank := -1
	for i := 0; i < n; i++ {
		if ind[i] < 0 {
			continue // blank line: it belongs to the block around it
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= ind[i] {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if lastNonBlank > top.start {
				rs = append(rs, foldRange{start: top.start, end: lastNonBlank})
			}
		}
		stack = append(stack, open{start: i, indent: ind[i]})
		lastNonBlank = i
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if lastNonBlank > top.start {
			rs = append(rs, foldRange{start: top.start, end: lastNonBlank})
		}
	}
	// The stack pops blocks innermost first; sort them back into line order.
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	// Drop folded markers whose block no longer exists: the buffer was edited
	// under the folded set, and such a mark would hide nothing while making
	// the folded count lie about the file.
	for s := range t.folds {
		i := sort.Search(len(rs), func(i int) bool { return rs[i].start >= s })
		if i >= len(rs) || rs[i].start != s {
			delete(t.folds, s)
		}
	}
	t.foldCached = rs
	t.foldText = text
	t.foldTabW = tabW
	return rs
}

// hasFolds reports whether the buffer provides anything to fold at all: the
// gutter only grows a chevron column when it does.
func (t *tab) hasFolds(tabW int) bool { return len(t.foldRanges(tabW)) > 0 }

// foldAtStart returns the fold range starting exactly at ln. The ranges are
// sorted by start line, so the lookup is a binary search: the gutter paints a
// chevron on every visible row, and a linear scan there would make rendering
// quadratic in the number of foldable blocks.
func (t *tab) foldAtStart(ln, tabW int) (foldRange, bool) {
	rs := t.foldRanges(tabW)
	i := sort.Search(len(rs), func(i int) bool { return rs[i].start >= ln })
	if i < len(rs) && rs[i].start == ln {
		return rs[i], true
	}
	return foldRange{}, false
}

// foldStateAt returns the range starting at ln only when that block is
// currently folded; a stale folded marker on a line that is no longer
// foldable is dropped so it heals itself on the next render.
func (t *tab) foldStateAt(ln, tabW int) (foldRange, bool) {
	r, ok := t.foldAtStart(ln, tabW)
	if !ok || !t.folds[ln] {
		return foldRange{}, false
	}
	return r, true
}

// foldHidden reports whether ln lies strictly inside a currently folded
// block, returning its outermost such block's start and end; it is what
// actually hides the line.
func (t *tab) foldHidden(ln, tabW int) (int, int, bool) {
	start, end := 0, -1
	for _, r := range t.foldRanges(tabW) {
		if !t.folds[r.start] || r.start >= ln || ln > r.end {
			continue
		}
		if end < 0 || r.start < start {
			start, end = r.start, r.end
		}
	}
	if end < 0 {
		return 0, 0, false
	}
	return start, end, true
}

// toggleFoldAt flips the folded state of the block starting at ln. Returns
// false when there is nothing to fold there.
func (t *tab) toggleFoldAt(ln, tabW int) bool {
	if _, ok := t.foldAtStart(ln, tabW); !ok {
		return false
	}
	if t.folds[ln] {
		delete(t.folds, ln)
		if len(t.folds) == 0 {
			t.folds = nil
		}
	} else {
		if t.folds == nil {
			t.folds = map[int]bool{}
		}
		t.folds[ln] = true
	}
	return true
}

// unfoldThrough removes every folded block that hides ln, so the cursor line
// is guaranteed visible afterwards. One pass over the sorted ranges instead
// of a lookup per folded block.
func (t *tab) unfoldThrough(ln, tabW int) {
	for _, r := range t.foldRanges(tabW) {
		if r.start < ln && ln <= r.end && t.folds[r.start] {
			delete(t.folds, r.start)
		}
	}
}

// foldAll folds every foldable block in the tab.
func (t *tab) foldAll(tabW int) {
	t.folds = map[int]bool{}
	for _, r := range t.foldRanges(tabW) {
		t.folds[r.start] = true
	}
}

// unfoldAll clears the folded set.
func (t *tab) unfoldAll() { t.folds = nil }

// foldChevron returns the gutter fold column glyph for a normal row: ▾ on an
// unfolded block's start line, ▸ while the block is folded, blank elsewhere.
// hasCol is false when the whole tab has no foldable blocks; nothing is
// painted in the fold column in that case.
func (m Model) foldChevron(t *tab, ln, tabW int) (string, bool) {
	if !t.hasFolds(tabW) {
		return " ", false
	}
	if r, ok := t.foldAtStart(ln, tabW); ok && r.end > ln {
		if t.folds[ln] {
			return m.g.expand, true
		}
		return m.g.collapse, true
	}
	return " ", true
}

// foldCloseAtCursor folds the block starting on the cursor line (vim zc).
func (m *Model) foldCloseAtCursor() {
	t := m.cur()
	if t == nil {
		return
	}
	ln := t.buf.CurLine()
	if _, ok := t.foldAtStart(ln, m.cfg.Editor.TabWidth); ok {
		if t.folds == nil {
			t.folds = map[int]bool{}
		}
		t.folds[ln] = true
		m.clampScroll()
		return
	}
	m.msg = m.t("msg.no_fold")
}

// foldOpenAtCursor expands the folded state at the cursor line: the block it
// starts, or the ancestor block hiding it (vim zo).
func (m *Model) foldOpenAtCursor() {
	t := m.cur()
	if t == nil {
		return
	}
	ln := t.buf.CurLine()
	tabW := m.cfg.Editor.TabWidth
	if _, ok := t.foldStateAt(ln, tabW); ok {
		t.toggleFoldAt(ln, tabW)
		m.clampScroll()
		return
	}
	if _, _, hid := t.foldHidden(ln, tabW); hid {
		t.unfoldThrough(ln, tabW)
		m.clampScroll()
		return
	}
	m.msg = m.t("msg.no_fold")
}

// toggleFoldAtCursor flips the fold of the block starting on the cursor line
// and reports the outcome for the status line.
func (m *Model) toggleFoldAtCursor() {
	t := m.cur()
	if t == nil {
		return
	}
	tabW := m.cfg.Editor.TabWidth
	if ln := t.buf.CurLine(); t.toggleFoldAt(ln, tabW) {
		return
	}
	m.msg = m.t("msg.no_fold")
}

// toggleFoldAll folds (or, when everything is already folded, unfolds) every
// block of the active tab.
func (m *Model) toggleFoldAll() {
	t := m.cur()
	if t == nil {
		return
	}
	tabW := m.cfg.Editor.TabWidth
	if len(t.folds) >= len(t.foldRanges(tabW)) && len(t.folds) > 0 {
		t.unfoldAll()
		m.msg = m.t("msg.unfold_all")
		return
	}
	t.foldAll(tabW)
	m.msg = m.t("msg.fold_all")
}

// foldSkip keeps the cursor outside hidden lines after a vertical move: a
// cursor that landed inside (or straight into) a folded block is placed on
// the block's nearest visible edge in the move direction. Selections stay
// untouched so a drag across a folded block reveals it via clampScroll.
func (m *Model) foldSkip(dir int) {
	t := m.cur()
	if t == nil || t.buf.HasSelection() {
		return
	}
	st, en, hid := t.foldHidden(t.buf.CurLine(), m.cfg.Editor.TabWidth)
	if !hid {
		return
	}
	target := en + 1
	if dir < 0 {
		target = st
	}
	if n := t.buf.LineCount(); target > n-1 {
		target = n - 1
	}
	if target < 0 {
		target = 0
	}
	t.buf.SetCursor(target, t.buf.Col())
}

// dispRow is one screen row of a pane. Normal rows carry the buffer line ln;
// summary rows render "… N hidden lines" for the block of the start line ln.
// In wrap mode each visible line contributes as many rows as it has wrap
// segments (expStart..expEnd); in plain mode a line yields exactly one row
// covering the whole line.
type dispRow struct {
	ln       int  // buffer line this row belongs to
	summary  bool // synthetic "N hidden lines" row
	hidden   int  // hidden line count (summary rows)
	expStart int  // wrap segment bounds; 0..len for plain rows
	expEnd   int
}

// foldScanner walks the folded blocks in ascending start order and answers
// two questions per line: is the line hidden inside a folded block, and does
// it start one. Both pointers only move forward, so a full pass over the
// document stays linear.
type foldScanner struct {
	iv  []foldRange
	hi  int // interval that may cover the current line
	fsi int // next interval whose start is not before the current line
}

// at reports the visibility of line for the scanner's current position.
func (s *foldScanner) at(line int) (hidden, foldedStart bool) {
	for s.hi < len(s.iv) && s.iv[s.hi].end < line {
		s.hi++
	}
	for s.fsi < len(s.iv) && s.iv[s.fsi].start < line {
		s.fsi++
	}
	if s.hi < len(s.iv) && s.iv[s.hi].start < line && line <= s.iv[s.hi].end {
		return true, false
	}
	return false, s.fsi < len(s.iv) && s.iv[s.fsi].start == line
}

// dispRows builds the pane's display row list under the current folded set.
// contentW is the pane content width. The scan is linear in the number of
// lines and fold blocks — rendering, hit-testing and cursor clamping call
// this several times per keystroke, so a quadratic scan here is felt
// immediately as input lag.
func (m Model) dispRows(paneIdx, contentW int) []dispRow {
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	tabW := m.cfg.Editor.TabWidth
	rs := t.foldRanges(tabW)

	// The currently folded blocks, ascending by start line. Markers whose
	// block no longer exists are pruned by foldRanges when it rescans.
	iv := make([]foldRange, 0, len(t.folds))
	for _, r := range rs {
		if t.folds[r.start] {
			iv = append(iv, r)
		}
	}

	if !p.wordWrap {
		n := t.buf.LineCount()
		// Count the rows first so the slice is allocated once and exactly:
		// a heavily folded file has far fewer rows than it has lines.
		hidden, summaries := 0, 0
		openEnd := -1
		for _, r := range iv {
			if r.start > openEnd { // outermost folded block
				summaries++
				hidden += r.end - r.start
				openEnd = r.end
			}
		}
		rows := make([]dispRow, 0, n-hidden+summaries)
		sc := foldScanner{iv: iv}
		for ln := 0; ln < n; {
			hid, foldedStart := sc.at(ln)
			if hid {
				ln++ // inside a folded block: not shown, and no summary
				continue
			}
			rows = append(rows, dispRow{ln: ln, expEnd: t.buf.LineLen(ln)})
			if foldedStart {
				rows = append(rows, dispRow{ln: ln, summary: true, hidden: iv[sc.fsi].end - ln})
				ln = iv[sc.fsi].end + 1
				continue
			}
			ln++
		}
		return rows
	}

	segs := t.tabWrap(contentW, tabW)
	// Same two passes as above: count the visible segments and the summary
	// rows, then fill a slice of exactly that size.
	visSegs, summaries := 0, 0
	count := foldScanner{iv: iv}
	for i := 0; i < len(segs); i++ {
		line := segs[i].line
		hid, foldedStart := count.at(line)
		if hid {
			continue
		}
		visSegs++
		if foldedStart && (i+1 >= len(segs) || segs[i+1].line != line) {
			summaries++
		}
	}
	rows := make([]dispRow, 0, visSegs+summaries)
	sc := foldScanner{iv: iv}
	for i := 0; i < len(segs); i++ {
		sg := segs[i]
		hid, foldedStart := sc.at(sg.line)
		if hid {
			continue
		}
		rows = append(rows, dispRow{ln: sg.line, expStart: sg.expStart, expEnd: sg.expEnd})
		if foldedStart && (i+1 >= len(segs) || segs[i+1].line != sg.line) {
			rows = append(rows, dispRow{ln: sg.line, summary: true, hidden: iv[sc.fsi].end - sg.line})
		}
	}
	return rows
}

// dispIndexOfLine returns the index of the first display row showing the
// start of buffer line ln (not a summary, not a wrap continuation), or -1
// when ln is hidden.
func dispIndexOfLine(d []dispRow, ln int) int {
	lo, hi := 0, len(d)-1
	// Display rows are ordered by (ln, expStart), summaries included.
	for lo <= hi {
		mid := (lo + hi) / 2
		r := d[mid]
		switch {
		case r.ln < ln || (r.ln == ln && r.summary):
			lo = mid + 1
		case r.ln > ln:
			hi = mid - 1
		default: // r.ln == ln, not a summary; rows share the expStart order
			if r.expStart > 0 {
				// A continuation of ln sorts before? No: same ln, larger
				// expStart comes later. Search left for the first segment.
				hi = mid - 1
				continue
			}
			return mid
		}
	}
	return -1
}

// dispCursorRow finds the display row index holding the cursor (line + column)
// so scrolling keeps it in view; it prefers the exact wrap segment. Falls back
// to the nearest visible row when the cursor sits on a hidden line.
func dispCursorRow(d []dispRow, ln, expCol int) int {
	if ln < 0 || len(d) == 0 {
		return 0
	}
	if ln > d[len(d)-1].ln {
		return len(d) - 1
	}
	if i := dispIndexOfLine(d, ln); i >= 0 {
		if expCol == 0 {
			return i
		}
		// Look through the line's segments for the one containing expCol.
		for ; i < len(d) && d[i].ln == ln; i++ {
			if d[i].summary {
				break
			}
			if expCol >= d[i].expStart && expCol < d[i].expEnd {
				return i
			}
		}
		return i - 1
	}
	// Hidden (or out of list): the nearest visible row at or above ln.
	best := 0
	for i, r := range d {
		if r.summary {
			continue
		}
		if r.ln <= ln {
			best = i
		}
	}
	return best
}
