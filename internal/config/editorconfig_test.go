package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ecDefault is what the editor falls back to: the historical tab-typed
// indentation, a four-column tab stop and a file that ends with a newline.
var ecDefault = FileOpts{
	TabWidth:           4,
	IndentSize:         4,
	UseTabs:            true,
	InsertFinalNewline: true,
}

// writeEC drops one .editorconfig into dir.
func writeEC(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EditorConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEditorConfigNoFileKeepsTheDefaults(t *testing.T) {
	root := t.TempDir()
	if got := LoadEditorConfig(root, filepath.Join(root, "a.go"), ecDefault); got != ecDefault {
		t.Errorf("without an .editorconfig the answer must be the caller's: %+v", got)
	}
}

func TestLoadEditorConfigSections(t *testing.T) {
	root := t.TempDir()
	writeEC(t, root, `
root = true

[*]
indent_style = space
indent_size = 2
insert_final_newline = false

[*.go]
indent_size = 4
trim_trailing_whitespace = true

[{Makefile,*.mk}]
indent_style = tab
`)
	cases := []struct {
		name string
		rel  string
		want FileOpts
	}{
		{
			name: "the extension section overrides the star section",
			rel:  "main.go",
			want: FileOpts{TabWidth: 4, IndentSize: 4, TrimTrailingWS: true},
		},
		{
			name: "a file only the star section speaks for",
			rel:  "notes.md",
			want: FileOpts{TabWidth: 2, IndentSize: 2},
		},
		{
			// The section that asks for tabs overrides only indent_style: the
			// size and the save policy stay what [*] said.
			name: "a brace alternative is one pattern",
			rel:  "build.mk",
			want: FileOpts{TabWidth: 2, IndentSize: 2, UseTabs: true},
		},
		{
			name: "a nested path still matches a separator-free pattern",
			rel:  filepath.Join("internal", "deep", "x.go"),
			want: FileOpts{TabWidth: 4, IndentSize: 4, TrimTrailingWS: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LoadEditorConfig(root, filepath.Join(root, c.rel), ecDefault)
			if got != c.want {
				t.Errorf("LoadEditorConfig(%s) = %+v, want %+v", c.rel, got, c.want)
			}
		})
	}
}

func TestLoadEditorConfigNearestFileWins(t *testing.T) {
	root := t.TempDir()
	writeEC(t, root, "[*]\nindent_size = 2\n")
	sub := filepath.Join(root, "sub")
	writeEC(t, sub, "[*]\nindent_size = 8\ntrim_trailing_whitespace = true\n")
	got := LoadEditorConfig(root, filepath.Join(sub, "a.go"), ecDefault)
	if got.IndentSize != 8 || !got.TrimTrailingWS {
		t.Errorf("the nearer file must win: %+v", got)
	}
	// …and the farther file still speaks for what the nearer one left unset.
	other := filepath.Join(root, "b.go")
	if got := LoadEditorConfig(root, other, ecDefault); got.IndentSize != 2 {
		t.Errorf("top-level file: indent_size = %d, want 2", got.IndentSize)
	}
}

func TestLoadEditorConfigRootTrueStopsTheWalk(t *testing.T) {
	root := t.TempDir()
	writeEC(t, root, "[*]\nindent_size = 2\nindent_style = space\n")
	mid := filepath.Join(root, "vendor")
	writeEC(t, mid, "root = true\n[*]\nindent_style = tab\n")
	got := LoadEditorConfig(root, filepath.Join(mid, "pkg", "a.go"), ecDefault)
	if !got.UseTabs {
		t.Errorf("a root = true file ends the search, so the outer size must not leak in: %+v", got)
	}
	if got.IndentSize != got.TabWidth || got.TabWidth != ecDefault.TabWidth {
		t.Errorf("indent_style = tab leaves the display stop at the caller's: %+v", got)
	}
}

func TestLoadEditorConfigLastMatchingSectionWins(t *testing.T) {
	root := t.TempDir()
	writeEC(t, root, "[*.go]\nindent_size = 2\n[a.go]\nindent_size = 8\n")
	if got := LoadEditorConfig(root, filepath.Join(root, "a.go"), ecDefault); got.IndentSize != 8 {
		t.Errorf("later section = %d, want 8", got.IndentSize)
	}
	if got := LoadEditorConfig(root, filepath.Join(root, "b.go"), ecDefault); got.IndentSize != 2 {
		t.Errorf("other file = %d, want 2", got.IndentSize)
	}
	// Reversing the two sections reverses the answer, which is what proves the
	// order was kept rather than read out of a map.
	writeEC(t, root, "[a.go]\nindent_size = 8\n[*.go]\nindent_size = 2\n")
	if got := LoadEditorConfig(root, filepath.Join(root, "a.go"), ecDefault); got.IndentSize != 2 {
		t.Errorf("reversed sections = %d, want 2", got.IndentSize)
	}
}

func TestLoadEditorConfigValueRules(t *testing.T) {
	root := t.TempDir()
	writeEC(t, root, `
[tab.go]
indent_style = tab
indent_size = 4

[tabonly.go]
indent_style = tab

[sizeonly.go]
indent_size = 3

[tabsize.go]
indent_style = space
indent_size = tab
tab_width = 3

[width.go]
tab_width = 8

[bad.go]
indent_size = many
tab_width = 0

[huge.go]
indent_size = 100000

[bool.go]
trim_trailing_whitespace = TRUE
insert_final_newline = False
`)
	cases := []struct {
		rel  string
		want FileOpts
	}{
		// A tab unit, shown four columns wide: the size describes the tab, not
		// a number of spaces.
		{"tab.go", FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true, InsertFinalNewline: true}},
		{"tabonly.go", FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true, InsertFinalNewline: true}},
		// Only a size given means spaces, and the display stop follows it.
		{"sizeonly.go", FileOpts{TabWidth: 3, IndentSize: 3, InsertFinalNewline: true}},
		// indent_size = tab takes the tab_width.
		{"tabsize.go", FileOpts{TabWidth: 3, IndentSize: 3, InsertFinalNewline: true}},
		// A stop with nothing else: the unit stays the caller's.
		{"width.go", FileOpts{TabWidth: 8, IndentSize: 8, UseTabs: true, InsertFinalNewline: true}},
		// Unreadable values are not answers, so the caller's defaults stand.
		{"bad.go", ecDefault},
		// The bound is what keeps a typo from becoming a 100000-column unit.
		{"huge.go", FileOpts{TabWidth: maxIndentSize, IndentSize: maxIndentSize, InsertFinalNewline: true}},
		// Booleans are read case-insensitively.
		{"bool.go", FileOpts{TabWidth: 4, IndentSize: 4, UseTabs: true, TrimTrailingWS: true}},
	}
	for _, c := range cases {
		if got := LoadEditorConfig(root, filepath.Join(root, c.rel), ecDefault); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.rel, got, c.want)
		}
	}
}

