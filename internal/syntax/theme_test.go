package syntax

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// What lives here: the words a [theme] section is written in, and the style a
// token ends up with once those words are laid over a bundled palette. The
// tokenizer itself is tested in syntax_test.go — a theme changes no token, only
// the answer to "what does this token look like".

func parseSpec(t *testing.T, spec string) (Override, error) {
	t.Helper()
	return ParseTokenSpec(spec)
}

func TestParseTokenSpecGrammar(t *testing.T) {
	fg, err := parseSpec(t, "#ff79c6")
	if err != nil {
		t.Fatal(err)
	}
	if !fg.Colour.IsSet() {
		t.Fatal("a bare hex must set the foreground")
	}
	if fg.Bold != chroma.Pass || fg.Italic != chroma.Pass || fg.Underline != chroma.Pass {
		t.Fatalf("a colour says nothing about the flags: %+v", fg)
	}

	bg, err := parseSpec(t, "bg:#101014")
	if err != nil {
		t.Fatal(err)
	}
	if !bg.Background.IsSet() || bg.Background.String() != "#101014" {
		t.Fatalf("bg: must set the background, got %q", bg.Background)
	}

	// The trilean is the whole point of writing a rule: saying nothing, saying
	// bold and saying not-bold are three different answers, and only the third can
	// beat a bold the token resolved from its parent.
	for spec, want := range map[string]chroma.Trilean{
		"bold":           chroma.Yes,
		"nobold":         chroma.No,
		"#ff0000":        chroma.Pass,
		"#ff0000 bold":   chroma.Yes,
		"#ff0000 nobold": chroma.No,
		"":               chroma.Pass,
	} {
		ov, err := parseSpec(t, spec)
		if err != nil {
			t.Errorf("ParseTokenSpec(%q): %v", spec, err)
			continue
		}
		if ov.Bold != want {
			t.Errorf("ParseTokenSpec(%q).Bold = %v, want %v", spec, ov.Bold, want)
		}
	}
	for spec, want := range map[string][2]chroma.Trilean{
		"italic":      {chroma.Yes, chroma.Pass},
		"noitalic":    {chroma.No, chroma.Pass},
		"underline":   {chroma.Pass, chroma.Yes},
		"nounderline": {chroma.Pass, chroma.No},
	} {
		ov, err := parseSpec(t, spec)
		if err != nil {
			t.Errorf("ParseTokenSpec(%q): %v", spec, err)
			continue
		}
		if ov.Italic != want[0] || ov.Underline != want[1] {
			t.Errorf("ParseTokenSpec(%q) = italic %v, underline %v; want %v/%v",
				spec, ov.Italic, ov.Underline, want[0], want[1])
		}
	}

	// A word that is neither a flag nor a colour is a typo, and a typo is reported
	// rather than kept as a rule that paints nothing.
	if _, err := parseSpec(t, "#not-a-colour"); err == nil {
		t.Error("a malformed colour must be refused")
	}
	if _, err := parseSpec(t, "wobble"); err == nil {
		t.Error("an unknown style element must be refused")
	}
	// A short hex is not a typo: chroma pads it, so "#f00" is red and "#ff" is blue.
	// Worth pinning because the theme panel previews while the row is still being
	// typed, and it needs to know which keystrokes are already a colour.
	short, err := parseSpec(t, "#f00")
	if err != nil {
		t.Fatalf("a short hex is a colour in this grammar: %v", err)
	}
	if short.Colour.String() != "#ff0000" {
		t.Errorf("#f00 = %q, want the padded #ff0000", short.Colour)
	}
	// The hash on its own is the half-typed case, and it is an error rather than a
	// colour made of nothing.
	if _, err := parseSpec(t, "#"); err == nil {
		t.Error("a lone hash is not a colour and must be refused")
	}
}

// TestResolveThemeSpellings: one token has three spellings a user may have met —
// chroma's identifier, Pygments' dotted name, and the lowercase a config file
// holds after parseINI — and all three must reach the same token.
func TestResolveThemeSpellings(t *testing.T) {
	for _, name := range []string{"KeywordConstant", "keyword.constant", "KEYWORD_CONSTANT", "keywordconstant", " keyword.constant "} {
		ov, refused := ResolveTheme(map[string]string{name: "#ff0000"})
		if len(refused) > 0 {
			t.Fatalf("%q refused: %v", name, refused)
		}
		got, ok := ov[chroma.KeywordConstant]
		if !ok {
			t.Fatalf("%q resolved to %v, want KeywordConstant", name, tokenKeys(ov))
		}
		if !got.Colour.IsSet() {
			t.Fatalf("%q resolved but carries no colour", name)
		}
	}
}

func TestResolveThemeRefusals(t *testing.T) {
	// An unknown name is reported, and the report says where to look them up.
	if _, refused := ResolveTheme(map[string]string{"keywordz": "#ff0000"}); len(refused) != 1 ||
		!strings.Contains(refused[0], "dump-tokens") {
		t.Fatalf("unknown token refused as %v", refused)
	}
	// A wrapper a terminal never paints is refused rather than accepted, cached and
	// silently unread.
	if _, refused := ResolveTheme(map[string]string{"background": "#ff0000"}); len(refused) != 1 ||
		!strings.Contains(refused[0], "wrapper") {
		t.Fatalf("Background refused as %v, want one message about a wrapper", refused)
	}
	// A bad colour is refused too: the line is not quietly dropped.
	if _, refused := ResolveTheme(map[string]string{"keyword": "not-a-colour"}); len(refused) != 1 {
		t.Fatalf("bad style refused as %v, want one message", refused)
	}
	// Refusals come back in name order, so the one the user reads is the same one
	// next time the file is saved.
	if _, refused := ResolveTheme(map[string]string{"zzz": "bold", "aaa": "bold"}); len(refused) != 2 ||
		!strings.HasPrefix(refused[0], `unknown token "aaa"`) {
		t.Fatalf("refusals not in name order: %v", refused)
	}
	// An empty value is the absence of a rule, not a rule that paints nothing: it is
	// how a line is switched off, and it is not an error either.
	ov, refused := ResolveTheme(map[string]string{"keyword": ""})
	if len(refused) > 0 || len(ov) != 0 {
		t.Fatalf("an off-switch came back as ov=%v refused=%v", ov, refused)
	}
	if got, refused := ResolveTheme(nil); got != nil || refused != nil {
		t.Fatalf("no section resolved to %v/%v", got, refused)
	}
}

