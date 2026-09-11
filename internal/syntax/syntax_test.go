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

func TestCommentTokens(t *testing.T) {
	cases := []struct {
		file, content, prefix, suffix string
	}{
		{"main.go", "package main\n", "//", ""},
		{"app.py", "print(1)\n", "#", ""},
		{"x.c", "int main(void) {}\n", "//", ""},
		{"style.css", "body {}\n", "/*", "*/"},
		{"index.html", "<html></html>\n", "<!--", "-->"},
		{"page.vue", "<template></template>\n", "<!--", "-->"},
		{"Makefile", "all:\n", "#", ""},
		{"a.sql", "SELECT 1;\n", "--", ""},
		{"a.lua", "print('x')\n", "#", ""},
		{"a.erl", "ok.\n", "%", ""},
		{"a.hs", "main = putStrLn \"x\"\n", "--", ""},
	}
	for _, c := range cases {
		p, s := CommentTokens(c.file, c.content)
		if p != c.prefix || s != c.suffix {
			t.Errorf("CommentTokens(%s) = (%q,%q), want (%q,%q)",
				c.file, p, s, c.prefix, c.suffix)
		}
	}
}

func TestCommentTokensUnknown(t *testing.T) {
	p, s := CommentTokens("blob.unknownext", "some text")
	if p != "" || s != "" {
		t.Fatalf("unknown type: got (%q,%q), want empty", p, s)
	}
}
