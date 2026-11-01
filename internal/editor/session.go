package editor

import (
	"dmed/internal/session"
)

// restoreCursors applies saved per-file cursor positions to the just-opened
// tabs. Positions are clamped by SetCursor, so stale values are harmless.
func (m *Model) restoreCursors(cursors map[string]session.CursorPos) {
	for _, t := range m.tabs {
		if c, ok := cursors[t.path]; ok {
			t.buf.SetCursor(c.Line, c.Col)
		}
	}
}

func (m *Model) saveSession() {
	var files []string
	cursors := map[string]session.CursorPos{}
	for _, t := range m.tabs {
		if t.path != "" {
			files = append(files, t.path)
			cursors[t.path] = session.CursorPos{Line: t.buf.CurLine(), Col: t.buf.Col()}
		}
	}
	if len(files) == 0 {
		return
	}
	sess := session.SessionState{
		Root:       m.root,
		Files:      files,
		ActiveTab:  m.activeTabIndex(),
		Layout:     int(m.layout),
		ActivePane: m.activePane,
		Cursors:    cursors,
		Bookmarks:  m.bookmarksForSession(),
	}
	_ = session.Save(session.DefaultPath(m.root), sess)
}
