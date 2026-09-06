# AGENTS.md

Guidance for AI coding agents working on this repository.

## Project

dmed is a terminal code editor (Bubbletea/TUI in Go) with AI agents as
first-class participants. It is well past the original "single-file skeleton":
milestones M0–M5 of [ROADMAP.md](ROADMAP.md) are done (version 0.6.5) — the
editor has splits, multi-cursor, rope-based buffers, syntax highlighting,
file watching, git integration, AI chat/inline/ghost, a background agent
queue with diff-review, LSP client, autocompletion, Lua plugins, and more.
Update ROADMAP checkboxes when you complete work.

The architectural rule of the project — now implemented, not aspirational —
is that agent-proposed changes always pass through **diff review → atomic
apply** before touching buffers or files. Agents never write to a buffer
directly; they emit `Change`s that land through `internal/agent.Applier`
(see below). Keep this rule for all future AI-related code.

## Toolchain gotchas (this machine)

- Use plain `go` from PATH (1.25.0, installed at `D:\go\bin`). No special
  path needed; the old `/usr/lib/go/bin/go` (1.22) is gone. `go.mod` requires
  `go 1.25.0`.
- A stale `GOROOT` used to be exported pointing at Go 1.15; it is clean now,
  but the Makefile still does `GOROOT :=` plus a value-less `export GOROOT`
  as insurance — leave it. The exports and phony lists are written in a form
  that works under both GNU make and FreeBSD's bmake.
- Network fetches need explicit `GOPROXY=https://proxy.golang.org,direct`
  and `GOSUMDB=sum.golang.org` (system defaults are wiped). The Makefile and
  `.github/workflows/ci.yml` both set these.
- Dependencies live on `charm.land` (bubbletea **v2**, lipgloss **v2**):
  `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`. Other notable deps:
  chroma/v2 (syntax), go-git/v5 (git), fsnotify (watching), atotto/clipboard
  (system clipboard), sergi/go-diff (diffs), yuin/gopher-lua (plugins).
  These are pinned in `go.mod`; only bump deliberately and re-run
  `make build && make vet && make test`.

## Commands

```sh
make build          # ./dmed binary
make test           # go test ./...
make vet            # go vet ./...
make run FILE=path  # build + run editor on a file
```

Run `make build && make vet && make test` before considering any change done.

CI (`make vet` + `make test` + native build + 8-platform cross-build) runs on
every push to `main`/`master` and every PR; keep it green.

## Architecture

The code is split into focused internal packages:

- `internal/buffer/` — pure text buffer. Rope-based document
  (`buffer.go`, `doc.go`, `internal/rope`) with cursor, sticky goal column,
  undo/redo with typing-run grouping, and multi-cursor support
  (`multicursor.go`). Must stay free of TUI imports; behavior changes belong
  in the corresponding `_test.go`. Undo grouping: consecutive inserts at
  adjacent positions form one undo step; any other operation breaks the group
  (`beginChange`/`breakGroup`).
- `internal/editor/` — Bubbletea model and UI. `editor.go` holds the model
  and key handling; the rest is split by concern: `view.go` (rendering),
  `finder.go` (fuzzy file finder), `split.go`, `tree.go`, `mouse.go`,
  `completion.go`, `palette.go`, `session.go`, `terminal.go`, `chat.go`
  (AI chat), `ai_inline.go` (inline rewrite → diff review), `ghost.go`
  (Copilot-style ghost text), `agent_panel.go` (agent task queue UI),
  `git_panel.go`, `diffview.go`, `plugins.go`, `plugin_store.go`, `lsp.go`,
  `ai_settings.go`, `transform.go`, `tools.go` (agent READ/SEARCH/RUN/EDIT).
- `internal/agent/` — background AI agents (the project-rule core).
  `queue.go` — thread-safe task queue with progress/cancel; `runner.go` runs
  tasks; `apply.go` is the **atomic Applier**: it validates that every
  `Change.Orig` still matches on-disk content (stale patches reject the whole
  series) and writes all-or-nothing with rollback; `commit.go` turns an
  approved, applied series into a single git commit. See `model.go` for the
  `Task`/`Change`/`Status` lifecycle.
- `internal/ai/` — LLM providers: `provider.go` interface, `ollama.go`
  (local), `openai.go` (OpenAI-compatible, SSE streaming). `[ai]` config.
- `internal/config/` — INI `.dmed.conf` loader (`[editor]`, `[ai]`, `[agent]`,
  `[ui]`, `[plugins]`); priority defaults < global < project < env vars;
  hot-reload on save; `WriteAI`/`WriteLang` merge helpers.
- `internal/lsp/` — JSON-RPC 2.0 LSP client over stdin/stdout (diagnostics,
  definition, didOpen/didChange); wired into autocompletion and the gutter.
- `internal/plugin/` — gopher-lua plugin framework (`dmed.*` API); plugins
  load from `~/.dmed/plugins` and `<project>/.dmed/plugins` with hot-reload.
- `internal/vcs/` — git via go-git (pure Go, no system git needed): status in
  the gutter, hunks, side-by-side diffs, stage/unstage, commit, history,
  branches. `internal/watcher/` — fsnotify external-change detection.
  `internal/events/` — pub/sub bus (`file:changed`, `buffer:modified`,
  `buffer:saved`, `git:updated`, `agent:updated`) linking buffers ← watchers
  ← agents.
- `internal/bundled/` — built-in plugins shipped with the binary (emmet,
  snippets). `internal/i18n/` — en/ru strings. `internal/session/` —
  save/restore open files across restarts. `internal/syntax/` — highlighting.
- `main.go` — CLI entry only (`dmed [dir | files...]`, `-h`, `-v`).

Conventions:

- Buffer state is the single source of truth; the view renders from it and
  holds no editable state of its own.
- No global mutable state; models are values, mutations happen through
  pointer receivers on small, named methods.
- Files are stored with a trailing newline; dirty check compares against the
  normalized saved snapshot (`MarkSaved`/`Dirty`).
- Agent edits go through `internal/agent.Applier`: validate all
  `Change.Orig` against current content, then write atomically with rollback;
  an approved series becomes one git commit (`agent: <prompt first line>`).
  Do not bypass this for AI-produced edits.
- New packages get focused unit tests; TUI behavior is verified manually.

## Manual verification

Unit tests cover the buffer core, rope, agent applier/queue/commit, AI
providers, LSP, plugins, git, and editor panels. There is no automated TUI
test yet.

Known-broken on this machine: the old cygwin recipe
`printf ... | script -qec './dmed f' /dev/null` does not work with the native
Windows binary. Use a ConPTY harness instead (pywinpty is installed):

```python
from winpty import PtyProcess
p = PtyProcess.spawn(r"<repo>\dmed.exe", cwd=workdir, dimensions=(24, 80))
p.write("text\x13\x11")  # type, Ctrl+S save, Ctrl+Q quit
```

Caveats of the winpty agent: most ctrl-chords arrive as plain letters
(`\x02`→`b`, `\x06`→`f`) and F-key byte sequences fall apart, so only
verify keybindings in a real terminal (Windows Terminal / mintty).
`DMED_DEBUG_KEYS=1` echoes recognized keys into the status bar.
Timing is flaky: capture several seconds after each keystroke before
asserting on output; headless ptys may report a zero window size (the model
guards against non-positive `WindowSizeMsg` values, keep that guard intact).