// TestLoadEditorConfigOutsideTheRoot: a file the editor was pointed at outside
// the project still gets the settings its own ancestors declare — the project
// boundary is not the boundary of the filesystem.
func TestLoadEditorConfigOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	writeEC(t, elsewhere, "[*]\nindent_size = 6\nindent_style = space\n")
	got := LoadEditorConfig(root, filepath.Join(elsewhere, "a.go"), ecDefault)
	if got.IndentSize != 6 || got.UseTabs {
		t.Errorf("ancestors of the file must still be read: %+v", got)
	}
}

func TestFileOptsTypedUnit(t *testing.T) {
	if u := (FileOpts{UseTabs: true}).TypedUnit(); u != "\t" {
		t.Errorf("TypedUnit with tabs = %q", u)
	}
	if u := (FileOpts{IndentSize: 3}).TypedUnit(); u != "   " {
		t.Errorf("TypedUnit 3 = %q", u)
	}
	// A size that cannot be read from a file must still not produce an empty
	// or absurd unit.
	if u := (FileOpts{IndentSize: 0}).TypedUnit(); u != " " {
		t.Errorf("TypedUnit unset = %q, want one space", u)
	}
	if u := (FileOpts{IndentSize: 999}).TypedUnit(); len(u) != maxIndentSize {
		t.Errorf("TypedUnit clamped to %d, want %d", len(u), maxIndentSize)
	}
}

// TestParseEditorConfigCommentsAndCRLF keeps the reader honest about the two
// shapes of the file that are not globs: comments and Windows line endings.
func TestParseEditorConfigCommentsAndCRLF(t *testing.T) {
	f := parseEditorConfig("# a comment\r\n; another\r\n[*]\r\nindent_style = space\r\nindent_size = 2\r\nbogus = 9\r\n")
	if f.root {
		t.Error("a comment-only preamble must not set root")
	}
	if len(f.sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(f.sections))
	}
	sec := f.sections[0]
	if sec.keys["indent_style"] != "space" || sec.keys["indent_size"] != "2" {
		t.Errorf("keys = %v", sec.keys)
	}
	if _, ok := sec.keys["bogus"]; !ok {
		t.Error("a key the reader does not know must still be parsed, not dropped: the format is open-ended")
	}
	if strings.Join(sec.patterns, ",") != "*" {
		t.Errorf("patterns = %v", sec.patterns)
	}
}

func TestParseEditorConfigHeaderWithoutBracketIsSkipped(t *testing.T) {
	f := parseEditorConfig("[oops\nindent_size = 2\n[*]\nindent_size = 4\n")
	if len(f.sections) != 1 || f.sections[0].keys["indent_size"] != "4" {
		t.Fatalf("a malformed header must not swallow the next section: %+v", f.sections)
	}
}
