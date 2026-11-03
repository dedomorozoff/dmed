package config

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// globMatches reports whether rel — the file's path relative to the directory
// the .editorconfig section lives in, always with forward separators — is
// matched by one editorconfig glob.
//
// This is the editorconfig grammar, deliberately not the one the project
// finder uses (globMatchSegments in internal/editor/tools_fs.go): that one has
// no {}, no anchoring rules for ** and matches names only. The two must not
// share code, and neither may be replaced by the other.
//
// A pattern this compiler cannot translate matches nothing. The alternative,
// matching everything, would apply a broken section to every file in the
// project, which is the worse of the two failures.
func globMatches(pattern, rel string) bool {
	src, err := globSource(pattern)
	if err != nil {
		return false
	}
	re, err := regexp.Compile(anchorRegexp(src))
	if err != nil {
		return false
	}
	return re.MatchString(rel)
}

// anchorRegexp wraps a compiled glob in ^…$ and, on the case-insensitive
// platforms, a (?i). Matching the whole string is what makes * stop at a path
// separator: a pattern that cannot reach across a / is the difference between
// "*.go" and "**/*.go".
func anchorRegexp(src string) string {
	if caseInsensitivePaths() {
		return `(?i)^` + src + `$`
	}
	return `^` + src + `$`
}

// caseInsensitivePaths reports the platforms whose filesystem treats MAIN.GO
// and main.go as one file, which is where a glob has to as well: the
// specification makes editorconfig matching case-insensitive on Windows and
// macOS and case-sensitive everywhere else.
func caseInsensitivePaths() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// globSource translates one editorconfig glob into a regular expression.
//
// The grammar, as documented by the specification:
//
//   - any run of characters except /
//     ?        exactly one character except /
//     [seq]    one character of the set, [!seq] one not in it
//     **       any run of characters, / included
//     {a,b}    alternation, nestable; {1..9} is a numeric range
//     \c       the literal character c
//
// **/ is the one sequence whose translation depends on where it sits: it must
// be able to stand for "no directory at all" as well as "any number of them",
// so it becomes (?:.*/)? and eats the separator that follows it. A ** anywhere
// else is a plain .*.
func globSource(p string) (string, error) {
	var out strings.Builder
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out.WriteString(regexp.QuoteMeta(lit.String()))
			lit.Reset()
		}
	}
	for i := 0; i < len(p); {
		switch c := p[i]; c {
		case '\\':
			if i+1 >= len(p) {
				return "", fmt.Errorf("pattern %q ends in an escape", p)
			}
			lit.WriteByte(p[i+1])
			i += 2
		case '*':
			n := 0
			for i+n < len(p) && p[i+n] == '*' {
				n++
			}
			flush()
			if n >= 2 {
				if i+n < len(p) && p[i+n] == '/' {
					out.WriteString("(?:.*/)?")
					i += n + 1
					continue
				}
				out.WriteString(".*")
			} else {
				out.WriteString("[^/]*")
			}
			i += n
		case '?':
			flush()
			out.WriteString("[^/]")
			i++
		case '[':
			flush()
			cls, next, err := globClass(p, i)
			if err != nil {
				return "", err
			}
			out.WriteString(cls)
			i = next
		case '{':
			flush()
			alt, next, err := globAlternation(p, i)
			if err != nil {
				return "", err
			}
			out.WriteString(alt)
			i = next
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()
	return out.String(), nil
}

// globClass compiles [seq] / [!seq] starting at the opening bracket and
// returns the position just past the closing one.
func globClass(p string, i int) (string, int, error) {
	j := i + 1
	neg := false
	if j < len(p) && (p[j] == '!' || p[j] == '^') {
		neg = true
		j++
	}
	start := j
	// A ] in the first position of the set is a literal ], not the end of it.
	if j < len(p) && p[j] == ']' {
		j++
	}
	for j < len(p) && p[j] != ']' {
		if p[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(p) {
		return "", 0, fmt.Errorf("unterminated character class in %q", p)
	}
	body := p[start:j]
	if body == "" {
		return "", 0, fmt.Errorf("empty character class in %q", p)
	}
	if strings.Contains(body, "[") {
		return "", 0, fmt.Errorf("nested character class in %q", p)
	}
	if neg {
		return "[^" + body + "]", j + 1, nil
	}
	return "[" + body + "]", j + 1, nil
}

// globAlternation compiles {a,b} — including the {1..9} numeric form — and
// returns the position just past the closing brace. The braces nest, so the
// split happens at this depth only and every alternative is translated by the
// same function.
func globAlternation(p string, i int) (string, int, error) {
	depth := 0
	end := -1
	for k := i; k < len(p); k++ {
		switch p[k] {
		case '\\':
			k++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = k
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", 0, fmt.Errorf("unbalanced braces in %q", p)
	}
	inner := p[i+1 : end]
	if rangeSrc, ok := numericRange(inner); ok {
		return rangeSrc, end + 1, nil
	}
	parts := splitTopLevel(inner)
	var b strings.Builder
	b.WriteString("(?:")
	for n, part := range parts {
		if n > 0 {
			b.WriteString("|")
		}
		src, err := globSource(part)
		if err != nil {
			return "", 0, err
		}
		b.WriteString(src)
	}
	b.WriteString(")")
	return b.String(), end + 1, nil
}

// maxNumericRange bounds what a {1..9} can expand to: a pattern asking for a
// million alternatives is a mistake, and the cost of honouring it is a regexp
// that cannot be built.
const maxNumericRange = 256

func numericRange(inner string) (string, bool) {
	dot := strings.Index(inner, "..")
	if dot <= 0 || strings.Count(inner, "..") != 1 {
		return "", false
	}
	lo, err1 := strconv.Atoi(inner[:dot])
	hi, err2 := strconv.Atoi(inner[dot+2:])
	if err1 != nil || err2 != nil {
		return "", false
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi-lo+1 > maxNumericRange {
		return "", false
	}
	var b strings.Builder
	b.WriteString("(?:")
	for n := lo; n <= hi; n++ {
		if n > lo {
			b.WriteString("|")
		}
		b.WriteString(strconv.Itoa(n))
	}
	b.WriteString(")")
	return b.String(), true
}

// splitTopLevel cuts a brace body on the commas that separate alternatives;
// commas inside a nested brace belong to that brace, not to this one.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[last:i])
				last = i + 1
			}
		}
	}
	return append(parts, s[last:])
}

// splitSectionPatterns cuts a section header on its top-level commas:
// [include(s),include] is two patterns for one set of values, which is exactly
// what the brace form means, so both are matched and any one hit applies.
func splitSectionPatterns(header string) []string {
	var out []string
	for _, p := range splitTopLevel(header) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
