package syntax

import (
	"testing"
)

func TestHighlightBufferGo(t *testing.T) {
	h := New("monokai")
	code := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	hl := h.HighlightBuffer("main.go", code)
	if len(hl) < 5 {
		t.Fatalf("expected at least 5 lines, got %d", len(hl))
	}
	// package main should have runes with styles
	if len(hl[0]) != len([]rune("package main")) {
		t.Fatalf("expected line 0 length %d, got %d", len([]rune("package main")), len(hl[0]))
	}
}

func TestHighlightBufferFallback(t *testing.T) {
	h := Default()
	code := "some plain text"
	hl := h.HighlightBuffer("unknown.xyz123", code)
	if len(hl) != 1 {
		t.Fatalf("expected 1 line, got %d", len(hl))
	}
	if len(hl[0]) != len([]rune(code)) {
		t.Fatalf("expected line length %d, got %d", len([]rune(code)), len(hl[0]))
	}
}
