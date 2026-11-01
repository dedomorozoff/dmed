package editor

import (
	"strings"

	"dmed/internal/buffer"
)

// Merge conflicts are resolved in the buffer, without an overlay: the markers are
// text the editor can already read, and the three choices each replace one block
// with one undo step. This is a different thing from m.conflictOpen /
// handleConflict, which is the file watcher's "the file changed on disk while you
// were editing it" prompt.

const (
	markOurs    = "<<<<<<<"
	markBase    = "|||||||"
	markSep     = "======="
	markTheirs  = ">>>>>>>"
	markLen     = 7 // git writes exactly seven marker characters
	conflictAll = -1
)

// conflictSide is the half of a conflict the user decided to keep.
type conflictSide int

const (
	conflictOurs conflictSide = iota
	conflictTheirs
	conflictBoth
)

// conflictBlock is one complete conflict: the lines the markers sit on. base is
// the diff3 ||||||| line, or -1 when git emitted the two-way form.
type conflictBlock struct {
	start int
	base  int
	sep   int
	end   int
}

// isMark reports a marker line: the seven marker characters and then either a
// space (git writes its label after one) or the end of the line. A run of eight
// is an ASCII box border or a corrupted marker, and editing around either would
// delete the user's text.
func isMark(l, mark string) bool {
	return strings.HasPrefix(l, mark) && (len(l) == markLen || l[markLen] == ' ')
}

func isMarkStart(l string) bool { return isMark(l, markOurs) }
func isMarkBase(l string) bool  { return isMark(l, markBase) }
func isMarkEnd(l string) bool   { return isMark(l, markTheirs) }

// isMarkSep is deliberately exact: a bare ======= line under a heading is
// Markdown setext style, and mistaking it for a separator would resolve a
// conflict that is not there.
func isMarkSep(l string) bool { return l == markSep }

// scanConflicts returns every complete block plus the start lines of unterminated
// markers. A conflict never nests, so a second <<<<<<< before the separator ends
// the block being read and the first start is reported as broken.
func scanConflicts(lines []string) (blocks []conflictBlock, broken []int) {
	var cur conflictBlock
	open := false
	for i, l := range lines {
		switch {
		case isMarkStart(l):
			if open {
				broken = append(broken, cur.start)
			}
			cur = conflictBlock{start: i, base: conflictAll, sep: conflictAll}
			open = true
		case open && isMarkBase(l) && cur.base == conflictAll && cur.sep == conflictAll:
			cur.base = i
		case open && isMarkSep(l) && cur.sep == conflictAll:
			cur.sep = i
		case open && isMarkEnd(l) && cur.sep != conflictAll:
			cur.end = i
			blocks = append(blocks, cur)
			open = false
		}
	}
	if open {
		broken = append(broken, cur.start)
	}
	return blocks, broken
}

// oursEnd is the line the "ours" side stops at: the base marker in the diff3
// form, the separator otherwise.
func (b conflictBlock) oursEnd() int {
	if b.base != conflictAll {
		return b.base
	}
	return b.sep
}

// side returns the lines to keep for one choice. The base section is dropped by
// every choice, as git's own tools do: it is context, not content.
func (b conflictBlock) side(lines []string, k conflictSide) []string {
	ours := lines[b.start+1 : b.oursEnd()]
	theirs := lines[b.sep+1 : b.end]
	switch k {
	case conflictTheirs:
		return theirs
	case conflictBoth:
		out := make([]string, 0, len(ours)+len(theirs))
		out = append(out, ours...)
		return append(out, theirs...)
	default:
		return ours
	}
}

func conflictAt(blocks []conflictBlock, line int) (conflictBlock, bool) {
	for _, b := range blocks {
		if line >= b.start && line <= b.end {
			return b, true
		}
	}
	return conflictBlock{}, false
}

// brokenUnderCursor reports whether the marker nearest above the cursor is one
// that never closed. A file with a broken marker at the top must still let the
// user resolve the well-formed block below it, so "broken" is a region, not a
// property of the whole file: it ends at the next complete block.
func brokenUnderCursor(broken []int, blocks []conflictBlock, line int) bool {
	near, isBroken := -1, false
	for _, b := range blocks {
		if b.start <= line && b.start > near {
			near, isBroken = b.start, false
		}
	}
	for _, start := range broken {
		if start <= line && start > near {
			near, isBroken = start, true
		}
	}
	return isBroken
}

// conflictAfter is the wrap-around search the navigation keys run: the cursor
// moving forward past the last conflict lands on the first one.
func conflictAfter(blocks []conflictBlock, line int) (conflictBlock, int, bool) {
	for i, b := range blocks {
		if b.start > line {
			return b, i, true
		}
	}
	if len(blocks) > 0 {
		return blocks[0], 0, true
	}
	return conflictBlock{}, -1, false
}

