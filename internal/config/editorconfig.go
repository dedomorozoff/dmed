package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileOpts are the per-file settings a .editorconfig asks for. They sit beside
// the [editor] section rather than inside it because [editor] is one answer
// for the whole editor while these are one answer per file: a project may hold
// tabs in its Makefile, two spaces in its YAML and four in its Go.
type FileOpts struct {
	// TabWidth is how many columns a tab character occupies on screen.
	TabWidth int
	// IndentSize is one indentation level in columns. It is what a Tab
	// inserts when UseTabs is false, and the display stop when it is true.
	IndentSize int
	// UseTabs says the typed unit is a tab character.
	UseTabs bool
	// TrimTrailingWS drops the whitespace at the end of every line on save.
	TrimTrailingWS bool
	// InsertFinalNewline keeps exactly one newline at the end of the file;
	// false means the file is written without it.
	InsertFinalNewline bool
}

// TypedUnit is the string one Tab press inserts in this file: a tab character,
// or the indentation level in spaces.
func (o FileOpts) TypedUnit() string {
	if o.UseTabs {
		return "\t"
	}
	n := o.IndentSize
	if n < 1 {
		n = 1
	}
	if n > maxIndentSize {
		n = maxIndentSize
	}
	return strings.Repeat(" ", n)
}

// maxIndentSize bounds what a file may ask for: indent_size is read from a
// file, and a typo of 100000 would otherwise become a 100000-column unit in
// every line the user indents.
const maxIndentSize = 64

// FileOpts is the per-file default this configuration implies: what a file
// gets where no .editorconfig has an opinion. Both the running editor and a
// background sub-agent need the same answer, and only this direction keeps
// internal/editor the one place that knows what dmed does by default.
func (c Config) FileOpts() FileOpts {
	w := c.Editor.TabWidth
	return FileOpts{
		TabWidth:           w,
		IndentSize:         w,
		UseTabs:            true,
		InsertFinalNewline: true,
	}
}

// EditorConfigFile is the name of the file that carries these settings.
const EditorConfigFile = ".editorconfig"

// honouredKeys are the settings dmed acts on. end_of_line and charset are not
// among them because the buffer already answers both from the bytes of the
// file itself (detectFileInfo in internal/editor): letting a text file
// overrule what the file actually contains would rewrite bytes the user never
// asked to rewrite. max_line_length is out for the same reason — the buffer
// holds runes and never re-flows a line. See the follow-ups in ROADMAP.md.
var honouredKeys = map[string]bool{
	"indent_style":             true,
	"indent_size":              true,
	"tab_width":                true,
	"trim_trailing_whitespace": true,
	"insert_final_newline":     true,
}

// maxConfigWalk bounds the upward search. Real trees do not go deeper than
// this, and a walk with no ceiling turns opening one file into a stat per
// ancestor directory for the rest of the session.
const maxConfigWalk = 64

// LoadEditorConfig returns the settings that apply to path, with def filling
// in everything the .editorconfig files left unset — that is how the editor's
// own tab_width stays the answer when no file has an opinion.
//
// The search starts in the file's directory and walks up. A nearer file wins
// over a farther one, and inside a file a later matching section wins over an
// earlier one, because those are the rules the specification states. A file
// that sets root = true ends the walk: settings above it are not this
// project's business. root is where the search stops when no file says so; an
// empty root, and a file that sits outside it, let the walk run to the
// filesystem root.
//
// path may be relative; it is resolved before the walk so that a caller's
// working directory cannot change which file is found.
func LoadEditorConfig(root, path string, def FileOpts) FileOpts {
	if path == "" {
		return def
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return def
	}
	files := editorConfigChain(root, abs)
	merged := map[string]string{}
	// Farthest first, so a nearer file's section overwrites what it said.
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		for _, sec := range f.sections {
			if !sectionMatches(sec.patterns, f.dir, abs) {
				continue
			}
			for k, v := range sec.keys {
				if honouredKeys[k] {
					merged[k] = v
				}
			}
		}
	}
	return resolveFileOpts(merged, def)
}

// editorConfigChain collects the .editorconfig files that can speak for path,
// nearest first.
func editorConfigChain(root, absPath string) []ecFile {
	var out []ecFile
	dir := filepath.Dir(absPath)
	bounded := root
	if bounded != "" {
		// Outside the project, the project boundary cannot be the ceiling:
		// the file's own ancestors are the only ones that can describe it.
		if rel, err := filepath.Rel(bounded, absPath); err != nil || strings.HasPrefix(rel, "..") {
			bounded = ""
		}
	}
	for i := 0; i < maxConfigWalk; i++ {
		if text, err := os.ReadFile(filepath.Join(dir, EditorConfigFile)); err == nil {
			f := parseEditorConfig(string(text))
			f.dir = dir
			out = append(out, f)
			if f.root {
				return out
			}
		}
		if bounded != "" && sameDir(dir, bounded) {
			return out
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return out
		}
		dir = parent
	}
	return out
}

