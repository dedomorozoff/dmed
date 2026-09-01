package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/lsp"
)

func TestDiagMarkFor(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Line: 1, Severity: 1},
		{Line: 3, Severity: 2},
		{Line: 5, Severity: 4},
		{Line: 6, Severity: 2},
	}
	cases := []struct {
		line int
		mark string
		sev  int
	}{
		{0, "", 0},
		{1, "!", 1},
		{3, "!", 2},
		{5, "•", 4},
		{6, "!", 2},
	}
	for _, c := range cases {
		mark, sev := diagMarkFor(diags, c.line)
		if mark != c.mark || sev != c.sev {
			t.Errorf("line %d: got (%q, %d) want (%q, %d)", c.line, mark, sev, c.mark, c.sev)
		}
	}
}

func TestDiagMarkForErrorWins(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Line: 0, Severity: 2},
		{Line: 0, Severity: 1},
	}
	mark, sev := diagMarkFor(diags, 0)
	if mark != "!" || sev != 1 {
		t.Fatalf("error must win over warning on the same line, got (%q, %d)", mark, sev)
	}
}

func TestDiagMarkersRenderedInGutter(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "d.go", "line one\nline two\nline three\n")
	m := New(f)
	m.width, m.height = 80, 24
	abs, _ := filepath.Abs(f)
	m.diags[abs] = []lsp.Diagnostic{
		{Line: 1, Severity: 1},
		{Line: 2, Severity: 2},
	}
	plain := stripANSI(m.View().Content)
	// Each diagnostic line shows a "!" marker in its gutter; the line above does not.
	if got := strings.Count(plain, "!"); got != 2 {
		t.Fatalf("expected 2 diag markers in gutter, got %d\n%s", got, plain)
	}
	if strings.Contains(plain, "line one") && !strings.Contains(strings.SplitN(plain, "line one", 2)[0], "!") {
		t.Log("first line has no marker as expected")
	}
}