func conflictBefore(blocks []conflictBlock, line int) (conflictBlock, int, bool) {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].start < line {
			return blocks[i], i, true
		}
	}
	if n := len(blocks); n > 0 {
		return blocks[n-1], n - 1, true
	}
	return conflictBlock{}, -1, false
}

// conflictLines reads the buffer as the LF lines the marker scan wants. A buffer
// holds LF text in memory whatever the file's line ending on disk is (see
// normalizePaste), so this is the same text the user sees.
func conflictLines(b *buffer.Buffer) []string {
	return strings.Split(b.Text(), "\n")
}

// jumpConflict moves the cursor to the previous/next conflict block. It reports
// false when the buffer holds no complete conflict, which is what lets the same
// key fall back to the Git hunk jump.
func (m *Model) jumpConflict(dir int) bool {
	t := m.cur()
	if t == nil {
		return false
	}
	blocks, _ := scanConflicts(conflictLines(t.buf))
	if len(blocks) == 0 {
		return false
	}
	var (
		blk conflictBlock
		i   int
		ok  bool
	)
	cur := t.buf.CurLine()
	if dir > 0 {
		blk, i, ok = conflictAfter(blocks, cur)
	} else {
		blk, i, ok = conflictBefore(blocks, cur)
	}
	if !ok {
		return false
	}
	t.buf.SetCursor(blk.start, 0)
	m.msg = m.t("msg.merge_nav", i+1, len(blocks))
	return true
}

// jumpChange is what Alt+[ / Alt+] and the palette run: a file that still holds
// conflicts is navigated by conflict, because those are the changes the user has
// to act on; once the last one is resolved the keys go back to Git hunks.
func (m *Model) jumpChange(dir int) {
	if m.jumpConflict(dir) {
		return
	}
	m.jumpHunk(dir)
}

// resolveMergeConflict replaces the conflict block under the cursor with the
// chosen side. Every refusal path leaves the buffer exactly as it was: a cursor
// outside any conflict, and markers that do not form a block — guessing there
// would delete text the user still has to read.
func (m *Model) resolveMergeConflict(k conflictSide) {
	t := m.cur()
	if t == nil {
		return
	}
	lines := conflictLines(t.buf)
	blocks, broken := scanConflicts(lines)
	cur := t.buf.CurLine()
	blk, ok := conflictAt(blocks, cur)
	if !ok {
		if brokenUnderCursor(broken, blocks, cur) {
			m.msg = m.t("msg.merge_malformed")
		} else {
			m.msg = m.t("msg.merge_none")
		}
		return
	}
	if !applyConflict(t.buf, blk, blk.side(lines, k)) {
		m.msg = m.t("msg.merge_malformed")
		return
	}
	// Re-scan the resolved text: the count in the message and the jump to the
	// next conflict both have to describe the buffer as it now is.
	rest, _ := scanConflicts(conflictLines(t.buf))
	if len(rest) == 0 {
		m.msg = m.t("msg.merge_done")
		return
	}
	next, _, _ := conflictAfter(rest, t.buf.CurLine())
	t.buf.SetCursor(next.start, 0)
	m.msg = m.t("msg.merge_resolved", len(rest))
}

// applyConflict writes one resolved side over the block in a single undo step.
// ReplaceSpan rebuilds exactly the lines its span covers, so a non-empty side
// takes the inclusive form over the block itself. An empty side (the deleted half
// of a conflict) cannot: ReplaceSpan always inserts at least one line, which would
// leave a blank line where the block was. It instead spans into column 0 of the
// line after >>>>>>>, swallowing the newlines it does not give back. A block that
// already is the last line has no following line to swallow into, so it accepts
// the leftover empty line.
func applyConflict(b *buffer.Buffer, blk conflictBlock, body []string) bool {
	last := b.LineCount() - 1
	if len(body) == 0 && blk.end < last {
		return b.ReplaceSpan(blk.start, 0, blk.end+1, 0, "")
	}
	return b.ReplaceSpan(blk.start, 0, blk.end, b.LineLen(blk.end), strings.Join(body, "\n"))
}

// mergeConflictHint is the status line a freshly opened conflicted file gets: it
// is the only way someone who merged in the built-in terminal learns the editor
// can resolve the markers. It takes the buffer, not the active tab, because the
// file being opened is not necessarily the one the panes point at yet.
func (m *Model) mergeConflictHint(b *buffer.Buffer) string {
	blocks, broken := scanConflicts(conflictLines(b))
	switch {
	case len(blocks) > 0:
		return m.t("msg.merge_found", len(blocks))
	case len(broken) > 0:
		return m.t("msg.merge_malformed")
	}
	return ""
}
