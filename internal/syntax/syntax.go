package syntax

import (
	"path/filepath"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Token identifies the kind of a syntax token. The editor keeps one token
// per character and asks for the style when it paints a line: a
// lipgloss.Style per character costs hundreds of bytes and a token a couple,
// which matters a lot for a large file.
type Token = chroma.TokenType

// HighlightedLine holds the token of every rune of the line.
type HighlightedLine []Token

// Highlighter provides syntax highlighting using chroma lexers and styles.
type Highlighter struct {
	mu         sync.RWMutex
	styleName  string
	style      *chroma.Style
	styleCache map[chroma.TokenType]lipgloss.Style
}

// New builds a Highlighter for the named chroma style. An unknown name falls
// back to monokai.
//
// There is deliberately no package-level "default" highlighter: the editor
// owns its Highlighter (see editor.Model.syn), because SetDefault used to be
// called from the Update loop while rendering goroutines read it — a global
// mutable that the project rules forbid and that -race would flag the moment
// the config hot-reload landed on another path.
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

// StyleFor returns the style of a token kind. The styles are built once and
// cached, so this is a lookup.
func (h *Highlighter) StyleFor(t Token) lipgloss.Style {
	return h.getStyle(t)
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

// langByExt maps a file extension to the language tag the editor shows.
// The tags are chroma's first alias, but they are listed here on purpose:
// asking chroma for them initializes its lexer registry, which parses every
// embedded grammar and takes seconds — far too long for the status bar of
// a frame.
var langByExt = map[string]string{
	".go": "go", ".py": "python", ".ts": "ts", ".tsx": "ts", ".mts": "ts",
	".cts": "ts", ".js": "js", ".jsx": "jsx", ".mjs": "js", ".cjs": "js",
	".php": "php", ".rb": "rb",
	".rs": "rust", ".java": "java", ".kt": "kotlin", ".swift": "swift",
	".c": "c", ".h": "c", ".cpp": "cpp", ".cc": "cpp", ".hpp": "cpp",
	".cs": "csharp", ".m": "mathematica", ".dart": "dart", ".zig": "zig",
	".lua": "lua", ".pl": "perl", ".pm": "perl", ".r": "splus", ".scala": "scala",
	".groovy": "groovy", ".ex": "elixir", ".exs": "elixir", ".erl": "erlang",
	".hs": "haskell", ".ml": "ocaml", ".fs": "forth", ".clj": "clojure",
	".jl": "julia", ".nim": "nim", ".pas": "objectpascal", ".f90": "fortran",
	".sh": "bash", ".bash": "bash", ".zsh": "bash", ".fish": "fish",
	".ps1": "powershell", ".bat": "bat", ".sql": "mysql", ".html": "html",
	".htm": "html", ".css": "css", ".scss": "scss", ".json": "json",
	".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".xml": "xml",
	".svg": "xml", ".md": "md", ".markdown": "md", ".rst": "rst", ".vue": "vue",
	".svelte": "svelte", ".proto": "protobuf", ".cmake": "cmake", ".vim": "vim",
	".asm": "nasm", ".txt": "text", ".gd": "gdscript3", ".sol": "sol",
	".tf": "terraform", ".hcl": "hcl", ".graphql": "graphql",
}

// langByName maps the file names that carry their type in the name.
var langByName = map[string]string{
	"makefile": "make", "gnumakefile": "make", "dockerfile": "docker",
	"cmakelists.txt": "cmake", "rakefile": "rb", "gemfile": "rb",
}

// Lang returns the short language tag for a filename (e.g. "go", "php",
// "python"), or "" when the file type is unknown/plain text. Used for the
// status-bar language indicator and DAP auto-detection.
func Lang(filename string) string {
	name := strings.ToLower(filepath.Base(filename))
	if tag, ok := langByName[name]; ok {
		return tag
	}
	if tag, ok := langByExt[strings.ToLower(filepath.Ext(name))]; ok {
		return tag
	}
	return ""
}

// Warmup initializes chroma's lexer registry. The first lookup parses every
// embedded grammar, which takes seconds; doing it in the background at
// startup keeps the first frame from waiting for it.
func Warmup() {
	lexers.Match("warmup.go")
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
	"Go":           {"//", ""},
	"C":            {"//", ""},
	"C++":          {"//", ""},
	"C#":           {"//", ""},
	"Java":         {"//", ""},
	"JavaScript":   {"//", ""},
	"TypeScript":   {"//", ""},
	"Rust":         {"//", ""},
	"Swift":        {"//", ""},
	"Kotlin":       {"//", ""},
	"Dart":         {"//", ""},
	"Zig":          {"//", ""},
	"PHP":          {"//", ""},
	"Scala":        {"//", ""},
	"Groovy":       {"//", ""},
	"GDScript3":    {"//", ""},
	"Nim":          {"#", ""},
	"Python":       {"#", ""},
	"Ruby":         {"#", ""},
	"Perl":         {"#", ""},
	"Lua":          {"#", ""},
	"Bash":         {"#", ""},
	"Shell":        {"#", ""},
	"YAML":         {"#", ""},
	"TOML":         {"#", ""},
	"Makefile":     {"#", ""},
	"Docker":       {"#", ""},
	"R":            {"#", ""},
	"Julia":        {"#", ""},
	"Elixir":       {"#", ""},
	"PowerShell":   {"#", ""},
	"Haskell":      {"--", ""},
	"Erlang":       {"%", ""},
	"TeX":          {"%", ""},
	"LaTeX":        {"%", ""},
	"VB.net":       {"'", ""},
	"MySQL":        {"--", ""},
	"SQL":          {"--", ""},
	"CSS":          {"/*", "*/"},
	"SCSS":         {"//", ""},
	"HTML":         {"<!--", "-->"},
	"XML":          {"<!--", "-->"},
	"markdown":     {"<!--", "-->"},
	"vue":          {"<!--", "-->"},
	"Svelte":       {"<!--", "-->"},
	"ObjectPascal": {"{", "}"},
	"Clojure":      {";", ""},
	"Common Lisp":  {";", ""},
	"Scheme":       {";", ""},
}

// lexerFor picks the lexer for a file name (and, as a fallback, for the
// content itself).
func (h *Highlighter) lexerFor(filename, text string) chroma.Lexer {
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
	return chroma.Coalesce(lexer)
}

// HighlightLines tokenizes the text and hands every finished line to fn, in
// order. The tokenizer is consumed as it goes — the whole token list is
// never materialized — so the caller can paint the beginning of a large file
// while the rest is still being tokenized. The callback may return false to
// stop the tokenizing.
func (h *Highlighter) HighlightLines(filename, text string, fn func(line HighlightedLine) bool) {
	iterator, err := h.lexerFor(filename, text).Tokenise(nil, text)
	if err != nil {
		return
	}
	var curLine HighlightedLine
	for {
		token := iterator()
		if token == chroma.EOF {
			break
		}
		tok := token.Type
		lines := strings.Split(token.Value, "\n")
		for i, segment := range lines {
			if i > 0 {
				if !fn(curLine) {
					return
				}
				curLine = HighlightedLine{}
			}
			for range []rune(segment) {
				curLine = append(curLine, tok)
			}
		}
	}
	fn(curLine)
}

// HighlightBuffer tokenizes full buffer text and returns a slice of styles per rune for each line.
func (h *Highlighter) HighlightBuffer(filename, text string) []HighlightedLine {
	var result []HighlightedLine
	h.HighlightLines(filename, text, func(line HighlightedLine) bool {
		result = append(result, line)
		return true
	})
	return result
}
