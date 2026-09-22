package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCtrlBGivesTreeCursorWhileDebugPanelOpen(t *testing.T) {
	m := New(".")
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapFocus = 0
	if !m.treeVisible {
		m.treeVisible = true
	}

	// Ctrl+B moves the caret into the project tree instead of being eaten by
	// the debug panel.
	n, _ := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = n.(Model)
	if !m.treeFocus {
		t.Fatal("ctrl+b must focus the project tree even with the debug panel open")
	}
	if !m.dapOpen {
		t.Fatal("ctrl+b must not close the debug panel")
	}

	// Arrow keys then move the tree cursor, not the debug lists.
	sel := m.treeSel
	n, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = n.(Model)
	if m.treeSel == sel {
		t.Fatal("down must move the tree cursor while the debug panel stays open")
	}
	if !m.dapOpen {
		t.Fatal("arrow keys must not close the debug panel")
	}
}