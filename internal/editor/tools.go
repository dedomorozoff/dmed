package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"dmed/internal/agent"
	"dmed/internal/ai"
)

// aiRequestOptions translates the configured AI generation parameters into the
// provider-agnostic Options handed to every request (chat, ghost, inline, agent).
func (m *Model) aiRequestOptions() ai.Options {
	return ai.Options{
		Temperature: m.cfg.AI.Temperature,
		NumCtx:      m.cfg.AI.NumCtx,
		NumPredict:  m.cfg.AI.NumPredict,
	}
}

// chatToolDefs returns the native function definitions exposed to the chat
// model. The model calls these via structured JSON arguments rather than
// emitting fragile text markers, so small local models reliably invoke them.
func chatToolDefs() []ai.ToolDef {
	str := func(name, desc string) ai.ToolDef {
		return ai.ToolDef{
			Name:        name,
			Description: desc,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg": map[string]any{"type": "string", "description": "argument"},
				},
				"required": []string{"arg"},
			},
		}
	}
	return []ai.ToolDef{
		str("READ", "Read the full content of a file. arg is the file path."),
		{
			Name:        "SEARCH",
			Description: "Find matching lines in the project. arg is the search query. Returns path:line:content. Set regex:true to treat arg as a regular expression.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg":   map[string]any{"type": "string", "description": "search query or regular expression"},
					"regex": map[string]any{"type": "boolean", "description": "if true, treat arg as a regular expression (RE2 syntax)"},
				},
				"required": []string{"arg"},
			},
		},
		str("RUN", "Execute a shell command in the project root and return its output. arg is the command."),
		{
			Name:        "REPLACE",
			Description: "Replace a small, unique search block inside a file with new text. Use this for surgical edits instead of rewriting a whole file. Call this only after READING the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "relative or absolute path to the file"},
					"search":  map[string]any{"type": "string", "description": "exact existing text block to replace (must match the file)"},
					"replace": map[string]any{"type": "string", "description": "the new text to put in place of search"},
				},
				"required": []string{"path", "search", "replace"},
			},
		},
		{
			Name:        "EDIT",
			Description: "Replace the ENTIRE content of a file. Use for new files or large rewrites; prefer REPLACE for small surgical edits. Call this only after READING the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "relative or absolute path to the file"},
					"content": map[string]any{"type": "string", "description": "complete new file content"},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

// execChatTool executes one native tool call. It returns the result text fed
// back to the model and, for EDIT, a proposed Change that awaits human diff
// review before it is applied. The returned result for EDIT is a short
// summary; the full proposed content lives in the Change so the chat stays
// compact while the model still learns what was proposed.
func (m *Model) execChatTool(tc ai.ToolCall) (string, *agent.Change) {
	switch tc.Name {
	case "READ":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		fullR, okR := m.authPath(a.Arg)
		if !okR {
			return "[READ error] path outside project root", nil
		}
		return m.chatRead(fullR), nil
	case "SEARCH":
		var a struct {
			Arg   string `json:"arg"`
			Regex bool   `json:"regex"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return m.chatSearch(a.Arg, a.Regex), nil
	case "RUN":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		if strings.EqualFold(m.cfg.AI.AllowRun, "never") {
			return "[RUN blocked] shell execution is disabled (allow_run = never)", nil
		}
		if strings.EqualFold(m.cfg.AI.AllowRun, "ask") {
			// Pause for human confirmation of this specific command instead of
			// running it. The chat loop parks and the user picks y/n; see
			// handleChatRunConfirm.
			return runConfirmMarker(m.root, a.Arg), nil
		}
		return runCommand(m.root, a.Arg), nil
	case "REPLACE":
		var a struct {
			Path    string `json:"path"`
			Search  string `json:"search"`
			Replace string `json:"replace"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return "[REPLACE error] " + err.Error(), nil
		}
		full, ok := m.authPath(a.Path)
		if !ok {
			return "[REPLACE error] path outside project root", nil
		}
		if a.Search == "" {
			return "[REPLACE error] empty search block", nil
		}
		origStr, oerr := readFileStr(full)
		if oerr != nil {
			return "[REPLACE error] " + oerr.Error(), nil
		}
		if !strings.Contains(origStr, a.Search) {
			return "[REPLACE error] search block not found in " + shortenPath(m.baseDir(), full), nil
		}
		newContent := strings.ReplaceAll(origStr, a.Search, a.Replace)
		if !strings.HasSuffix(newContent, "\n") {
			newContent += "\n"
		}
		if newContent == origStr {
			return "[REPLACE] no change for " + shortenPath(m.baseDir(), full), nil
		}
		chg := &agent.Change{Path: full, Orig: origStr, New: newContent}
		return "[REPLACE] proposed update to " + shortenPath(m.baseDir(), full), chg
	case "EDIT":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return "[EDIT error] " + err.Error(), nil
		}
		full, ok := m.authPath(a.Path)
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
		content := a.Content
		if !strings.HasSuffix(content, "\n") && content != "" {
			content += "\n"
		}
		if origStr == content {
			return "[EDIT] no change for " + shortenPath(m.baseDir(), full), nil
		}
		chg := &agent.Change{Path: full, Orig: origStr, New: content}
		return "[EDIT] proposed update to " + shortenPath(m.baseDir(), full), chg
	default:
		return "[unknown tool " + tc.Name + "]", nil
	}
}

