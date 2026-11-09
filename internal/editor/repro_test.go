package editor

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var reproAnsiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func plain(s string) string { return reproAnsiRe.ReplaceAllString(s, "") }

func TestDumpWideRunesWrap(t *testing.T) {
	root := t.TempDir()
	writeTemp(t, root, "a.txt", strings.Repeat("字", 100)+"\n")
	m := New(filepath.Join(root, "a.txt"))
	m.width, m.height = 80, 24
	m.cfg.UI.TreeWidth = 36
	m.treeVisible = true
	m.rebuildTree()
	m.panes[0].wordWrap = true
	m.panes[0].offsetX = 0
	content := m.View().Content
	for i, l := range strings.Split(content, "\n") {
		p := plain(l)
		t.Logf("row %2d: %q", i, p)
	}
}
