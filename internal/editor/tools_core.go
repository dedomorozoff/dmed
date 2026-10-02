package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dmed/internal/agent"
)

// toolEnv is the TUI-free context every filesystem tool needs: where the project
// is, what to skip, and whether paths are confined to the root.
//
// It exists so the same tool implementations serve both callers that own a
// Model (the chat) and callers that must not (a background sub-agent, which runs
// on the queue worker goroutine and may not touch editor state).
type toolEnv struct {
	root     string
	skip     projectSkip
	restrict bool
}

// toolEnv returns the chat's tool context.
func (m *Model) toolEnv() toolEnv {
	return toolEnv{
		root:     m.root,
		skip:     m.newProjectSkip(),
		restrict: m.cfg.AI.RestrictToRoot,
	}
}

// newToolEnv builds a context for a background task that has no Model.
func newToolEnv(root string, cfg configAI) toolEnv {
	return toolEnv{root: root, skip: newProjectSkipFor(cfg.SkippedDirs), restrict: cfg.RestrictToRoot}
}

// configAI is the slice of the configuration a toolEnv needs. It exists so
// tools_fs.go does not have to import the config package and so a test can build
// an environment with three fields.
type configAI struct {
	SkippedDirs    []string
	RestrictToRoot bool
}

// resolve turns a model-supplied path into an absolute one, refusing anything
// outside the project root when restrict is set — the model must not read or
// write arbitrary files elsewhere.
func (e toolEnv) resolve(p string) (string, bool) {
	full := resolvePath(e.root, p)
	if !e.restrict {
		return full, true
	}
	root, _ := filepath.Abs(e.root)
	absFull := full
	if !filepath.IsAbs(absFull) {
		absFull, _ = filepath.Abs(full)
	}
	if root == "" || !strings.HasPrefix(absFull, root) {
		return "", false
	}
	return full, true
}

// read returns the file content for the model.
func (e toolEnv) read(path string) string {
	full, ok := e.resolve(path)
	if !ok {
		return "[READ error] path outside project root"
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "[READ error] " + err.Error()
	}
	return "[READ " + full + "]\n" + string(data)
}

// search walks the project and returns matching lines as "path:line:content".
// Returning locations lets the model read precisely instead of guessing which
// file contains the text.
func (e toolEnv) search(q string, regex bool) string {
	if q == "" {
		return "[SEARCH] empty query"
	}
	pattern := q
	var re *regexp.Regexp
	if regex {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "[SEARCH error] invalid regex: " + err.Error()
		}
	} else {
		pattern = strings.ToLower(strings.TrimSpace(q))
	}
	var hits []string
	const maxHits = 24
	_ = walkProject(e.root, e.skip, func(path string, _ os.FileInfo) error {
		if len(hits) >= maxHits {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel := shortenPath(e.root, path)
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if len(hits) >= maxHits {
				return filepath.SkipAll
			}
			var ok bool
			if re != nil {
				ok = re.MatchString(line)
			} else {
				ok = strings.Contains(strings.ToLower(line), pattern)
			}
			if ok {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return "[SEARCH] no matches for " + q
	}
	return "[SEARCH " + q + "]\n" + strings.Join(hits, "\n")
}

// replace proposes a surgical edit: replace one exact block inside a file.
func (e toolEnv) replace(path, block, with string) (string, *agent.Change) {
	full, ok := e.resolve(path)
	if !ok {
		return "[REPLACE error] path outside project root", nil
	}
	if block == "" {
		return "[REPLACE error] empty search block", nil
	}
	orig, err := readFileStr(full)
	if err != nil {
		return "[REPLACE error] " + err.Error(), nil
	}
	if !strings.Contains(orig, block) {
		return "[REPLACE error] search block not found in " + shortenPath(e.root, full), nil
	}
	newContent := strings.ReplaceAll(orig, block, with)
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	if newContent == orig {
		return "[REPLACE] no change for " + shortenPath(e.root, full), nil
	}
	return "[REPLACE] proposed update to " + shortenPath(e.root, full),
		&agent.Change{Path: full, Orig: orig, New: newContent}
}

// edit proposes a whole-file rewrite, creating the file when it does not exist.
func (e toolEnv) edit(path, content string) (string, *agent.Change) {
	full, ok := e.resolve(path)
	if !ok {
		return "[EDIT error] path outside project root", nil
	}
	orig, err := os.ReadFile(full)
	origStr := ""
	if err == nil {
		origStr = string(orig)
	} else if !os.IsNotExist(err) {
		return "[EDIT error] " + err.Error(), nil
	}
	if !strings.HasSuffix(origStr, "\n") && origStr != "" {
		origStr += "\n"
	}
	if !strings.HasSuffix(content, "\n") && content != "" {
		content += "\n"
	}
	if origStr == content {
		return "[EDIT] no change for " + shortenPath(e.root, full), nil
	}
	return "[EDIT] proposed update to " + shortenPath(e.root, full),
		&agent.Change{Path: full, Orig: origStr, New: content}
}

// listDir returns the entries of one directory: name, kind and size.
func (e toolEnv) listDir(dir string) string { return e.listDirWith(dir, e.skip) }

// glob returns the paths matching a pattern, with ** meaning "zero or more
// path segments".
func (e toolEnv) glob(pattern string) string { return globIn(e.root, pattern, e.skip) }
