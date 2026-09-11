package syntax

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"charm.land/lipgloss/v2"
)

// HighlightedLine contains a lipgloss style for each rune of the line.
type HighlightedLine []lipgloss.Style

// Highlighter provides syntax highlighting using chroma lexers and styles.
type Highlighter struct {
	mu         sync.RWMutex
	styleName  string
	style      *chroma.Style
	styleCache map[chroma.TokenType]lipgloss.Style
}

var defaultHighlighter = New("monokai")

func Default() *Highlighter {
	return defaultHighlighter
}

// SetDefault changes the default highlighter to the named chroma style.
func SetDefault(styleName string) {
	h := New(styleName)
	defaultHighlighter = h
}

func New(styleName string) *Highlighter {
	s := styles.Get(styleName)
	if s == nil {
		s = styles.Get("monokai")
		styleName = "monokai"
	}
	h := &Highlighter{
		styleName:  styleName,
		style:      s,
		styleCache: make(map[chroma.TokenType]lipgloss.Style),
	}
	return h
}

func (h *Highlighter) getStyle(tt chroma.TokenType) lipgloss.Style {
	h.mu.RLock()
	st, ok := h.styleCache[tt]
	h.mu.RUnlock()
	if ok {
		return st
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if st, ok := h.styleCache[tt]; ok {
		return st
	}

	entry := h.style.Get(tt)
	res := lipgloss.NewStyle()
	if entry.Colour.IsSet() {
		res = res.Foreground(lipgloss.Color(entry.Colour.String()))
	}
	if entry.Bold == chroma.Yes {
		res = res.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		res = res.Italic(true)
	}
	if entry.Underline == chroma.Yes {
		res = res.Underline(true)
	}

	h.styleCache[tt] = res
	return res
}

// HighlightBuffer tokenizes full buffer text and returns a slice of styles per rune for each line.
// CommentTokens returns the line-comment prefix/suffix for a file based on the
// chroma lexer that would highlight it. For simple line comments suffix is
// ""; for block-style markers like CSS /* */ both are set. An empty prefix
// signals that the file type has no supported comment syntax.
func CommentTokens(filename, text string) (prefix, suffix string) {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Match(filepath.Base(filename))
	}
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	name := lexer.Config().Name
	if t, ok := commentTable[name]; ok {
		return t[0], t[1]
	}
	return "", ""
}

// [2]string{prefix, suffix}
var commentTable = map[string][2]string{
	"Go":              {"//", ""},
	"C":               {"//", ""},
	"C++":             {"//", ""},
	"C#":              {"//", ""},
	"Java":            {"//", ""},
	"JavaScript":      {"//", ""},
	"TypeScript":      {"//", ""},
	"Rust":            {"//", ""},
	"Swift":           {"//", ""},
	"Kotlin":          {"//", ""},
	"Dart":            {"//", ""},
	"Zig":             {"//", ""},
	"PHP":             {"//", ""},
	"Scala":           {"//", ""},
	"Groovy":          {"//", ""},
	"GDScript3":       {"//", ""},
	"Nim":             {"#", ""},
	"Python":          {"#", ""},
	"Ruby":            {"#", ""},
	"Perl":            {"#", ""},
	"Lua":             {"#", ""},
	"Bash":            {"#", ""},
	"Shell":           {"#", ""},
	"YAML":            {"#", ""},
	"TOML":            {"#", ""},
	"Makefile":        {"#", ""},
	"Docker":          {"#", ""},
	"R":               {"#", ""},
	"Julia":           {"#", ""},
	"Elixir":          {"#", ""},
	"PowerShell":      {"#", ""},
	"Haskell":         {"--", ""},
	"Erlang":          {"%", ""},
	"TeX":             {"%", ""},
	"LaTeX":           {"%", ""},
	"VB.net":          {"'", ""},
	"MySQL":           {"--", ""},
	"SQL":             {"--", ""},
	"CSS":             {"/*", "*/"},
	"SCSS":            {"//", ""},
	"HTML":            {"<!--", "-->"},
	"XML":             {"<!--", "-->"},
	"markdown":        {"<!--", "-->"},
	"vue":             {"<!--", "-->"},
	"Svelte":          {"<!--", "-->"},
	"ObjectPascal":    {"{", "}"},
	"Clojure":         {";", ""},
	"Common Lisp":     {";", ""},
	"Scheme":          {";", ""},
}

// HighlightBuffer tokenizes full buffer text and returns a slice of styles per rune for each line.
func (h *Highlighter) HighlightBuffer(filename, text string) []HighlightedLine {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Match(filepath.Base(filename))
	}
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}

	var result []HighlightedLine
	var curLine HighlightedLine

	for _, token := range iterator.Tokens() {
		st := h.getStyle(token.Type)
		val := token.Value

		lines := strings.Split(val, "\n")
		for i, segment := range lines {
			if i > 0 {
				result = append(result, curLine)
				curLine = HighlightedLine{}
			}
			runes := []rune(segment)
			for range runes {
				curLine = append(curLine, st)
			}
		}
	}
	result = append(result, curLine)
	return result
}
