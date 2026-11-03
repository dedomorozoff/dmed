package config

import (
	"runtime"
	"testing"
)

func TestGlobMatches(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// A name-only pattern: * never crosses a separator.
		{"*.go", "main.go", true},
		{"*.go", "a/main.go", false},
		{"*.go", "main.py", false},
		{"*.go", "x.go.bak", false},
		// ** crosses separators, and the leading **/ may stand for no
		// directory at all.
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/main.go", true},
		{"**/*.go", "a/b/main.go", true},
		{"**/*.go", "a/main.py", false},
		// **/ in the middle keeps the separator it was given and eats the one
		// that followed it, so a/**/b.go covers a/b.go as well as a/x/y/b.go.
		{"a/**/b.go", "a/b.go", true},
		{"a/**/b.go", "a/x/b.go", true},
		{"a/**/b.go", "a/x/y/b.go", true},
		{"a/**/b.go", "b.go", false},
		// A trailing ** needs the directory it was anchored to.
		{"lib/**", "lib/a.js", true},
		{"lib/**", "lib/x/a.js", true},
		{"lib/**", "lib", false},
		{"lib/**", "other/a.js", false},
		{"**", "anything/at/all", true},
		// ? is exactly one character and never a separator.
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"a/?/b.go", "a/x/b.go", true},
		{"a/?/b.go", "a/x/y/b.go", false},
		// Character sets, with and without the negation.
		{"[ab].go", "a.go", true},
		{"[ab].go", "c.go", false},
		{"[!ab].go", "c.go", true},
		{"[!ab].go", "a.go", false},
		{"[a-c]x.go", "bx.go", true},
		{".*", ".gitignore", true},
		{".*", "main.go", false},
		// Alternation, including the nested and numeric forms.
		{"*.{go,js}", "m.go", true},
		{"*.{go,js}", "m.py", false},
		{"{a,b}/{c,d}.go", "a/c.go", true},
		{"{a,b}/{c,d}.go", "b/d.go", true},
		// The two groups are independent: a/d.go pairs a from the first with d
		// from the second, which is what the alternation means.
		{"{a,b}/{c,d}.go", "a/d.go", true},
		{"{a,b}/{c,d}.go", "e/c.go", false},
		{"{a,{b,c}}/x.go", "c/x.go", true},
		{"{a,{b,c}}/x.go", "d/x.go", false},
		{"file{1..3}.txt", "file2.txt", true},
		{"file{1..3}.txt", "file4.txt", false},
		// A comma inside braces belongs to the braces, not to the section.
		{"{a,b}.go", "a.go", true},
		// Backslash escapes the metacharacter.
		{`a\*b.go`, "a*b.go", true},
		{`a\*b.go`, "axb.go", false},
		// Literal dots must not act as regexp wildcards.
		{"a.go", "axgo", false},
		{"v1.2/*", "v1.2/main.go", true},
		{"v1.2/*", "vX2/main.go", false},
		// A pattern this compiler cannot read matches nothing, so a broken
		// section never applies itself to the whole project.
		{"[", "a", false},
		{"{", "a", false},
		{"a]", "a]", true},
		{"", "", true},
	}
	for _, c := range cases {
		if got := globMatches(c.pattern, c.path); got != c.want {
			t.Errorf("globMatches(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// TestGlobMatchesCaseFollowsThePlatform pins the documented difference: the
// specification makes matching case-insensitive where the filesystem is, and
// case-sensitive everywhere else. Asserting the platform's own answer keeps the
// suite green on the Linux CI and on this Windows box.
func TestGlobMatchesCaseFollowsThePlatform(t *testing.T) {
	want := caseInsensitivePaths()
	if got := globMatches("*.go", "MAIN.GO"); got != want {
		t.Errorf(`globMatches("*.go", "MAIN.GO") = %v, want %v on %s`, got, want, runtime.GOOS)
	}
}
