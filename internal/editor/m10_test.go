package editor

import (
	"strings"
	"testing"

	"dmed/internal/lsp"
)

func TestNextDiagnosticWalksSorted(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "one\ntwo\nthree\nfour\n")
	m.openPath(p)
	abs := p
	m.diags[abs] = []lsp.Diagnostic{
		{Line: 3, Col: 0, Severity: 2, Message: "later warning"},
		{Line: 1, Col: 0, Severity: 1, Message: "early error"},
		{Line: 1, Col: 4, Severity: 3, Message: "col info"},
	}

	// First press from line 0 lands on the first sorted diagnostic.
	m.nextDiagnostic(1)
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("first F8 landed on line %d, want 1", got)
	}
	if !strings.Contains(m.msg, "early error") {
		t.Fatalf("status = %q, want the message", m.msg)
	}
	// Second press: next line with a diagnostic (both on line 1 are passed in
	// line order).
	m.nextDiagnostic(1)
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("second F8 landed on line %d, want 1 (the col 4 one)", got)
	}
	// Third: line 3, then wrap to the top.
	m.nextDiagnostic(1)
	if got := m.cur().buf.CurLine(); got != 3 {
		t.Fatalf("third F8 landed on line %d, want 3", got)
	}
	m.nextDiagnostic(1)
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("wrapped F8 landed on line %d, want 1", got)
	}
	// Backwards from line 1 goes to the previous diagnostic (line 1 col 0 was
	// the cursor's own; the walk must not re-answer it).
	m.nextDiagnostic(-1)
	if got := m.cur().buf.CurLine(); got != 3 {
		t.Fatalf("Shift+F8 landed on line %d, want 3 (wrapped back)", got)
	}
}

func TestNextDiagnosticNone(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.nextDiagnostic(1)
	if m.msg != m.t("msg.no_diagnostics") {
		t.Fatalf("status = %q", m.msg)
	}
}

func TestHoverLinesStripsMarkdown(t *testing.T) {
	md := "```go\nfunc F(x int) int\n```\n\nSome `docs` here.\n\n"
	got := hoverLines(md)
	want := []string{"func F(x int) int", "", "Some docs here."}
	if len(got) != len(want) {
		t.Fatalf("hoverLines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHoverPanelClipsLongLines(t *testing.T) {
	m := New()
	m.width, m.height = 40, 24
	m.hoverText = []string{strings.Repeat("x", 100)}
	rows := m.hoverPanel()
	// Title + 100/38-wrapped lines + hint: 3 content lines.
	if len(rows) != 5 {
		t.Fatalf("hoverPanel rows = %d, want title + 3 + hint", len(rows))
	}
}

var _ = strings.TrimSpace

func TestSymKindGlyph(t *testing.T) {
	m := New()
	if m.symKindGlyph(5) != "C" || m.symKindGlyph(12) != "f" || m.symKindGlyph(99) != "·" {
		t.Fatal("unexpected glyphs")
	}
}

func TestOutlineAndRefsPanels(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.outline = []lsp.DocumentSymbol{
		{Name: "main", Kind: 12, Line: 7},
		{Name: "Greeter", Kind: 5, Line: 4, Detail: "struct"},
	}
	rows := m.outlinePanel()
	if len(rows) != 3 {
		t.Fatalf("outlinePanel rows = %d, want title + 2", len(rows))
	}
	joined := stripANSI(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "main") || !strings.Contains(joined, "Greeter") {
		t.Fatalf("outline panel = %q", joined)
	}

	m.refs = []lsp.Location{
		{Path: "a.go", Line: 4},
		{Path: "b.go", Line: 9},
	}
	rrows := m.refsPanel()
	if len(rrows) != 3 {
		t.Fatalf("refsPanel rows = %d, want title + 2", len(rrows))
	}
	rjoined := stripANSI(strings.Join(rrows, "\n"))
	if !strings.Contains(rjoined, "a.go:5") || !strings.Contains(rjoined, "b.go:10") {
		t.Fatalf("refs panel = %q", rjoined)
	}
}

func TestRefsWindowClamps(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	for i := 0; i < 25; i++ {
		m.refs = append(m.refs, lsp.Location{Path: "x.go", Line: i})
	}
	// Walk down past the window: the offset follows the selection.
	for i := 0; i < 12; i++ {
		m.refsSel = (m.refsSel + 1) % len(m.refs)
		m.clampRefsWindow()
	}
	if m.refsSel < m.refsOff || m.refsSel >= m.refsOff+10 {
		t.Fatalf("selection %d outside window [%d,%d)", m.refsSel, m.refsOff, m.refsOff+10)
	}
}

func TestSortedDiags(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	dir := t.TempDir()
	p := writeTemp(t, dir, "a.go", "x\n")
	m.openPath(p)
	m.diags[p] = []lsp.Diagnostic{{Line: 5}, {Line: 2}, {Line: 9, Col: 1}, {Line: 9}}
	got := m.sortedDiags()
	if len(got) != 4 || got[0].Line != 2 || got[2].Col != 0 || got[3].Col != 1 {
		t.Fatalf("sortedDiags = %+v", got)
	}
}