// runConfirmMarker returns a special result that makes the chat loop park for
// an explicit yes/no on the command instead of executing it (allow_run = ask).
// It is the only tool result expected to be interpreted as a pending decision.
func runConfirmMarker(dir, cmdline string) string {
	return "\x00DMED_RUN_CONFIRM\x00" + cmdline + "\x00" + dir
}

// readFileStr reads a text file, returning "" with nil error for a missing file
// (consistent with the EDIT create flow).
func readFileStr(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// authPath resolves p against the project root and, when RestrictToRoot is
// enabled, refuses paths that escape the root (so the model cannot read or
// write arbitrary files elsewhere).
func (m *Model) authPath(p string) (string, bool) {
	full := resolvePath(m.root, p)
	if !m.cfg.AI.RestrictToRoot {
		return full, true
	}
	root, _ := filepath.Abs(m.root)
	absFull := full
	if !filepath.IsAbs(absFull) {
		absFull, _ = filepath.Abs(full)
	}
	if root == "" || !strings.HasPrefix(absFull, root) {
		return "", false
	}
	return full, true
}

// chatRead reads a file and returns the content for the model.
func (m *Model) chatRead(path string) string {
	full := resolvePath(m.root, path)
	data, err := os.ReadFile(full)
	if err != nil {
		return "[READ error] " + err.Error()
	}
	return "[READ " + full + "]\n" + string(data)
}

func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// chatSearch walks the project and returns matching lines as
// "path:line:content" entries (plus a little context), skipping ignored dirs
// and oversized files. Returning locations lets the model READ precisely
// instead of guessing which file contains the text.
func (m *Model) chatSearch(q string, regex bool) string {
	if q == "" {
		return "[SEARCH] empty query"
	}
	base := m.root
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
	skip := make(map[string]bool, 0)
	for _, d := range m.cfg.Editor.SkippedDirs {
		if d != "" {
			skip[strings.ToLower(d)] = true
		}
	}
	var hits []string
	const maxHits = 24
	const maxFile = 512 * 1024
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skip[strings.ToLower(filepath.Base(path))] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(hits) >= maxHits {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".git", ".dmed", ".cache", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf":
			return nil
		}
		if info.Size() > int64(maxFile) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel := shortenPath(base, path)
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

// runCommand executes a shell command in dir (the project root) with a timeout
// and captures output (capped so a noisy command cannot flood the conversation).
func runCommand(dir, cmdline string) string {
	if cmdline == "" {
		return "[RUN] empty command"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", cmdline)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdline)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "[RUN error] " + err.Error() + "\n" + capCommandOutput(string(out))
	}
	res := capCommandOutput(string(out))
	if res == "" {
		res = "(no output)"
	}
	return "[RUN " + cmdline + "]\n" + res
}

// capCommandOutput truncates command output so a noisy RUN (e.g. a recursive
// listing) cannot bloat memory or the model's context window.
func capCommandOutput(s string) string {
	const limit = 8 * 1024
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n… (%d more chars)", len(s)-limit)
}

// isPlausibleText reports whether a file is likely readable text; binary files
// (and compiled build artifacts) are skipped when auto-opening tabs.
func isPlausibleText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return true // empty file (e.g. freshly created) is fine to open
	}
	return bytes.IndexByte(buf[:n], 0) == -1
}

// fileState captures the size and modification time of a file, used to detect
// both newly created and modified files after a tool round.
type fileState struct {
	size int64
	mod  int64
}

// snapshotFiles walks root and records every regular file (skipping the .git
// directory) together with its size and modification time, so callers can tell
// what a tool created or rewrote while it ran.
func snapshotFiles(root string) map[string]fileState {
	snap := make(map[string]fileState)
	if root == "" {
		return snap
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		snap[path] = fileState{size: info.Size(), mod: info.ModTime().UnixNano()}
		return nil
	})
	return snap
}
