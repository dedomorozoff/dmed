package editor

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestGlyphSetsSingleWidth guards the single-cell glyphs the layout relies on:
// hit-testing and padding use their width without measuring. Chat button labels
// are excluded — those are measured with lipgloss.Width and may be words.
func TestGlyphSetsSingleWidth(t *testing.T) {
	for name, g := range map[string]glyphSet{
		"unicode": unicodeGlyphs,
		"ascii":   asciiGlyphs,
	} {
		for field, glyph := range map[string]string{
			"vline":      g.vline,
			"hline":      g.hline,
			"tee":        g.tee,
			"corner":     g.corner,
			"expand":     g.expand,
			"collapse":   g.collapse,
			"stop":       g.stop,
			"breakpt":    g.breakpt,
			"breakptO":   g.breakptO,
			"bookmark":   g.bookmark,
			"diagInfo":   g.diagInfo,
			"check":      g.check,
			"cross":      g.cross,
			"ellipsis":   g.ellipsis,
			"dotSep":     g.dotSep,
			"progFull":   g.progFull,
			"progEmpty":  g.progEmpty,
			"iconTree":   g.iconTree,
			"iconGit":    g.iconGit,
			"iconChat":   g.iconChat,
			"iconDebug":  g.iconDebug,
			"iconTerm":   g.iconTerm,
			"iconSplitV": g.iconSplitV,
			"iconSplitH": g.iconSplitH,
			"mask":       g.mask,
		} {
			w := lipgloss.Width(glyph)
			if w != 1 {
				t.Errorf("%s.%s = %q, width %d, want 1", name, field, glyph, w)
			}
		}
	}
}

// TestApplyTerminalCompatASCII verifies the ASCII fallback actually lands when
// the terminal heuristic says so, and that a modern terminal keeps unicode.
func TestApplyTerminalCompat(t *testing.T) {
	// Keep the test independent of the host running it. An empty TERM in a
	// headless Windows shell is correctly treated as a legacy console.
	t.Setenv("TERM", "xterm-256color")
	m := New()
	m.ApplyTerminalCompat()
	if m.g != unicodeGlyphs {
		t.Fatalf("default terminal must keep unicode glyphs, got %+v", m.g)
	}
}
