package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/buffer"
)

// benchBuffer builds a large Go-like document: every line indented inside a
// few thousand foldable blocks, which is the worst case for the fold caches.
func benchBuffer(t testing.TB) Model {
	var sb strings.Builder
	for f := 0; f < 100; f++ {
		sb.WriteString("func fn" + strings.Repeat("x", f%7) + "() {\n")
		for i := 0; i < 50; i++ {
			sb.WriteString("\tif cond")
			sb.WriteString(strings.Repeat(" ", i%23))
			sb.WriteString(" {\n\t\tstep()\n\t\tmore()\n\t}\n")
		}
		sb.WriteString("}\n")
	}
	m := New()
	m.width, m.height = 80, 24
	path := filepath.Join(t.TempDir(), "bench.go")
	m.tabs = []tab{{buf: buffer.Load(sb.String()), path: path}}
	return m
}

// BenchmarkInsertAndRender types one character and renders the frame — the
// per-keystroke cost the editor pays on every keypress.
func BenchmarkInsertAndRender(b *testing.B) {
	m := benchBuffer(b)
	body := &m.tabs[0]
	var sink string
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body.buf.InsertText("x")
		m.View()
		sink = body.buf.Text()
	}
	_ = sink
}

// BenchmarkRenderFolded renders with every block folded, plus the scrolling
// and cursor-mapping helpers that run per keystroke.
func BenchmarkRenderFolded(b *testing.B) {
	m := benchBuffer(b)
	body := &m.tabs[0]
	body.foldAll(m.cfg.Editor.TabWidth)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
		m.clampScroll()
		_, _ = m.cursorScreenPos()
	}
}
