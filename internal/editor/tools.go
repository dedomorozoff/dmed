package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dmed/internal/agent"
	"dmed/internal/ai"
)

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
		str("SEARCH", "Find files whose content contains the given text. arg is the search query."),
		str("RUN", "Execute a shell command in the project root and return its output. arg is the command."),
		{
			Name:        "EDIT",
			Description: "Replace the ENTIRE content of a file. Call this only after READING the file.",
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
		return m.chatRead(a.Arg), nil
	case "SEARCH":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return searchFilesForContent(m.root, a.Arg), nil
	case "RUN":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return runCommand(m.root, a.Arg), nil
	case "EDIT":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return "[EDIT error] " + err.Error(), nil
		}
		full := resolvePath(m.root, a.Path)
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

// searchFilesForContent walks the project and returns files containing text.
func searchFilesForContent(base, q string) string {
	if q == "" {
		return "[SEARCH] empty query"
	}
	q = strings.ToLower(q)
	var hits []string
	maxHits := 40
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
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
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(strings.ToLower(string(data)), q) {
			hits = append(hits, path)
		}
		return nil
	})
	if len(hits) == 0 {
		return "[SEARCH] no files contain " + q
	}
	return "[SEARCH " + q + "] " + strings.Join(hits, ", ")
}

// runCommand executes a shell command in dir (the project root) with a timeout
// and captures output.
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
		return "[RUN error] " + err.Error() + "\n" + string(out)
	}
	res := string(out)
	if res == "" {
		res = "(no output)"
	}
	return "[RUN " + cmdline + "]\n" + res
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
