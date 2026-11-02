package syntax

import (
	"strings"
	"testing"
)

// benchGen generates a Go-ish file of ~n lines.
func benchGen(n int) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport \"fmt\"\n\n")
	for i := 0; i < n; i++ {
		b.WriteString("// a comment line\n")
		b.WriteString("func f" + strings.Repeat("x", 1+i%5) + string(rune('a'+i%26)) + "(x int) string {\n")
		b.WriteString("\ts := fmt.Sprintf(\"item %d\", x)\n")
		b.WriteString("\treturn s + \"tail\"\n}\n\n")
	}
	return b.String()
}

func BenchmarkFull10k(b *testing.B) {
	h := New("monokai")
	text := benchGen(1400) // ~10k lines
	for i := 0; i < b.N; i++ {
		h.HighlightBuffer("bench.go", text)
	}
}

func BenchmarkIncOneLine10k(b *testing.B) {
	h := New("monokai")
	text := benchGen(1400)
	ic := NewIncremental()
	ic.Update("bench.go", text, h)
	edited := strings.Replace(text, "item", "ITEM", 1)
	for i := 0; i < b.N; i++ {
		ic.Update("bench.go", edited, h)
	}
}
