package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// A slash command is a prompt template the user addresses by name: Enter on
// `/refactor internal/agent` sends the expanded prompt to the model.
//
// Templates come from three places, first match wins:
//
//	<project>/.dmed/templates/<name>.(md|txt|prompt|lua)
//	~/.dmed/templates/<name>.(...)
//   the two built-ins, /refactor and /test
//
// A plain file's first line starting with "# " is its description (the listing
// and the file's own comment are the same line); the rest is the prompt body.
// `{args}` in the body is replaced with what the user typed after the name; a
// body without the placeholder gets the arguments appended on a new line, and
// an empty argument list gets the model a hint to ask. A `.lua` template runs
// with a global `args` and answers with the prompt: either the chunk's return
// value or the global `prompt`, under the same 3-second budget the plugin VM
// keeps — a template that hangs is a broken file, not a hung editor.

const (
	slashTemplateDir     = "templates"
	slashDirName         = ".dmed"
	slashBudget          = 3 * time.Second
	slashBuiltins        = 2 // /refactor and /test, used to size lookup maps
	builtinRefactorBody  = "# Refactor without changing behavior\nRefactor {args} so it reads better, but do not change what it does. Read the relevant files first, keep public APIs intact, and propose the changes as usual — every edit still goes through diff review."
	builtinTestBody      = "# Write tests\nWrite tests for {args}. Look at how this project already writes tests (files, names, framework, style) and follow it. The tests must fail if the behavior regresses."
	slashNoArgsHint      = "\n\nNo target was named: ask the user which file or module they mean (ASK_USER) before proposing edits."
	slashUnboundArgsMark = "{args}"
)

var builtinSlash = []struct {
	name, body string
}{
	{"refactor", builtinRefactorBody},
	{"test", builtinTestBody},
}

// slashCmd is one resolved command and where it came from.
type slashCmd struct {
	Name        string
	Description string
	Body        string // plain template text
	Path        string // template file, empty for a built-in
	Lua         bool
}

// splitSlash reads a leading "/name rest" out of submitted text.
func splitSlash(text string) (name, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	rest := strings.TrimSpace(text[1:])
	if rest == "" {
		return "", "", true // a bare "/": the listing
	}
	i := strings.IndexAny(rest, " \t\n")
	if i < 0 {
		return strings.ToLower(rest), "", true
	}
	return strings.ToLower(rest[:i]), strings.TrimSpace(rest[i+1:]), true
}

// slashCommands lists every command, newest source first: a project template
// shadows the same name in the user's home, which shadows a built-in. The
// scan runs on submit, not at startup — templates are edited in the very
// editor that uses them, and a cached list would be a reload bug waiting to
// be filed.
func (m *Model) slashCommands() map[string]slashCmd {
	cmds := make(map[string]slashCmd, slashBuiltins+8)
	// Built-ins land last so they lose every name a file claims, but the
	// listing always shows the two shipped ones.
	for _, dir := range m.slashTemplateDirs() {
		m.scanSlashDir(dir, cmds)
	}
	for _, b := range builtinSlash {
		if _, dup := cmds[b.name]; !dup {
			cmds[b.name] = slashCmd{Name: b.name, Description: descriptionOf(b.body), Body: b.body}
		}
	}
	return cmds
}

// slashTemplateDirs is project first, then home — the order that gives the
// project the last word in scanSlashDir (later files of the same name in the
// *same* directory lose, across directories the earlier directory wins).
func (m *Model) slashTemplateDirs() []string {
	var dirs []string
	if root := m.baseDir(); root != "" {
		dirs = append(dirs, filepath.Join(root, slashDirName, slashTemplateDir))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, slashDirName, slashTemplateDir))
	}
	return dirs
}

