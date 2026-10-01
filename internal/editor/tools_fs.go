package editor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Filesystem tools that walk the project (SEARCH, LIST_DIR, GLOB) share the
// rules below, so a new tool cannot accidentally descend into .git or read a
// multi-gigabyte build artifact.
const (
	// maxWalkFile is the largest file a walking tool will read into memory.
	maxWalkFile = 512 * 1024
	// maxListEntries caps LIST_DIR output (and the number of scanned dirs).
	maxListEntries = 200
	// maxGlobHits caps GLOB output.
	maxGlobHits = 200
)

// projectSkip describes which parts of the tree the walking tools ignore.
type projectSkip struct {
	dirs  map[string]bool // lower-cased directory names
	files map[string]bool // lower-cased extensions
}

// newProjectSkip builds the skip set from the editor configuration (shared with
// SEARCH, so a skipped_dir in .dmed.conf also keeps LIST_DIR and GLOB quiet).
func (m *Model) newProjectSkip() projectSkip {
	return newProjectSkipFor(m.cfg.Editor.SkippedDirs)
}

// newProjectSkipFor builds the skip set from a list of directory names, so both
// the editor and a background sub-agent share one rule.
func newProjectSkipFor(skipped []string) projectSkip {
	dirs := make(map[string]bool, len(skipped)+8)
	for _, d := range skipped {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			dirs[d] = true
		}
	}
	// Vendored and generated trees are large and rarely what a model means.
	// .git/.dmed are skipped unconditionally: they hold no project source and
	// .git holds nothing but objects, so relying on configuration for them
	// only means a fresh Model (no config loaded) walks into them.
	for _, d := range []string{".git", ".dmed", "vendor", "target", "dist", "build", "node_modules", "__pycache__"} {
		dirs[d] = true
	}
	files := map[string]bool{}
	for _, ext := range []string{".git", ".dmed", ".cache", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf", ".zip", ".exe", ".dll"} {
		files[ext] = true
	}
	return projectSkip{dirs: dirs, files: files}
}

// skipDir reports whether a directory named name must not be descended into.
func (p projectSkip) skipDir(name string) bool {
	return p.dirs[strings.ToLower(name)]
}

// skipFile reports whether a file is not worth reading (binary, generated or
// simply huge).
func (p projectSkip) skipFile(path string, size int64) bool {
	if p.files[strings.ToLower(filepath.Ext(path))] {
		return true
	}
	return size > int64(maxWalkFile)
}

// walkProject visits every readable file under root in lexical order, calling
// fn. Directories the skip set rejects are pruned, unreadable entries are
// skipped, and returning err from fn stops the walk with that error.
func walkProject(root string, skip projectSkip, fn func(path string, info os.FileInfo) error) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		if info.IsDir() {
			if p != root && skip.skipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if skip.skipFile(p, info.Size()) {
			return nil
		}
		return fn(p, info)
	})
}

// chatListDir lists one directory: entry name, kind and size, directories
// first. It is the cheap way for a model to learn the project layout without
// shelling out to `ls` (which would need confirmation when allow_run = ask).
func (m *Model) chatListDir(dir string) string {
	return m.toolEnv().listDir(dir)
}

// listDirWith is the TUI-free listing used by the chat and by sub-agents.
func (e toolEnv) listDirWith(dir string, skip projectSkip) string {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	full, ok := e.resolve(dir)
	if !ok {
		return "[LIST_DIR error] path outside project root"
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "[LIST_DIR error] " + err.Error()
	}
	type row struct {
		name  string
		isDir bool
		size  int64
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != "." {
			continue // hidden files are noise here
		}
		if e.IsDir() {
			if skip.skipDir(name) {
				continue
			}
			rows = append(rows, row{name: name, isDir: true})
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			continue // never follow links out of the tree
		}
		if skip.skipFile(name, 0) {
			continue
		}
		var size int64
		if info, ierr := e.Info(); ierr == nil {
			size = info.Size()
		}
		rows = append(rows, row{name: name, size: size})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].isDir != rows[j].isDir {
			return rows[i].isDir
		}
		return rows[i].name < rows[j].name
	})
	label := shortenPath(e.root, full)
	if len(rows) == 0 {
		return fmt.Sprintf("[LIST_DIR %s] (empty)", label)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[LIST_DIR %s] %d entries", label, len(rows))
	for i, r := range rows {
		if i >= maxListEntries {
			fmt.Fprintf(&b, "\n… %d more entries omitted", len(rows)-maxListEntries)
			break
		}
		if r.isDir {
			b.WriteString("\n" + r.name + "/")
			continue
		}
		fmt.Fprintf(&b, "\n%s\t%d B", r.name, r.size)
	}
	return b.String()
}

