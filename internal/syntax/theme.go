package syntax

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// A theme is a chroma style plus the user's own colours on top of it. The base
// style stays chroma's — its 80-odd bundled palettes are the reason dmed has
// colours at all — and what a user adds is a per-token change to the style of
// one token kind, which is exactly what a chroma StyleEntry already expresses.
//
// The overrides are read when a token's style is built, never when the tokens
// are produced: a cached highlight holds token *kinds*, so repainting a theme is
// a matter of building a new Highlighter, not of re-tokenizing the buffer.

// Override is one token's style as its user wrote it. An unset field leaves the
// base style's answer alone, which is why the three flags are trilean: "bold"
// says yes, "nobold" says no, and saying nothing means the theme decides.
type Override = chroma.StyleEntry

// paintable is the set of token kinds a lexer can hand out, minus the ones only
// an HTML or SVG formatter wraps output in. dmed asks for the style of a token
// it is about to paint, so a rule for one of these would be accepted, cached and
// never read — and "my theme does nothing" is the worst kind of bug to debug.
var paintableDenied = map[string]bool{
	"background": true, "prewrapper": true, "line": true, "linenumbers": true,
	"linenumberstable": true, "linehighlight": true, "linetable": true,
	"linetabletd": true, "linelink": true, "codeline": true, "eof": true,
	"ignore": true, "none": true,
}

// TokenNames lists the token kinds a [theme] section may name, sorted, for a
// document or a completion list. This is chroma's own table of token types, not
// a dmed vocabulary: a name a user reads in another tool's theme is a name that
// works here.
func TokenNames() []string {
	names := chroma.TokenTypeStrings()
	sort.Strings(names)
	return names
}

// ParseTokenSpec reads one style rule: "#ff79c6 bold bg:#101014". The grammar is
// chroma's (and Pygments', which is where most of it is documented): a hex
// foreground, "bg:" for a background, and bold / italic / underline, each of
// which can be turned off with a "no" prefix. An empty spec is not a rule that
// paints nothing, it is the absence of one, and the caller uses that to drop an
// override.
func ParseTokenSpec(spec string) (Override, error) {
	ov, err := chroma.ParseStyleEntry(spec)
	if err != nil {
		return Override{}, err
	}
	return ov, nil
}

// ResolveTheme turns the words of a [theme] section into overrides, and reports
// the lines it could not use. Both halves matter: a theme that silently ignored
// half its rules would be reported as a bug in the colours, not in the spelling.
//
// Names are matched the way chroma spells them, case-insensitively, and a dot is
// accepted as the separator the Pygments documentation uses — "keyword.constant"
// and "KeywordConstant" name one token. An empty value drops the token's
// override, so a rule can be switched off without deleting its line.
func ResolveTheme(tokens map[string]string) (map[Token]Override, []string) {
	if len(tokens) == 0 {
		return nil, nil
	}
	out := make(map[Token]Override, len(tokens))
	var refused []string
	names := make([]string, 0, len(tokens))
	for name := range tokens {
		names = append(names, name)
	}
	// Map order is random; a refusal the user reads has to be the same one twice.
	sort.Strings(names)
	for _, name := range names {
		tt, err := lookupTokenType(name)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		spec := strings.TrimSpace(tokens[name])
		if spec == "" {
			continue
		}
		ov, err := ParseTokenSpec(spec)
		if err != nil {
			refused = append(refused, fmt.Sprintf("theme %s: %v", name, err))
			continue
		}
		out[tt] = ov
	}
	if len(out) == 0 {
		return nil, refused
	}
	return out, refused
}

// DumpTokens prints the token names a [theme] section may name, one per line, and
// then the names it refuses because only an HTML or SVG formatter wraps a terminal
// in them. Both halves belong in one list: a user who has "Keyword" painted the way
// they want and a "Background" that does nothing needs to be told which of the two
// kinds of name they are holding, and the refusal message points here for the same
// reason.
func DumpTokens() string {
	var names, wrappers []string
	for _, name := range TokenNames() {
		if paintableDenied[strings.ToLower(name)] {
			wrappers = append(wrappers, name)
			continue
		}
		names = append(names, name)
	}
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name + "\n")
	}
	b.WriteString("\nNot styleable in a terminal (an HTML or SVG wrapper, not text):\n  ")
	b.WriteString(strings.Join(wrappers, ", "))
	b.WriteString("\n")
	return b.String()
}

// nameSeparators folds the spellings a name arrives in: chroma's own is one
// identifier ("KeywordConstant"), Pygments' documents write it with a dot
// ("Keyword.Constant"), and a hand-typed line is lowercase. All three name the
// same token, and only one of them can be the key of a lookup table.
var nameSeparators = strings.NewReplacer(".", "", "_", "", "-", "", " ", "")

// lookupTokenType resolves one written name to a token kind.
func lookupTokenType(name string) (Token, error) {
	// chroma's own table is searched case-insensitively, so folding the name here
	// is what lets the three spellings a user may have seen share one answer.
	key := strings.ToLower(nameSeparators.Replace(strings.TrimSpace(name)))
	tt, err := chroma.TokenTypeString(key)
	if err != nil {
		return 0, fmt.Errorf("unknown token %q (dmed --dump-tokens lists them)", strings.TrimSpace(name))
	}
	if paintableDenied[key] {
		return 0, fmt.Errorf("token %q styles a wrapper no terminal paints; style the text itself (text, keyword, string, ...)", strings.TrimSpace(name))
	}
	return tt, nil
}

// mergeOnto lays the override over the style's own answer. Doing it after
// chroma's inheritance resolution is what makes "nobold" mean something: the
// entry the style resolves for Keyword.Constant already carries the bold it
// inherited from Text, and an override has to beat that, not be merged under it.
func mergeOnto(base, ov chroma.StyleEntry) chroma.StyleEntry {
	if ov.Colour.IsSet() {
		base.Colour = ov.Colour
	}
	if ov.Background.IsSet() {
		base.Background = ov.Background
	}
	if ov.Bold != chroma.Pass {
		base.Bold = ov.Bold
	}
	if ov.Italic != chroma.Pass {
		base.Italic = ov.Italic
	}
	if ov.Underline != chroma.Pass {
		base.Underline = ov.Underline
	}
	return base
}
