package editor

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
	// Expanded indent of each line, or -1 when the line is blank.
	ind := make([]int, n)
	for i := 0; i < n; i++ {
		line := t.buf.LineAt(i)
		w, blank := 0, true
		for _, r := range line {
			if r == ' ' || r == '\t' {
				w++
				if r == '\t' {
					w += tabW - 1
				}
				continue
			}
			blank = false
			break
		}
		if blank {
			ind[i] = -1
		} else {
			ind[i] = w
		}
	}
	var rs []foldRange
	for i := 0; i < n; i++ {
		if ind[i] < 0 {
			continue
		}
		// The first non-blank line below decides whether a block starts here.
		k := i + 1
		for k < n && ind[k] < 0 {
			k++
		}
		if k >= n || ind[k] <= ind[i] {
			continue
		}
		// The block ends at the last non-blank line before the indentation
		// falls back to the start level (blank lines inside belong to it).
		end := -1
		for m := i + 1; m < n; m++ {
			if ind[m] >= 0 && ind[m] <= ind[i] {
				break
			}
			if ind[m] >= 0 {
				end = m
			}
		}
		if end > i {
			rs = append(rs, foldRange{start: i, end: end})
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

// foldAt start returns the fold range starting exactly at ln.
func (t *tab) foldAtStart(ln, tabW int) (foldRange, bool) {
	for _, r := range t.foldRanges(tabW) {
		if r.start == ln {
			return r, true
		}
		if r.start > ln {
			break // sorted
		}
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
// is guaranteed visible afterwards.
func (t *tab) unfoldThrough(ln, tabW int) {
	for s := range t.folds {
		if r, ok := t.foldAtStart(s, tabW); ok && s < ln && ln <= r.end {
			delete(t.folds, s)
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

// dispRows builds the pane's display row list under the current folded set.
// contentW is the pane content width; a stale folded marker whose line no
// longer starts a foldable block (buffer edited underneath) is dropped here,
// keeping the state honest without a separate invalidation hook.
func (m Model) dispRows(paneIdx, contentW int) []dispRow {
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	tabW := m.cfg.Editor.TabWidth

	if !p.wordWrap {
		var rows []dispRow
		n := t.buf.LineCount()
		for ln := 0; ln < n; {
			rows = append(rows, dispRow{ln: ln, expEnd: t.buf.LineLen(ln)})
			if r, ok := t.foldStateAt(ln, tabW); ok {
				rows = append(rows, dispRow{ln: ln, summary: true, hidden: r.end - ln})
				ln = r.end + 1
				continue
			}
			ln++
		}
		return rows
	}

	segs := t.tabWrap(contentW, tabW)
	rows := make([]dispRow, 0, len(segs)+len(t.folds))
	for i := 0; i < len(segs); i++ {
		sg := segs[i]
		if _, _, hid := t.foldHidden(sg.line, tabW); hid {
			continue
		}
		rows = append(rows, dispRow{ln: sg.line, expStart: sg.expStart, expEnd: sg.expEnd})
		last := i+1 >= len(segs) || segs[i+1].line != sg.line
		if last {
			if r, ok := t.foldStateAt(sg.line, tabW); ok && r.end < t.buf.LineCount() && r.end > sg.line {
				rows = append(rows, dispRow{ln: sg.line, summary: true, hidden: r.end - sg.line})
			}
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
