package editor

import (
	"path/filepath"
	"sort"
)

// sortedBoolLines returns the 1-based line numbers of a bool-set map sorted
// ascending.
func sortedBoolLines(set map[int]bool) []int {
	ls := make([]int, 0, len(set))
	for l := range set {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	return ls
}

// toggleBookmarkAt adds/removes a bookmark at the given 0-based line of the
// active tab. Bookmarks are session-local navigation marks (like breakpoints,
// which they mirror in the gutter).
func (m *Model) toggleBookmarkAt(ln int) {
	t := m.cur()
	if t == nil || t.path == "" {
		return
	}
	abs, _ := filepath.Abs(t.path)
	line := ln + 1
	if m.bookmarks[abs] == nil {
		m.bookmarks[abs] = map[int]bool{}
	}
	if m.bookmarks[abs][line] {
		delete(m.bookmarks[abs], line)
		if len(m.bookmarks[abs]) == 0 {
			delete(m.bookmarks, abs)
		}
		m.msg = m.t("msg.bookmark_removed", line)
	} else {
		m.bookmarks[abs][line] = true
		m.msg = m.t("msg.bookmark_added", line)
	}
}

// jumpBookmark moves the cursor to the next (dir > 0) or previous (dir < 0)
// bookmark in the active tab, wrapping around when the edge is reached.
func (m *Model) jumpBookmark(dir int) {
	t := m.cur()
	if t == nil || t.path == "" {
		return
	}
	abs, _ := filepath.Abs(t.path)
	set := m.bookmarks[abs]
	if len(set) == 0 {
		m.msg = m.t("msg.no_bookmarks")
		return
	}
	ls := sortedBoolLines(set)
	cur := t.buf.CurLine() + 1
	next := -1
	if dir > 0 {
		for _, l := range ls {
			if l > cur {
				next = l
				break
			}
		}
		if next < 0 {
			next = ls[0]
		}
	} else {
		for i := len(ls) - 1; i >= 0; i-- {
			if ls[i] < cur {
				next = ls[i]
				break
			}
		}
		if next < 0 {
			next = ls[len(ls)-1]
		}
	}
	t.buf.SetCursor(next-1, 0)
	t.buf.Deselect()
	m.clampScroll()
	m.msg = m.t("msg.bookmark_jump", next)
}
