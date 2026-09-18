package editor

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestOpenFolderCommandInPalette(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	cmds := m.getPaletteCommands()
	var found bool
	for _, c := range cmds {
		if c.id == "open_folder" {
			found = true
			if c.action == nil {
				t.Fatal("open_folder command has no action")
			}
		}
	}
	if !found {
		t.Fatal("palette must include the open_folder command")
	}

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeStr(m, "folder")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "open_folder" {
		t.Fatalf("filtering 'folder' must surface open_folder, got: %+v", hits)
	}
}

func TestSwitchRoot(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	m := New(dirA)
	m.width, m.height = 80, 24
	if m.root == "" {
		t.Fatal("New(dir) must set the project root")
	}

	m.switchRoot(dirB)
	want, _ := filepath.Abs(dirB)
	if m.root != want {
		t.Fatalf("root = %q, want %q", m.root, want)
	}
	if !m.treeVisible {
		t.Fatal("switchRoot must enable the project tree")
	}
	if !strings.Contains(m.msg, filepath.Base(dirB)) {
		t.Fatalf("status must announce the new project, got %q", m.msg)
	}
}