# dmEd (Developer-Machine Editor)

![GitHub Release](https://img.shields.io/github/v/release/dedomorozoff/dmed)
[![Go Version](https://img.shields.io/github/go-mod/go-version/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
[![License](https://img.shields.io/github/license/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed/blob/main/LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/dedomorozoff/dmed/total)

**dmEd** is a terminal-native, keyboard-driven code editor designed for the era of AI-assisted development. Built entirely in Go, it treats AI agents not as plugins, but as **first-class participants** in your workflow. 

AI agents can read, propose, and apply changes directly to your codebase — but **humans approve every single diff**. Nothing ever happens behind your back.

## Key Highlights

*   **Keyboard-First & Terminal-Native:** Fast, lightweight, and works seamlessly over SSH.
*   **Human-in-the-Loop AI:** High-level autonomy for agents (Ollama, OpenAI, DeepSeek, etc.) with 100% human control via explicit diff reviews.
*   **Zero-Dependency Git:** Native side-by-side diffs, gutter indicators, and staging directly from the editor without needing an external git binary.
*   **All-in-One Dev Environment:** Built-in persistent terminal, LSP diagnostics,
    autocompletion, and a plugin store out of the box.


## Features

### Editor Core

- Multi-file editing with **tabs** and **splits** (vertical/horizontal)
- Fuzzy file finder (`Ctrl+O`) with subsequence scoring
- Project tree sidebar (`Ctrl+B`) with fold/unfold navigation
- Syntax highlighting (Chroma) for 100+ languages
- Find & replace (`Ctrl+F` / `Ctrl+H`) with regex support
- Move lines up/down (`Alt+↑/↓`) with undo
- Full undo/redo with typing-run grouping
- Clipboard: copy (`Ctrl+C`), cut (`Ctrl+X`), paste (`Ctrl+V`)
- Multi-selection with `Shift+Arrows`
- Uppercase selection or whole buffer (`Ctrl+U`)
- Rope-backed buffer: O(1) undo, cheap branching
- Configurable via `.dmed.conf` (INI format, hot-reload on save)

### AI Integration

- **Chat panel** (`Alt+A`) — streaming conversation with your code
- **Works with zero setup**: no provider, no key, no account — the session falls
  back to a keyless public provider and says so in the chat (a `CLOUD` badge
  stays in the header). Set `free_fallback = false` to keep every request local.
- **Inline rewrite** (`Alt+I`) — select text, describe change, review diff, accept/reject
- **Tools the model can call**: `READ`, `SEARCH` (regex-capable), `LIST_DIR`,
  `GLOB`, `RUN`, `REPLACE`, `EDIT`, `TODO_WRITE`/`TODO_SET`/`TODO_READ` (a
  visible plan), `ASK_USER` (a question that pauses the loop until you answer),
  `SWITCH_MODE` (ask to leave plan mode) and `WEB_SEARCH` (DuckDuckGo, opt-in),
  plus `SUB_AGENT` to delegate a self-contained task to a background sub-agent
  (read-only plus proposals — its diff waits in the agent panel, `Esc` cancels).
  Read-only tools need no confirmation; every proposed change arrives as a diff
  for you to accept or reject
- **Plan mode** (`Ctrl+P` → `AI: Toggle Plan/Act Mode`): the model gets read-only
  tools, writes down a plan, and asks you before it is allowed to edit anything
- **Zero-config start**: with a running Ollama the chat just works — the first
  model reported by the server is picked automatically
- Built-in provider presets (wizard cycles them with `←`/`→`):
  **Ollama** (local, free), **OpenAI**, **DeepSeek**, **Groq**,
  **LM Studio** (local), **vLLM** (local), **Unsloth** (local), or any OpenAI-compatible server
- **First-time setup, three ways**:
  - `dmed setup-ai` — interactive CLI wizard (provider → key → test → saved)
  - In the editor: `Ctrl+P` → `AI: Preferences...` — pick a preset with
    `←`/`→`, paste the API key, press `t` to test the connection, `Ctrl+S` to save.
    The **model list is fetched from the provider as soon as the panel opens**,
    so the Model row shows what the server actually has (`←`/`→` to walk it)
    instead of asking you to guess a name
  - Environment variables (highest priority, nothing written to disk):
    `DMED_PROVIDER`, `DMED_API_KEY`, `DMED_MODEL`, `DMED_OLLAMA_URL`

```sh
# example: one-off run against DeepSeek without touching any config file
DMED_PROVIDER=DeepSeek DMED_API_KEY=sk-... DMED_MODEL=deepseek-chat dmed
```

### Change Tracking

- **fsnotify** file watcher — detects external changes, offers reload/auto-merge
- **Git integration** (pure Go, no git binary required):
  - Gutter indicators: added `+`, modified `~`, deleted `_`
  - Side-by-side diff view vs HEAD
  - Stage/unstage/commit from the Git panel (`Ctrl+G`)
  - Fetch / push to `origin` from the Git panel (`f` / `p`, in the status line)
  - Inline per-line blame (`Alt+B`): who changed each line and when, like Zed
  - Hunk navigation (`Alt+[` / `Alt+]`)

### Developer Tools

- Built-in **terminal** (`Alt+T`) — persistent ConPTY/PTY shell session with ANSI emulation, colors, cursor addressing, resize, paste, keyboard/mouse forwarding, and support for interactive/full-screen TUI programs
- **LSP client** — diagnostics (rendered in the gutter), completion, go-to-definition; hints the install command when a language server is missing
- **Autocompletion** (`Ctrl+Space`, auto-trigger) — buffer words + LSP sources for Go, Python, TS/JS, Rust, C/C++, Lua, Ruby, PHP, Zig, JSON, YAML, CSS, HTML
- **Debugger** (`Ctrl+Alt+D`) — DAP-based debugging: gutter breakpoints (`F4`), run/continue (`F5`, which pauses a running debuggee), step over/in/out (`F6`/`F7`/`Shift+F7`), stop (`Shift+F5`); a threads/stack/variables panel that is fully mouse-driven (click a thread, frame or variable to select it, double-click a `▸` variable to expand, the wheel walks the focused column, `l` peeks at the process console with scroll-back), an opt-in expression eval (`Tab`), and one shared gutter marker column — left click toggles a breakpoint, middle click (the wheel) toggles a bookmark; the panel itself scrolls like a pager (`↑↓`, `PgUp`/`PgDn`, `Home`/`End`; the wheel works too, and over the console column it walks the backlog), `l` peeks at the process console with scroll-back, `+`/`-` resize the panel, and the selected entry is drawn across the full panel width so a long frame path or variable value is readable; Go/Delve out of the box, any other DAP adapter (debugpy, lldb-dap, node, ...) via `[debug]` config, and PHP out of the box as well — Xdebug has no DAP, so dmed speaks its DBGp protocol directly (spawns `php` on the active file, sets breakpoints, steps, and walks the variables tree)
- **Lua plugins** — keybindings, palette commands and events; hot-reload on edit, plus a built-in store (`Plugins: Install...`) with embedded and GitHub-hosted plugins
- **Localization** — English/Russian UI, switchable from the palette
- **Sessions** — auto-save/restore open files across restarts
- **Command palette** (`Ctrl+P` / `F2`) — fuzzy search all commands

## Install

Install the latest release with a single command:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

> Requires a Go toolchain **>= 1.26** for building from source; the one-liner
> installs a prebuilt binary (no Go needed).

**Alternative — build from source:**

```sh
git clone https://github.com/dedomorozoff/dmed.git
cd dmed
make build        # produces ./dmed.exe (or ./dmed on Linux/macOS)
```

**Or install with Go directly:**

```sh
go install github.com/dedomorozoff/dmed@latest
```

> The one-liner installs to `~/.local/bin` on Linux/macOS/BSD and to
> `%LOCALAPPDATA%\dmed` on native Windows. Add that directory to your `PATH`
> if `dmed` isn't found after installing.

## Usage

```sh
dmed path/to/file.txt          # open file (creates if missing)
dmed dir/                      # open folder — shows project tree
dmed a.txt b.txt               # multiple files → tabs
```

## Keybindings

### Navigation

| Keys | Action |
|------|--------|
| `Arrows` | Move cursor |
| `Home` / `End` | Line start / end |
| `PgUp` / `PgDn` | Page up / down |
| `Ctrl+↑/↓` | Scroll without moving cursor |
| `Ctrl+L` | Go to line (N, N:C, or `+N`/`-N` relative) |

### Editing

| Keys | Action |
|------|--------|
| `Shift+Arrows` | Select text |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste |
| `Ctrl+Z` / `Ctrl+R` | Undo / redo |
| `Ctrl+Y` / `Ctrl+D` | Delete / duplicate line |
| `Ctrl+/` | Toggle comment |
| `Alt+Z` | Toggle word wrap |
| `Alt+B` | Toggle inline git blame (author · when per line) |
| `Alt+↑` / `Alt+↓` | Move line up / down |
| `Alt+D` | Add multi-cursor at next word occurrence |
| `Alt+Click` | Add cursor at click position |
| `Esc` | Exit multi-cursor mode |
| `Ctrl+Space` | Autocomplete (word + LSP) |
| `Ctrl+U` | Uppercase selection / buffer |
| `Enter` / `Backspace` / `Delete` | Standard editing |

### Files & Tabs

| Keys | Action |
|------|--------|
| `Ctrl+S` | Save (untitled: Save As) |
| `Ctrl+T` | Open file by path |
| `Ctrl+O` / `F3` | Fuzzy file finder |
| `Ctrl+W` / `Ctrl+X` | Close tab (last quits) |
| `Alt+←` / `Alt+→` | Switch tabs |
| `Alt+1..9` | Jump to tab N |

### Splits & Panels

| Keys | Action |
|------|--------|
| `Ctrl+\` / `F6` | Split vertical |
| `Ctrl+Alt+H` / `F7` | Split horizontal |
| `Ctrl+Alt+P` / `F8` | Focus other pane |
| `Ctrl+Alt+W` | Close pane |
| `Ctrl+B` / `F9` | Toggle project tree |
| `Ctrl+G` | Git panel |
| `Alt+T` | Toggle terminal |

The status bar's bottom-left icons (`▤` tree, `⎇` git, `✦` AI, `◉` debug, `❯`
terminal) are clickable; hovering one shows a callout with its shortcut.

### Debug

| Keys | Action |
|------|--------|
| `Ctrl+Alt+D` | Toggle debug panel |
| `F4` | Toggle breakpoint at cursor line (or left-click the gutter) |
| Middle click (gutter) | Toggle bookmark |
| `F5` | Run / continue / pause a running debuggee |
| `Shift+F5` | Stop session |
| `F6` | Step over (while the debug panel is open) |
| `F7` | Step in (while the debug panel is open) |
| `Shift+F7` | Step out (while the debug panel is open) |
| `Tab` (in panel) | Cycle threads / stack / variables / eval input |
| Click / wheel (in panel) | Select a thread, frame or variable; `Backspace` leaves an expanded variable |

### AI

| Keys | Action |
|------|--------|
| `Alt+A` | Toggle AI chat panel |
| `Alt+I` | Inline rewrite (select text first) |
| `Alt+G` | Inline ghost suggestion (Tab accept, Esc dismiss) |
| `Alt+L` | Agent panel — background task queue, progress, cancel |
| `Ctrl+U` | Clear chat history (in chat panel) |

### Search

| Keys | Action |
|------|--------|
| `Ctrl+F` | Find in file |
| `Ctrl+H` | Find & replace |
| `F3` / `Shift+F3` | Next / previous match |

### Git

| Keys | Action |
|------|--------|
| `D` (in panel) | Side-by-side diff vs HEAD |
| `Alt+[` / `Alt+]` | Previous / next hunk |

### General

| Keys | Action |
|------|--------|
| `F12` / `Ctrl+Click` | Go to definition (LSP) |
| `Ctrl+P` / `F2` / double `Shift` | Command palette |
| `F1` / `Ctrl+E` | Help overlay |
| `Ctrl+Q` / `Ctrl+C` | Quit |

## Configuration

Create `.dmed.conf` in your home directory (global) or project root (overrides global).
Settings hot-reload on save.

```ini
[editor]
tab_width = 4
syntax_theme = monokai      # any chroma style name
line_numbers = true
word_wrap = false           # wrap long lines to pane width (Alt+Z toggles)
skipped_dirs = .git,node_modules,vendor

[ai]
provider = Pollinations (free, no key)   # default: no account needed; wizard presets: Ollama (local) | OpenAI | DeepSeek | Groq | LM Studio (local) | vLLM (local) | Unsloth (local) | Custom
                            # legacy values "ollama"/"openai" still work
model =                      # empty = first model reported by the server
ollama_url = https://text.pollinations.ai   # base URL, no /v1 or /openai suffix (it is appended automatically)
api_key =                    # for OpenAI-compatible providers that need one
api_path =                   # endpoint prefix; /v1 by default, /openai for Pollinations (set by the wizard)
models_path =                # model-list path; /v1/models by default, /models for Pollinations
context_max = 6000           # max runes sent as file context
temperature = 0              # generation temperature in tenths (7 => 0.7); 0 = provider default
num_ctx = 0                  # context window in tokens for Ollama (num_ctx); 0 = default
num_predict = 0              # max output tokens; 0 = provider default
tool_rounds = 0              # chat tool-calling loop cap; 0 = built-in (6)
allow_run = ask                # always | never | ask — ask (default) confirms each shell command the model proposes
restrict_to_root = true       # true (default) keeps READ/EDIT/REPLACE inside the project root
tools_enabled =               # optional whitelist of chat tools, e.g. read,search,list_dir,glob,run,replace,edit
tools_disabled =              # optional blacklist applied after the whitelist
mode = act                   # act | plan — plan exposes read-only tools only
web_search = false            # true enables WEB_SEARCH (DuckDuckGo): the only tool that leaves this machine
web_search_budget = 20        # web queries allowed per session
free_fallback = true          # true (default): with nothing configured and no local answer, use the keyless provider
system_prompt = You are a helpful coding assistant...

[agent]                       # background agent tasks (M4): defaults are fine for most users
system_prompt =               # instruction override for agents producing edits; empty = built-in
context_max = 262144          # total bytes of file context gathered for the task
subagent_prompt =             # instruction override for delegated sub-agents; empty = built-in
subagent_rounds = 0           # tool-loop cap for one delegated task; 0 = built-in (6)

[ui]
tree_width = 25
chat_width_pct = 40          # percentage of screen width
lang = en                    # UI language: en | ru

[plugins]                     # remote source for the plugin store
repo = dedomorozoff/dmed     # "owner/repo"
dir = plugins                # directory holding .lua plugins
branch = main

[debug]                       # DAP debugger (Go/Delve by default)
mode = debug                  # launch mode passed to the adapter (debug/test/exec for Delve)
program =                     # package dir / executable / module entry; empty = active file dir
args =                        # whitespace-separated arguments for the debuggee
stop_on_entry = false         # pause at program start
adapter_cmd = dlv             # adapter binary (dlv, debugpy-adapter, lldb-dap, node, ...); dlv_path is a legacy alias
adapter_mode = reverse        # reverse (adapter dials us back) | stdio (stdin/stdout DAP)
adapter_args =                # extra CLI args for the adapter process
launch_type = go              # DAP launch "type" field
launch_request = launch       # launch | attach
launch_json =                 # raw JSON merged into the launch body (adapter-specific keys win)
```

Example: debug a Python module through debugpy (a stdio DAP adapter):

```ini
[debug]
adapter_cmd = debugpy-adapter
adapter_mode = stdio
launch_type = python
program = /path/to/app
launch_json = {"justMyCode": false}
```

Node.js and TypeScript: with VS Code's "JavaScript Debugger" extension
(`ms-vscode.js-debug`) installed, `.js/.mjs/.cjs/.jsx/.ts/.mts/.tsx` files are
auto-detected and debugged through its standalone stdio adapter (`node
src/dap.js`) — `F4` breakpoints, `F5` runs the active file, F6/F7 step. Without
the extension, install the adapter once and/or point dmed at it:

```sh
npm i -g @vscode/js-debug-adapter     # adapter script: src/dap.js
```

```ini
[debug]
adapter_cmd = node
adapter_mode = stdio
adapter_args = C:/Users/me/node_modules/@vscode/js-debug-adapter/src/dap.js
launch_type = node
launch_request = launch
```

The adapter script is searched in `~/.vscode{-insiders,-server}/extensions`,
`~/.vscodium/extensions`, `~/.cursor/extensions` and `~/node_modules`, and
`DMED_JS_DEBUG=/path/to/dap.js` pins it explicitly. Attaching to an
already-running process works the same way: run `node --inspect-brk app.js`,
then set `launch_request = attach` with `launch_json =
{"port": 9229}`.

## Documentation

- [Plugins (Lua)](docs/PLUGINS.md) — write keybindings, palette commands, events
- [Autocompletion & LSP](docs/AUTOCOMPLETION.md) — completion popup + language servers
- [M4 Agents](docs/M4-AGENTS.md) — background agent tasks, queue, diff-review apply
- [ROADMAP.md](ROADMAP.md) — milestones and status
- [docs/IMPROVEMENT-PLAN.md](docs/IMPROVEMENT-PLAN.md) — what to improve next,
  ordered by cost/benefit
- [docs/AUDIT-2026-10-01.md](docs/AUDIT-2026-10-01.md) — the audit that led there

[docs/AI-TOOLS-PLAN.md](docs/AI-TOOLS-PLAN.md) records the design behind the AI
tool surface (why every tool is gated the way it is, and what was deliberately
not done).

## Known limitations

Worth knowing before you file a bug.

- **Keybindings must be checked in a real terminal.** Automated harnesses lose
  most Ctrl-chords and break F-key sequences, so the binding table is verified
  manually (Windows Terminal / mintty), not by CI.
- **AI tools are safe by default and can be relaxed.** `allow_run = ask` means
  the model has to ask before running a shell command, and
  `restrict_to_root = true` keeps `READ`/`EDIT`/`REPLACE` inside the project.
  Set both to `always`/`false` in `.dmed.conf` if you prefer the old behaviour.
- **Small local models pick tools worse as the list grows.** Trim it with
  `tools_enabled` (whitelist) or `tools_disabled` (blacklist) in `[ai]` — the
  names also accept the common aliases (`read_file`, `grep`, `run_command`,
  `write_file`, `edit_file`).
- **The default provider is a public service (Pollinations, no account
  needed).** Prompts and code leave the machine out of the box; prefer everything
  local by picking `Ollama (local)` in `Ctrl+P` → `AI: Preferences...`.
  With nothing configured and no answer from the configured provider, a session
  also falls back to the keyless provider and says so in the chat, with a `CLOUD`
  badge in the chat header. Set `free_fallback = false` to disable the fallback
  (the default provider itself is not affected). The free tier is rate-limited
  (roughly one request per 15s).
- **`web_search` is off by default.** It is the only tool that reaches outside
  the workspace; turn it on deliberately and watch the per-session budget.
- **Accepted agent changes are committed to git but cannot be undone from the
  editor yet.** Use `git revert` for now.
- **Large repositories get slow between AI tool rounds**, because the editor
  snapshots the whole project tree to detect touched files.
- **LSP support is minimal**: diagnostics and go-to-definition. No
  code actions or formatting yet.
- **`php-test/`** is a manual fixture for Xdebug debugging, not used by any
  automated test — see [php-test/README.md](php-test/README.md).

## Architecture

```
internal/buffer/     pure text buffer + undo/redo (no TUI deps)
internal/rope/       persistent line-rope backing the buffer (O(1) undo)
internal/editor/     Bubbletea model: keys, tabs, splits, rendering
internal/plugin/     Lua plugin framework (gopher-lua)
internal/bundled/    embedded official plugins for the plugin store
internal/i18n/       en/ru localization catalogs
internal/ai/         provider interface: Ollama + OpenAI-compatible
internal/config/     INI parser, hot-reload, defaults
internal/dap/        DAP client (debugging): framing, stdio/reverse launchers
internal/syntax/     Chroma-based highlighting
internal/vcs/        pure-Go git operations (go-git)
internal/lsp/        JSON-RPC 2.0 LSP client
internal/events/     internal event bus
internal/watcher/    fsnotify file watcher
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

See [ROADMAP.md](ROADMAP.md) for full details.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