// chatGlob finds files and directories by a path pattern relative to the
// project root, returning one slash-separated path per line. It understands
// ** for "zero or more path segments" on top of path.Match, so **/*.go works
// as models expect.
func (m *Model) chatGlob(pattern string) string {
	return m.toolEnv().glob(pattern)
}

// globIn is the TUI-free pattern search used by the chat and by sub-agents.
func globIn(root, pattern string, skip projectSkip) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "[GLOB error] empty pattern"
	}
	if isAbsPattern(pattern) {
		return "[GLOB error] pattern must be relative to the project root"
	}
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	if _, err := path.Match(pattern, "probe"); err != nil {
		return "[GLOB error] invalid pattern: " + err.Error()
	}
	var hits []string
	addHit := func(rel string) {
		if len(hits) >= maxGlobHits {
			return
		}
		hits = append(hits, rel)
	}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if p == root {
				return nil
			}
			if skip.skipDir(name) {
				return filepath.SkipDir
			}
			if matchGlob(pattern, shortenPath(root, p)) {
				addHit(shortenPath(root, p))
			}
			// Prune directories the pattern provably cannot reach: without
			// this a pattern like *.go would still walk the whole tree.
			if !globMayContain(pattern, shortenPath(root, p)) {
				return filepath.SkipDir
			}
			return nil
		}
		if skip.skipFile(p, info.Size()) {
			return nil
		}
		if matchGlob(pattern, shortenPath(root, p)) {
			addHit(shortenPath(root, p))
		}
		if len(hits) >= maxGlobHits {
			return filepath.SkipAll
		}
		return nil
	})
	if len(hits) == 0 {
		return fmt.Sprintf("[GLOB %s] no matches", pattern)
	}
	out := fmt.Sprintf("[GLOB %s] %d match(es)", pattern, len(hits))
	for _, h := range hits {
		out += "\n" + h
	}
	if len(hits) >= maxGlobHits {
		out += fmt.Sprintf("\n… (capped at %d matches, narrow the pattern)", maxGlobHits)
	}
	return out
}

// isAbsPattern reports whether a glob pattern is anchored outside the project.
// filepath.IsAbs alone is not enough: on Windows "/abs/path" is not "absolute"
// but it still must not be treated as a project-relative pattern.
func isAbsPattern(pattern string) bool {
	if filepath.IsAbs(pattern) {
		return true
	}
	if strings.HasPrefix(pattern, "/") {
		return true
	}
	return len(pattern) >= 2 && pattern[1] == ':'
}

// globMayContain reports whether the walk should descend into the directory
// dirRel. It is deliberately conservative: false is only returned when the
// pattern provably cannot match the directory itself nor anything under it, so
// an over-eager prune (and therefore a silently missing file) is impossible.
func globMayContain(pattern, dirRel string) bool {
	patSegs := strings.Split(pattern, "/")
	dirSegs := strings.Split(filepath.ToSlash(dirRel), "/")
	if len(patSegs) == 1 {
		// A root-only pattern matches nothing below a subdirectory.
		return matchGlob(pattern, path.Base(dirRel))
	}
	return globPrefixAligns(patSegs, dirSegs)
}

// globPrefixAligns reports whether the pattern's segments could still match a
// prefix of the path starting at some position. A ** shifts that starting
// position, which is exactly why this cannot be a plain segment-by-segment
// comparison: `**/*.go` must keep walking internal/agent, where the *.go
// segment aligns with a directory deeper than the current one.
func globPrefixAligns(pat, dir []string) bool {
	if len(pat) == 0 {
		return true // nothing left to constrain the path
	}
	if pat[0] == "**" {
		for i := 0; i <= len(dir); i++ {
			if globPrefixAligns(pat[1:], dir[i:]) {
				return true
			}
		}
		return false
	}
	if len(dir) == 0 {
		return true // the pattern reaches deeper than the path: keep walking
	}
	ok, err := path.Match(pat[0], dir[0])
	if err != nil || !ok {
		return false
	}
	return globPrefixAligns(pat[1:], dir[1:])
}

// matchGlob matches a slash-separated path against a glob pattern. It is
// path.Match plus "**", which stands for zero or more path segments — the one
// extension models reliably assume and path.Match cannot express.
func matchGlob(pattern, name string) bool {
	return globMatchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func globMatchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if globMatchSegments(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return globMatchSegments(pat[1:], name[1:])
}
