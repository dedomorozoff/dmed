# dmEd (Developer-Machine Editor)

![GitHub Release](https://img.shields.io/github/v/release/dedomorozoff/dmed)
[![Go Version](https://img.shields.io/github/go-mod/go-version/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
[![License](https://img.shields.io/github/license/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed/blob/main/LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/dedomorozoff/dmed/total)

**dmEd** is a terminal-native, keyboard-driven code editor in Go: rope-backed buffers, multi-cursor editing, splits, LSP, a debugger, a persistent terminal and zero-dependency git — fast over SSH, instant from a cold start.

AI is part of the workflow, not the point of it: agents read, propose and run — but **every diff is reviewed by a human before anything is written**, and accepted work can be undone from the editor itself.

## Key Highlights

- **Built for the keyboard.** Every feature has a chord and a palette entry; nothing is mouse-only, and the mouse support that exists (gutter fold clicks, panel clicks, the debug tree) works on the same state the keys drive.
- **Fast by construction.** Syntax highlighting, git diff and language servers all run off the UI thread; a keystroke on a 60k-line file paints in milliseconds.
- **Zero external dependencies.** Go binaries only: git (go-git), SQLite (modernc.org/sqlite), syntax (Chroma) — no `git`, no CGO, nothing to install on the machine you SSH into.
- **AI with a hard stop.** Chat, inline rewrite, ghost suggestions and background agents — all ending in the same diff-review → atomic-apply pipeline.

## Features

### Editor Core

- Rope-backed buffer with full **undo/redo** (`Ctrl+Z`/`Ctrl+R`), cheap on large files
- **Multi-cursor**: `Alt+D` adds a cursor at the next word occurrence, `Alt+Click` anywhere, `Esc` exits
- **Tabs and splits** (vertical `Ctrl+\`/`F6`, horizontal `Ctrl+Alt+H`/`F7`, focus/paste across panes)
- **Syntax highlighting** (Chroma) for 100+ languages, styles configurable (`syntax_theme`), streamed in the background
- **Code folding** (`Alt+C`/`Alt+O`, gutter chevrons, mouse-driven, hidden-line counts)
- **Find & replace** in file (`Ctrl+F`/`Ctrl+H`) with regex; `F3`/`Shift+F3` walk matches
- **Word wrap** (`Alt+Z`), sticky goal column, move lines (`Alt+↑/↓`), duplicate/delete line, `Ctrl+/` toggle comment
- **Fuzzy file finder** (`Ctrl+O`/`F3`) over the project tree
- **Search everywhere** (`Alt+P`, `Ctrl+Shift+P`, double `Shift`): commands, project files *and* settings in one JetBrains-style list; `Ctrl+P` searches commands alone
- Files are stored with a trailing newline; dirty state is exact (normalized snapshots)

### Files & Project

- **Project tree** (`Ctrl+B`/`F9`): fold/unfold dirs, new file/folder (`n`/`N`), rename (`r`), duplicate (`d`), delete/trash (`Delete`/`t`), file names stay readable at any depth or panel width
- **fsnotify watcher**: external changes offer reload/auto-merge; renamed files keep tabs
- **Sessions**: open files restore across restarts
- Config wired through `.dmed.conf` (INI, hot-reload on save)

### Git (pure Go — no git binary)

- Gutter indicators: added `+`, modified `~`, deleted `_`
- **Side-by-side diff** vs HEAD; per-line **inline blame** (`Alt+B`)
- Stage/unstage/commit from the Git panel (`Ctrl+G`); fetch/push to `origin` (`f`/`p`)
- Branches: create/switch; **history and per-commit diffs**
- Hunk navigation (`Alt+[`/`Alt+]`)

### Databases & Pictures

- **SQLite browser**: a `.sqlite`/`.db` file (magic-header checked, so a text-only `.db` stays a text tab) opens a paged table browser — substring filter over every column (`/`), sorting (`o`), the CREATE statement (`s`), immediate cell editing (`Enter`, empty = NULL), row insert (`Ctrl+N`)/delete (`Ctrl+D`). Views and `WITHOUT ROWID` tables open read-only.
- **ASCII picture preview**: png/jpg/jpeg/gif/bmp render as half-block art with the terminal's colour profile (graceful ANSI/ASCII fallback), GIFs animate, tall pictures scroll (wheel included), tabs are read-only, the watcher re-decodes changes.

### Developer Tools

- **Persistent built-in terminal** (`Alt+T`): ConPTY/PTY shell with ANSI colors, resize, paste, mouse forwarding and full-screen TUI support
- **LSP**: diagnostics in the gutter, completion, go-to-definition (`F12`/`Ctrl+Click`), **rename** (`Ctrl+Alt+R`), **code actions** (`Ctrl+Alt+Q`), **format document** (`Ctrl+Alt+F` — the language server lays out the file, `gofmt` via gopls, `prettier` via the TS server; the result lands in the buffer unsaved, `Ctrl+S` keeps it). Missing servers print their install hint.
- **Autocompletion** (`Ctrl+Space`, auto-trigger): buffer words + LSP for Go, Python, TS/JS, Rust, C/C++, Lua, Ruby, PHP, Zig, JSON, YAML, CSS, HTML
- **Debugger** (`Ctrl+Alt+D`): DAP-based — gutter breakpoints (`F4` or left-click, bookmarks on middle-click), run/continue (`F5`), steps (`F6`/`F7`/`Shift+F7`), stop (`Shift+F5`); a threads/stack/variables/console panel that is fully mouse-driven with expression eval. Go/Delve and PHP/Xdebug (own DBGp client) out of the box; any other adapter (debugpy, lldb-dap, node/js-debug) via `[debug]` config.
- **Lua plugins**: keybindings, palette commands and events; hot-reload; built-in plugin store
- **Localization** en/ru, switchable from the palette
- **Command palette** (`Ctrl+P`/`F2`) — everything above is reachable, searchable and documented there

### AI (second to the editor, wired to the same review gate)

- **Chat** (`Alt+A`): streaming conversation with project context; **inline rewrite** (`Alt+I`) rolls a selected region through a model, **ghost suggestions** (`Alt+G`) autocomplete inside the editor
- **Works with zero setup**: no provider, no key, no account — a session falls back to a keyless public provider and says so (a `CLOUD` badge stays in the chat header). `free_fallback = false` keeps every request local; with a running Ollama the chat just works.
- **Tools the model can call**: `READ`, `SEARCH` (regex), `LIST_DIR`, `GLOB`, `RUN`, `REPLACE`, `EDIT`, `TODO_*` (a visible plan), `ASK_USER` (the loop waits for your answer), `SWITCH_MODE`, `WEB_SEARCH` (opt-in DuckDuckGo) and `SUB_AGENT` (a delegated task whose diff waits in the same review panel). Read-only tools need no confirmation; every proposed change arrives as a diff to accept or reject
- **Plan mode**: the model gets read-only tools and has to ask you before it may edit anything
- **Background agents** (`Alt+L`): queue a refactoring task, watch progress, review the multi-file diff, cancel; **accept is committed to git, and `u` in the panel reverts it** — the undo refuses files you edited after the apply and lands as its own `agent: revert <hash>` commit
- **Provider presets** (wizard cycles with `←`/`→`): **Ollama** (local), **OpenAI**, **DeepSeek**, **Groq**, **LM Studio**, **vLLM**, **Unsloth** or any OpenAI-compatible server; the model list is fetched live from the server as soon as the panel opens
- **First-time setup, three ways**: `dmed setup-ai` (CLI wizard), `Ctrl+P → AI: Preferences...` (test with `t`, save with `Ctrl+S`), or environment variables (`DMED_PROVIDER`, `DMED_API_KEY`, `DMED_MODEL`, `DMED_OLLAMA_URL`):

```sh
DMED_PROVIDER=DeepSeek DMED_API_KEY=sk-... DMED_MODEL=deepseek-chat dmed
```

## Install

One-liner (installs a prebuilt binary, no Go needed):

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Every release ships ready binaries for **linux amd64/arm64, macOS amd64/arm64, FreeBSD, OpenBSD, NetBSD and Windows amd64**, plus packages: `.deb` (Debian/Ubuntu), `.rpm` (Fedora), `.pkg.tar.zst` (Arch), a Termux `.deb` and a Windows `.zip` — see the [Releases page](https://github.com/dedomorozoff/dmed/releases).

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

Or with Go directly:

```sh
go install github.com/dedomorozoff/dmed@latest
```

> Requires Go **>= 1.26** to build; the one-liner installs to `~/.local/bin`
> (Linux/macOS/BSD) or `%LOCALAPPDATA%\dmed` (Windows) — add it to `PATH` if
> needed.

## Usage

```sh
dmed path/to/file.txt          # open file (creates if missing)
dmed dir/                      # open folder — shows project tree
dmed a.txt b.txt               # multiple files → tabs
```

Open a file (`Ctrl+O` fuzzy finder or `Ctrl+T`), edit, `Ctrl+S` to save. `Ctrl+G` for git, `Alt+T` for the terminal, `Alt+A` for AI, `Ctrl+P` for everything else.

## Keybindings

### Navigation

| Keys | Action |
|------|--------|
| `Arrows`, `Home`/`End`, `PgUp`/`PgDn` | Cursor / line / page |
| `Ctrl+↑/↓` | Scroll without moving cursor |
| `Ctrl+L` | Go to line (`N`, `N:C`, or `+N`/`-N` relative) |
| `Alt+←` / `Alt+→` | Switch tabs |
| `Alt+1..9` | Jump to tab N |

### Editing

| Keys | Action |
|------|--------|
| `Shift+Arrows` | Select text |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste (`Ctrl+X` closes the tab when nothing is selected) |
| `Ctrl+Z` / `Ctrl+R` | Undo / redo |
| `Ctrl+Y` / `Ctrl+D` | Delete / duplicate line |
| `Ctrl+/` | Toggle comment |
| `Alt+Z` | Toggle word wrap |
| `Alt+B` | Toggle inline git blame |
| `Alt+↑` / `Alt+↓` | Move line up / down |
| `Alt+D` / `Alt+Click` | Multi-cursor (next word / at click), `Esc` exits |
| `Alt+C` / `Alt+O` | Fold / unfold block at cursor |
| `Ctrl+U` | Uppercase selection / buffer |
| `Ctrl+Space` | Autocomplete (word + LSP) |
| `Enter` / `Backspace` / `Delete` | Standard editing |

### Files & Tabs

| Keys | Action |
|------|--------|
| `Ctrl+S` | Save (untitled: Save As) |
| `Ctrl+T` | Open file by path |
| `Ctrl+O` / `F3` | Fuzzy file finder |
| `Ctrl+W` / `Ctrl+X` | Close tab (last quits; `Ctrl+X` cuts a selection instead) |

### Splits & Panels

| Keys | Action |
|------|--------|
| `Ctrl+\` / `F6` | Split vertical |
| `Ctrl+Alt+H` / `F7` | Split horizontal |
| `Ctrl+Alt+P` / `F8` | Focus other pane |
| `Ctrl+Alt+W` | Close pane |
| `Ctrl+B` / `F9` | Toggle project tree |
| `Ctrl+G` | Git panel |
| `Alt+T` | Terminal |
| `Alt+A` / `Alt+L` | AI chat / Agent panel |

The status bar icons (`▤` tree, `⎇` git, `✦` AI, `◉` debug, `❯` terminal) are clickable and show their shortcut on hover.

### Debug (`Ctrl+Alt+D` opens the panel)

| Keys | Action |
|------|--------|
| `F4` | Toggle breakpoint at cursor (left-click gutter; middle-click = bookmark) |
| `F5` | Run / continue / pause a running debuggee |
| `Shift+F5` | Stop session |
| `F6` / `F7` / `Shift+F7` | Step over / in / out (while the panel is open) |
| `Tab` (in panel) | Cycle threads / stack / variables / eval input |
| Click / wheel (in panel) | Select thread, frame or variable; `▸` expands |

### LSP | Search | General

| Keys | Action |
|------|--------|
| `F12` / `Ctrl+Click` | Go to definition |
| `Ctrl+Alt+R` | Rename symbol (LSP, across files) |
| `Ctrl+Alt+Q` | Code actions (quick fixes, refactors) |
| `Ctrl+Alt+F` | Format document (LSP; buffer stays unsaved until `Ctrl+S`) |
| `Ctrl+F` / `F3` / `Shift+F3` | Find / next / previous match |
| `Ctrl+H` | Find & replace |
| `Ctrl+P` / `F2` | Command palette |
| `Alt+P` / `Ctrl+Shift+P` | Search everywhere (double `Shift` works where the terminal reports it) |
| `F1` / `Ctrl+E` | Help overlay |
| `Ctrl+Q` | Quit (a dirty buffer asks first) |
| `Ctrl+C` | Copy the selection (never quits) |

## Configuration

`.dmed.conf` in your home directory (global) or project root (project wins), hot-reload on save.

```ini
[editor]
tab_width = 4
syntax_theme = monokai      # any chroma style name
line_numbers = true
word_wrap = false           # wrap long lines to pane width (Alt+Z toggles)
skipped_dirs = .git,node_modules,vendor

[ai]                        # everything below defaults sensibly; the wizard writes it
provider = Pollinations (free, no key)   # wizard presets: Ollama (local) | OpenAI | DeepSeek | Groq | LM Studio (local) | vLLM (local) | Unsloth (local) | Custom
model =                      # empty = first model reported by the server
ollama_url = https://text.pollinations.ai   # base URL, no /v1 or /openai suffix (it is appended automatically)
api_key =
api_path =                   # endpoint prefix; /v1 by default, /openai for Pollinations (set by the wizard)
models_path =                # /v1/models by default, /models for Pollinations
context_max = 6000           # max runes sent as file context
temperature = 0              # tenths (7 => 0.7); 0 = provider default
tool_rounds = 0              # chat tool-loop cap; 0 = built-in (6)
allow_run = ask              # always | never | ask — ask (default) confirms each shell command
restrict_to_root = true      # keeps READ/EDIT/REPLACE inside the project root
mode = act                   # act | plan — plan exposes read-only tools only
web_search = false           # true enables WEB_SEARCH (DuckDuckGo), budgeted per session
web_search_budget = 20
free_fallback = true         # keyless provider when nothing is configured (a CLOUD badge marks it)
system_prompt =

[agent]                      # background agent tasks
system_prompt =
context_max = 262144
subagent_prompt =
subagent_rounds = 0

[ui]
tree_width = 25
chat_width_pct = 40
lang = en                    # en | ru

[plugins]
repo = dedomorozoff/dmed
dir = plugins
branch = main

[debug]                      # DAP debugger (Go/Delve by default)
mode = debug                 # launch mode passed to the adapter (debug/test/exec for Delve)
program =                    # package dir / executable; empty = active file dir
args =
adapter_cmd = dlv            # adapter binary (dlv, debugpy-adapter, lldb-dap, node, ...)
adapter_mode = reverse       # reverse (adapter dials us back) | stdio
launch_type = go
launch_request = launch      # launch | attach
launch_json =                # raw JSON merged into the launch body
```

Example — debug a Python module through debugpy (a stdio DAP adapter):

```ini
[debug]
adapter_cmd = debugpy-adapter
adapter_mode = stdio
launch_type = python
program = /path/to/app
launch_json = {"justMyCode": false}
```

The VS Code "JavaScript Debugger" (`ms-vscode.js-debug`, adapter script `src/dap.js`) is auto-detected for `.js/.ts` files from `~/.vscode*/extensions`, `~/.cursor/extensions` and `~/node_modules`; `DMED_JS_DEBUG` pins it explicitly. Attaching to `node --inspect-brk app.js`: `launch_request = attach`, `launch_json = {"port": 9229}`.

## Documentation

- [Plugins (Lua)](docs/PLUGINS.md) — write keybindings, palette commands, events
- [Autocompletion & LSP](docs/AUTOCOMPLETION.md) — completion popup + language servers
- [M4 Agents](docs/M4-AGENTS.md) — background agent tasks, queue, diff-review apply
- [ROADMAP.md](ROADMAP.md) — milestones, features and their design notes
- [docs/RELEASE-v0.9.3.md](docs/RELEASE-v0.9.3.md) — the latest release notes

## Known Limitations

Worth knowing before you file a bug:

- **Keybindings are verified in real terminals, not by CI.** Automated harnesses lose most Ctrl-chords and break F-key sequences, so the binding table is checked manually (Windows Terminal / mintty). dmed keeps a Windows-native binary, not a cygwin one — msys/pty follow the usual windows caveats.
- **The default AI provider is a public keyless service (Pollinations).** Prompts and code leave the machine out of the box; prefer local by picking `Ollama (local)` in `Ctrl+P → AI: Preferences...`, and/or set `free_fallback = false`. The free tier is rate-limited (roughly one request per 15 s).
- **`web_search` is off by default** — it is the only tool that reaches outside the workspace; enable it deliberately and watch the budget.
- **Small local models pick tools worse as the list grows.** Trim the surface with `tools_enabled` (whitelist) or `tools_disabled` (blacklist); the names accept common aliases (`read_file`, `grep`, `run_command`, `write_file`, `edit_file`).
- There is no automated TUI test — the editor is verified by hand for interactive flows; the core (buffer, rope, applier, providers, git, debug clients) is unit-tested.
- **`php-test/`** is a manual fixture for Xdebug debugging, not used by automated tests — see [php-test/README.md](php-test/README.md).

## Architecture

```
internal/buffer/     pure text buffer + undo/redo (no TUI deps)
internal/rope/       persistent line-rope backing the buffer (O(1) undo)
internal/editor/     Bubbletea model: keys, tabs, splits, rendering
internal/agent/      background agents: queue, runner, atomic applier, git commit
internal/ai/         provider interface: Ollama + OpenAI-compatible (streaming)
internal/config/     INI parser, hot-reload, defaults, env overrides
internal/lsp/        JSON-RPC 2.0 LSP client (diagnostics, def, rename, actions, format)
internal/dap/        DAP client (framing, stdio/reverse launchers)
internal/dbgp/       DBGp client for Xdebug (PHP): NUL-framed XML protocol
internal/sqlitedb/   TUI-free SQLite data layer (modernc.org/sqlite, pure Go)
internal/imgprev/    TUI-free ASCII terminal preview of images
internal/plugin/     Lua plugin framework (gopher-lua)
internal/bundled/    embedded official plugins for the plugin store
internal/i18n/       en/ru localization catalogs
internal/syntax/     Chroma-based highlighting (streamed)
internal/vcs/        pure-Go git operations (go-git)
internal/watcher/    fsnotify file watcher
internal/events/     internal event bus
internal/todo/       the model's task checklist
internal/ask/        questions the model asks the user
internal/websearch/  opt-in DuckDuckGo search for WEB_SEARCH
internal/session/    save/restore of open files across restarts
internal/terminal/   persistent built-in terminal (ConPTY/PTY + ANSI)
internal/ptyterm/    low-level pty plumbing behind the terminal
internal/debug/      panic-capture reporting for background goroutines
main.go              CLI entry point
```

## Roadmap

| Milestone | Status |
|-----------|--------|
| M0 — Editor skeleton | ✅ Done |
| M1 — Multi-file, tabs, splits, finder, tree, move lines | ✅ Done |
| M2 — fsnotify, git gutter, event bus | ✅ Done |
| M3 — AI chat, inline rewrite, OpenAI-compatible providers | ✅ Done |
| M4 — Background agents (multi-file tasks, task queue) | ✅ Done |
| M5 — LSP, terminal, plugins, sessions, config | ✅ Done |
| M6 — AI onboarding (provider presets, setup wizard) | ✅ Done |
| M7 — Debugging (DAP/Delve + generic adapters) | ✅ Done |

Post-milestone work (SQLite browser, picture preview, Tree-panel width, LSP formatting, agent undo) is tracked in [ROADMAP.md](ROADMAP.md) together with the design rationale.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