// TestOverrideBeatsWhatTheStyleResolved is the reason the merge happens after
// chroma's inheritance walk rather than inside the style: an override has to
// outvote the answer the style worked out for the token.
func TestOverrideBeatsWhatTheStyleResolved(t *testing.T) {
	// monokai paints Generic.Strong bold; that is the base this rule argues with.
	if !New("monokai").StyleFor(chroma.GenericStrong).GetBold() {
		t.Skip("the bundled monokai no longer bolds GenericStrong; the rule below needs a bold token")
	}
	h := NewWith("monokai", map[Token]Override{
		chroma.GenericStrong: mustParse(t, "nobold bg:#101014"),
	})
	st := h.StyleFor(chroma.GenericStrong)
	if st.GetBold() {
		t.Fatal("nobold must beat the bold the style resolved")
	}
	if got := hexOf(st.GetBackground()); got != "#101014" {
		t.Fatalf("a background rule is not painted: %q", got)
	}
	// The rule said nothing about the foreground, so the style's own answer stays.
	base := styles.Get("monokai").Get(chroma.GenericStrong)
	if got := hexOf(st.GetForeground()); got != strings.ToLower(base.Colour.String()) {
		t.Fatalf("an override that names no colour replaced the style's own: %q vs %q", got, base.Colour)
	}
}

// TestOverrideDoesNotTouchTokensItDoesNotName is the difference between a palette and a
// blank screen: one rule must not recolour the file.
func TestOverrideDoesNotTouchTokensItDoesNotName(t *testing.T) {
	plain := New("monokai")
	yours := NewWith("monokai", map[Token]Override{chroma.Keyword: mustParse(t, "#ff0000")})
	if got := hexOf(yours.StyleFor(chroma.Keyword).GetForeground()); got != "#ff0000" {
		t.Fatalf("Keyword foreground = %s", got)
	}
	for _, tt := range []chroma.TokenType{chroma.Comment, chroma.LiteralString, chroma.Text} {
		base := hexOf(plain.StyleFor(tt).GetForeground())
		got := hexOf(yours.StyleFor(tt).GetForeground())
		if base != got {
			t.Fatalf("%s changed from %s to %s by a Keyword rule", tt, base, got)
		}
	}
	if plain.StyleName() != "monokai" || yours.StyleName() != "monokai" {
		t.Fatalf("StyleName = %q/%q", plain.StyleName(), yours.StyleName())
	}
}

// TestUnknownStyleNameStillPaints: a style the registry does not know is a typo,
// and a typo is not a reason for an editor without colours.
func TestUnknownStyleNameStillPaints(t *testing.T) {
	h := NewWith("definitely-not-a-chroma-style", nil)
	if h.StyleName() != "monokai" {
		t.Fatalf("StyleName = %q, want the fallback monokai", h.StyleName())
	}
	if hexOf(h.StyleFor(chroma.Keyword).GetForeground()) == "" {
		t.Fatal("the fallback style paints nothing")
	}
}

func TestTokenNamesAreSortedAndUsable(t *testing.T) {
	names := TokenNames()
	if len(names) < 100 {
		t.Fatalf("TokenNames returned %d names, chroma has far more", len(names))
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("TokenNames must be sorted: a list a user reads has to be the same list twice")
	}
	for _, want := range []string{"Keyword", "KeywordConstant", "LiteralString", "Comment", "TextWhitespace"} {
		if !hasName(names, want) {
			t.Fatalf("TokenNames is missing %s", want)
		}
	}
}

// TestDumpTokensListsBothHalves: the flag the refusal message points at has to
// answer both questions a stuck user has — which names work, and why this one
// does not.
func TestDumpTokensListsBothHalves(t *testing.T) {
	out := DumpTokens()
	usable, tail, found := strings.Cut(out, "Not styleable")
	if !found {
		t.Fatal("the dump must name the wrappers it refuses, so a user knows which half they are holding")
	}
	for _, want := range []string{"Keyword\n", "GenericStrong\n", "LiteralString\n"} {
		if !strings.Contains(usable, want) {
			t.Fatalf("the usable list is missing %s", strings.TrimSpace(want))
		}
	}
	for _, denied := range []string{"LineNumbersTable", "Background", "PreWrapper"} {
		if !strings.Contains(tail, denied) {
			t.Fatalf("the refused list is missing %s", denied)
		}
		if strings.Contains(usable, denied+"\n") {
			t.Fatalf("%s is in both halves of the dump", denied)
		}
	}
}

// hexOf is how a colour gets compared here: a style's foreground comes back as an
// image/color value, not the hex string the theme was written in, and printing it
// would show the struct rather than the colour a user asked for.
func hexOf(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func mustParse(t *testing.T, spec string) Override {
	t.Helper()
	ov, err := ParseTokenSpec(spec)
	if err != nil {
		t.Fatalf("ParseTokenSpec(%q): %v", spec, err)
	}
	return ov
}

func hasName(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func tokenKeys(m map[Token]Override) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k.String())
	}
	return out
}