func (m *Model) scanSlashDir(dir string, cmds map[string]slashCmd) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".md" && ext != ".txt" && ext != ".prompt" && ext != ".lua" && ext != ".markdown" {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		if _, dup := cmds[name]; dup {
			continue // an earlier directory (the project) already owns the name
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		cmd := slashCmd{Name: name, Path: filepath.Join(dir, e.Name()), Lua: ext == ".lua"}
		cmd.Body = string(data)
		cmd.Description = descriptionOf(cmd.Body)
		if cmd.Lua {
			cmd.Body = "" // the body is produced at run time, not read from the file
		}
		cmds[name] = cmd
	}
}

// descriptionOf is the first "# " line, or the first line cut short — a
// listing row has a width budget, a template file has a human's name for it.
func descriptionOf(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
		if len(line) > 60 {
			return line[:60]
		}
		return line
	}
	return ""
}

// expandSlash turns submitted "/name args" text into the prompt to send. It
// reports whether the text was a slash command at all; a command that does
// not exist (or the bare "/") is answered with the listing in `notice` and
// sends nothing.
func (m *Model) expandSlash(text string) (prompt, notice string, isCommand bool) {
	name, args, ok := splitSlash(text)
	if !ok {
		return "", "", false
	}
	if name == "" || name == "help" {
		return "", m.slashListing(), true
	}
	cmd, found := m.slashCommands()[name]
	if !found {
		return "", m.t("chat.unknown_slash", "/"+name, m.slashListing()), true
	}
	if cmd.Lua {
		// A Lua template produced the prompt itself: `args` was its input,
		// post-processing would add to what the script already wrote.
		out, err := runLuaTemplate(cmd.Path, args)
		if err != nil {
			return "", m.t("chat.slash_lua_error", cmd.Name, err.Error()), true
		}
		return out, "", true
	}
	return expandTemplate(cmd.Body, args), "", true
}

// expandTemplate substitutes {args} and drops the leading "# " description
// line (a file's comment about itself is not part of the prompt). A body
// without the placeholder gets the arguments on their own line; no arguments
// get the model a hint to ask, so an empty target never reads as named.
func expandTemplate(body, args string) string {
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		if strings.HasPrefix(strings.TrimSpace(body[:i]), "# ") {
			body = body[i+1:]
		}
	} else if strings.HasPrefix(strings.TrimSpace(body), "# ") {
		body = ""
	}
	body = strings.TrimSpace(body)
	if strings.Contains(body, slashUnboundArgsMark) {
		body = strings.ReplaceAll(body, slashUnboundArgsMark, args)
	} else if args != "" {
		body += "\n\n" + args
	}
	if args == "" {
		body += slashNoArgsHint
	}
	return body
}

func (m *Model) slashListing() string {
	cmds := m.slashCommands()
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := make([]string, 0, len(names))
	for _, n := range names {
		row := "/" + n
		if d := firstLine(cmds[n].Description); d != "" {
			row += " — " + d
		}
		rows = append(rows, row)
	}
	return "commands: " + strings.Join(rows, ", ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// runLuaTemplate executes one .lua template file and returns the prompt it
// produces. The contract is the smallest one that fits the format: the chunk
// sees a global `args`, and answers either by returning a string or by
// setting the global `prompt`. It runs on a fresh LState with a wall-clock
// budget — the same defense the plugin framework uses against `while true`.
func runLuaTemplate(path, args string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	L := lua.NewState()
	defer L.Close()
	ctx, cancel := context.WithTimeout(context.Background(), slashBudget)
	defer cancel()
	L.SetContext(ctx)
	L.SetGlobal("args", lua.LString(args))
	fn, err := L.LoadString(string(src))
	if err != nil {
		return "", err
	}
	if err := L.CallByParam(lua.P{Fn: fn, NRet: lua.MultRet, Protect: true}); err != nil {
		return "", err
	}
	if n := L.GetTop(); n > 0 {
		if s, ok := L.Get(-1).(lua.LString); ok {
			return string(s), nil
		}
		L.Pop(n)
	}
	if g := L.GetGlobal("prompt"); g != nil && g.Type() == lua.LTString {
		return string(g.(lua.LString)), nil
	}
	return "", errors.New("the template neither returned a string nor set the global `prompt`")
}
