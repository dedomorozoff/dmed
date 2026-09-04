package editor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// toolCall is a parsed tool invocation emitted by the LLM in a chat reply.
type toolCall struct {
	name string // "READ" | "SEARCH" | "RUN" | "EDIT"
	arg  string // first line argument for READ/SEARCH/RUN, or file path for EDIT
	body string // full body for EDIT (the new file content)
}

// blockMarkerRe matches any === ... === marker line (tool headers, END TOOL).
var blockMarkerRe = regexp.MustCompile(`(?m)^\s*===`)

// toolHeaderRe matches a tool block header: === TOOL: NAME: arg ===
var toolHeaderRe = regexp.MustCompile(`(?m)^\s*=== *TOOL: *([A-Z]+):?(.*?)===?\s*$`)

// parseToolCalls scans a reply for tool blocks.
// A block is: "=== TOOL: NAME: arg ===" followed by content up to the next
// "=== ... ===" marker (including END TOOL) or end of text.
// Content inside markdown triple-backtick fences is ignored (code examples
// are not tool calls).
func parseToolCalls(reply string) []toolCall {
	var tools []toolCall
	lines := strings.Split(reply, "\n")
	inFence := false
	i := 0
	for i < len(lines) {
		line := strings.TrimRight(lines[i], "\r")
		trim := strings.TrimSpace(line)

		// Toggle markdown fence state.
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			i++
			continue
		}

		if inFence {
			// We're inside a fence and this isn't the triple-backtick line handled
			// above. But because we toggle on the fence opener, subsequent content
			// lines are already handled. The only case left here is when the fence
			// opener did not close yet — skip content.
			i++
			continue
		}

		m := toolHeaderRe.FindStringSubmatch(line)
		if m == nil {
			i++
			continue
		}
		name := strings.TrimSpace(m[1])
		arg := strings.TrimSpace(m[2])
		name = strings.TrimSuffix(name, ":")
		arg = strings.TrimSuffix(arg, "===")
		arg = strings.TrimSpace(arg)

		// Read body until the next === marker line (respecting fences)
		var body []string
		j := i + 1
		fenceOpen := false
		for j < len(lines) {
			bline := strings.TrimRight(lines[j], "\r")
			btrim := strings.TrimSpace(bline)
			if strings.HasPrefix(btrim, "```") {
				fenceOpen = !fenceOpen
				body = append(body, bline)
				j++
				continue
			}
			if !fenceOpen && blockMarkerRe.MatchString(bline) {
				break
			}
			body = append(body, bline)
			j++
		}
		tc := toolCall{name: name, arg: arg, body: strings.Join(body, "\n")}
		if name == "EDIT" {
			tc.body = strings.TrimSuffix(tc.body, "\n")
		}
		tools = append(tools, tc)
		i = j
	}
	return tools
}

// toolSystemPrompt documents the available tools for the LLM.
const toolSystemPrompt = `
You have tools available. When you need to interact with the codebase, emit
exactly one tool per block, then wait for the result. Format:

=== TOOL: READ: <relative-or-absolute path> ===
=== TOOL: SEARCH: <case-insensitive text> ===
=== TOOL: RUN: <shell command> ===
=== TOOL: EDIT: <relative-or-absolute path> ===
<complete new file content, no other decoration>
=== END TOOL ===

Rules:
- READ: read a file to understand it. Nothing after the header.
- SEARCH: find files whose content contains the text. Nothing after the header.
- RUN: execute a shell command (e.g. "go test ./..."). Output is returned.
- EDIT: replace the ENTIRE file with the content between the header and the
  closing marker. Include the complete file content, not a diff.
- You can call multiple tools in one turn, but each must be a separate block.
- After you receive results, continue normally with the next tool or answer.
You must NOT emit these markers as plain text outside of a tool block; if you
just want to show code, use markdown fences.
`

// executeTool runs one tool and returns a result string.
func executeTool(m *Model, tc toolCall) string {
	base := m.root
	if base == "" {
		base = "."
	}
	switch tc.name {
	case "READ":
		path := resolvePath(base, tc.arg)
		data, err := os.ReadFile(path)
		if err != nil {
			return "[READ error] " + err.Error()
		}
		return "[READ " + path + "]\n" + string(data)
	case "SEARCH":
		q := tc.arg
		return searchFilesForContent(base, q)
	case "RUN":
		return runCommand(tc.arg)
	case "EDIT":
		return applyToolEdit(base, tc.arg, tc.body)
	default:
		return "[unknown tool " + tc.name + "]"
	}
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

// runCommand executes a shell command with a timeout and captures output.
func runCommand(cmdline string) string {
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

// applyToolEdit writes the given content to a file (respecting diff review is
// done by the caller; this performs the write).
func applyToolEdit(base, path, content string) string {
	full := resolvePath(base, path)
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "[EDIT error] " + err.Error()
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "[EDIT error] " + err.Error()
	}
	return "[EDIT applied] " + full
}
