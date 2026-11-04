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
	// Start from hidden so Ctrl+B runs the show branch (the toggle is binary:
	// a visible sidebar would hide on the next press).
	m.treeVisible = false
	m.treeFocus = false

	// Ctrl+B reveals and focuses the project tree instead of being eaten by
	// the debug panel.
	n, _ := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = n.(Model)
	if !m.treeVisible || !m.treeFocus {
		t.Fatalf("ctrl+b must show and focus the project tree even with the debug panel open: visible=%v focus=%v", m.treeVisible, m.treeFocus)
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
