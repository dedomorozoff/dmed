# dmed (Developer-Machine Editor)

[![Go Version](https://img.shields.io/github/go-mod/go-version/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
[![License](https://img.shields.io/github/license/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed/blob/main/LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed)
[![Issues](https://img.shields.io/github/issues/dedomorozoff/dmed)](https://github.com/dedomorozoff/dmed/issues)

**dmed** is a terminal-native, keyboard-driven code editor designed for the era of AI-assisted development. Built entirely in Go, it treats AI agents not as plugins, but as **first-class participants** in your workflow. 

AI agents can read, propose, and apply changes directly to your codebase — but **humans approve every single diff**. Nothing ever happens behind your back.

## Key Highlights

*   **Keyboard-First & Terminal-Native:** Fast, lightweight, and works seamlessly over SSH.
*   **Human-in-the-Loop AI:** High-level autonomy for agents (Ollama, OpenAI, DeepSeek, etc.) with 100% human control via explicit diff reviews.
*   **Zero-Dependency Git:** Native side-by-side diffs, gutter indicators, and staging directly from the editor without needing an external git binary.
*   **All-in-One Dev Environment:** Built-in persistent terminal, LSP diagnostics, and smart fuzzy searching out of the box.


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
- Configurable via `.dmed.conf` (INI format, hot-reload on save)

### AI Integration

- **Chat panel** (`Alt+A`) — streaming conversation with your code
- **Inline rewrite** (`Alt+I`) — select text, describe change, review diff, accept/reject
- Supports multiple providers:
  - **Ollama** (local, free)
  - **OpenAI-compatible** (OpenAI, DeepSeek, Groq, Together, vLLM, LM Studio)

### Change Tracking

- **fsnotify** file watcher — detects external changes, offers reload/auto-merge
- **Git integration** (pure Go, no git binary required):
  - Gutter indicators: added `+`, modified `~`, deleted `_`
  - Side-by-side diff view vs HEAD
  - Stage/unstage/commit from the Git panel (`Ctrl+G`)
  - Hunk navigation (`Alt+[` / `Alt+]`)

### Developer Tools

- Built-in **terminal** (`Alt+T`) — persistent shell session at the bottom
- **LSP client** — diagnostics, go-to-definition, go-to-references
- **Sessions** — auto-save/restore open files across restarts
- **Command palette** (`Ctrl+P` / `F2`) — fuzzy search all commands

## Install

```sh
# Requires Go >= 1.24
git clone https://github.com/user/dmed.git
cd dmed
make build        # produces ./dmed.exe (or ./dmed on Linux/macOS)
```

Or install directly:

```sh
go install github.com/user/dmed@latest
```

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

### Editing

| Keys | Action |
|------|--------|
| `Shift+Arrows` | Select text |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste |
| `Ctrl+Z` / `Ctrl+R` | Undo / redo |
| `Ctrl+Y` | Delete line |
| `Alt+↑` / `Alt+↓` | Move line up / down |
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

### AI

| Keys | Action |
|------|--------|
| `Alt+A` | Toggle AI chat panel |
| `Alt+I` | Inline rewrite (select text first) |
| `Ctrl+U` | Clear chat history |

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
| `Ctrl+P` / `F2` | Command palette |
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
skipped_dirs = .git,node_modules,vendor

[ai]
provider = ollama            # ollama | openai
model =                      # e.g. gpt-4, qwen2.5-coder:7b
ollama_url = http://localhost:11434
api_key =                    # for OpenAI-compatible providers
context_max = 6000           # max lines sent as file context
system_prompt = You are a helpful coding assistant...

[ui]
tree_width = 25
chat_width_pct = 40          # percentage of screen width
```

## Architecture

```
internal/buffer/     pure text buffer + undo/redo (no TUI deps)
internal/editor/     Bubbletea model: keys, tabs, splits, rendering
internal/ai/         provider interface: Ollama + OpenAI-compatible
internal/config/     INI parser, hot-reload, defaults
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
| M4 — Background agents (multi-file tasks, task queue) | 🔜 Next |
| M5 — LSP, terminal, plugins, sessions, config | ✅ Done |

See [ROADMAP.md](ROADMAP.md) for full details.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
