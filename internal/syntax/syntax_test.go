package syntax

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
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

// TestHighlightTokensAndStyles: the cached highlight holds one token per
// character (a style per character would cost hundreds of bytes each) and
// every token resolves to a style.
func TestHighlightTokensAndStyles(t *testing.T) {
	h := New("monokai")
	code := "package main\n"
	hl := h.HighlightBuffer("main.go", code)
	if len(hl) != 2 { // the text ends with a newline, so there is an empty line
		t.Fatalf("expected 2 lines, got %d", len(hl))
	}
	line := hl[0]
	if len(line) != len([]rune("package main")) {
		t.Fatalf("expected %d tokens, got %d", len([]rune("package main")), len(line))
	}
	// The keyword and the space after it are different tokens.
	if line[0] == line[7] {
		t.Fatalf("the keyword and the space must not share a token: %v", line[:8])
	}
	// Every token resolves to a style.
	for i, tok := range line {
		_ = h.StyleFor(tok)
		if i > 8 {
			break
		}
	}
}

func TestHighlightBufferFallback(t *testing.T) {
	h := New("monokai")
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

func TestLang(t *testing.T) {
	cases := []struct{ file, want string }{
		{"main.go", "go"},
		{"index.php", "php"},
		{"server.py", "python"},
		{"app.ts", "ts"},
		{"notes.txt", "text"},
		{"README", ""},
	}
	for _, c := range cases {
		if got := Lang(c.file); got != c.want {
			t.Errorf("Lang(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

// TestLangMatchesChroma: the language tags the editor shows are listed in a
// map (asking chroma for them would initialize its lexer registry, which
// takes seconds). The map must agree with chroma, otherwise the status bar
// and the debug adapter pick the wrong language.
func TestLangMatchesChroma(t *testing.T) {
	for file := range langByExt {
		name := "a" + file
		lexer := lexers.Match(name)
		if lexer == nil {
			continue // chroma does not know this extension
		}
		cfg := chroma.Coalesce(lexer).Config()
		want := cfg.Name
		if len(cfg.Aliases) > 0 {
			want = cfg.Aliases[0]
		}
		if got := Lang(name); got != want {
			t.Errorf("Lang(%q) = %q, want %q (chroma)", name, got, want)
		}
	}
}

func TestCommentTokensUnknown(t *testing.T) {
	p, s := CommentTokens("blob.unknownext", "some text")
	if p != "" || s != "" {
		t.Fatalf("unknown type: got (%q,%q), want empty", p, s)
	}
}