// sameDir compares two directory paths the way the walk produced them, so a
// trailing separator or a different drive-letter case on Windows does not
// restart a search that has already reached the boundary.
func sameDir(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if caseInsensitivePaths() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ecFile is one parsed .editorconfig; ecSection is one [glob] block.
type ecFile struct {
	dir      string
	root     bool
	sections []ecSection
}

type ecSection struct {
	patterns []string
	keys     map[string]string
}

// sectionMatches reports whether the file's settings apply to absPath.
// Patterns are matched against the path relative to the .editorconfig's own
// directory, and a pattern with no separator is matched against the file name
// alone — which is what makes "*.py" mean "any Python file, at any depth".
func sectionMatches(patterns []string, dir, absPath string) bool {
	rel, err := filepath.Rel(dir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	base := pathBase(rel)
	for _, p := range patterns {
		target := rel
		if !strings.Contains(p, "/") {
			target = base
		}
		if globMatches(p, target) {
			return true
		}
	}
	return false
}

func pathBase(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// parseEditorConfig reads one file's text. Comments start with # or ;, keys
// and section names are case-insensitive, and the order of the sections is
// kept because "last match wins" is the rule that decides a conflict.
func parseEditorConfig(text string) ecFile {
	var f ecFile
	var cur *ecSection
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.LastIndex(line, "]")
			if end < 0 {
				cur = nil
				continue
			}
			f.sections = append(f.sections, ecSection{
				patterns: splitSectionPatterns(line[1:end]),
				keys:     map[string]string{},
			})
			cur = &f.sections[len(f.sections)-1]
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.ToLower(strings.TrimSpace(line[eq+1:]))
		if cur != nil {
			cur.keys[key] = val
			continue
		}
		// Before the first section header the file speaks about itself.
		if key == "root" {
			f.root = parseECBool(val) == ecYes
		}
	}
	return f
}

type ecBool int8

const (
	ecUnset ecBool = iota
	ecNo
	ecYes
)

// parseECBool reads a boolean the way the format defines it: case-insensitive
// true/false, anything else not an answer at all. An unrecognised value must
// not flip a policy, because the fallback for a key the file did not set is
// the previous section's value or the editor's default.
func parseECBool(v string) ecBool {
	switch v {
	case "true":
		return ecYes
	case "false":
		return ecNo
	}
	return ecUnset
}

// resolveFileOpts turns the merged key map into the settings a caller uses,
// applying the interdependencies the specification spells out: tab_width
// defaults to indent_size, indent_size = tab means the tab width, and
// indent_style = tab with a numeric indent_size describes the display width of
// the tab rather than a unit of spaces.
func resolveFileOpts(merged map[string]string, def FileOpts) FileOpts {
	o := def
	num := func(key string) (int, bool) {
		n, err := strconv.Atoi(merged[key])
		if err != nil || n < 1 {
			return 0, false
		}
		if n > maxIndentSize {
			n = maxIndentSize
		}
		return n, true
	}
	tw, hasTW := num("tab_width")
	is, hasIS := num("indent_size")
	sizeIsTab := merged["indent_size"] == "tab"

	switch merged["indent_style"] {
	case "tab":
		o.UseTabs = true
	case "space":
		o.UseTabs = false
	case "":
		// Only a size given: the size is a number of spaces, because a tab
		// unit is asked for by name, never by width.
		if hasIS || sizeIsTab {
			o.UseTabs = false
		}
	}

	// The two widths default to each other, in this order: what the file
	// named for the stop, then what it named for the level, then the caller.
	switch {
	case hasTW:
		o.TabWidth = tw
	case hasIS:
		o.TabWidth = is
	}
	if o.UseTabs {
		// The typed unit is a tab character, so one level is one stop.
		o.IndentSize = o.TabWidth
	} else if hasIS {
		o.IndentSize = is
	} else {
		// Covers indent_size = tab and the file that only named a width:
		// both mean "as wide as a tab looks here".
		o.IndentSize = o.TabWidth
	}
	if v := parseECBool(merged["trim_trailing_whitespace"]); v != ecUnset {
		o.TrimTrailingWS = v == ecYes
	}
	if v := parseECBool(merged["insert_final_newline"]); v != ecUnset {
		o.InsertFinalNewline = v == ecYes
	}
	return o
}
