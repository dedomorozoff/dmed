# dmed

A terminal-native code editor with AI agents as first-class participants.

The idea: an editor like Zed, but living entirely in your shell. Core concepts:

- **AI agents** that read, propose and apply changes to your code
- **Change tracking** — every file modification is watched and reviewable
- One strict rule: *agents propose diffs, humans approve them*. Nothing is
  ever modified behind your back.

## Status

**M0 — working skeleton.** Single-file editing with full cursor/edit/undo
semantics. No AI yet by design: the editor core comes first.

Roadmap (see [ROADMAP.md](ROADMAP.md)):

| Milestone | Scope |
|-----------|-------|
| M1 | Multi-file: tabs/splits, fuzzy finder, syntax highlighting |
| M2 | Change tracking: fsnotify watchers, git gutter, event bus |
| M3 | AI v1: chat panel, inline edit → diff preview → accept/reject |
| M4 | Background agents: multi-file tasks, task queue, atomic patches |
| M5 | Polish: LSP client, plugins, sessions |

## Build

Requires Go ≥ 1.24.

```sh
make build        # produces ./dmed.exe
make test         # unit tests
make vet          # static checks
```

## Usage

```sh
./dmed.exe path/to/file.txt          # opens, or creates if missing
./dmed.exe a.txt b.txt               # multiple files → tabs
```

Keybindings:

| Keys | Action |
|------|--------|
| Arrows, Home/End, PgUp/PgDn | Cursor movement |
| Ctrl+S | Save active tab |
| Ctrl+Z / Ctrl+Y (or Ctrl+R) | Undo / Redo |
| Ctrl+T | Open file by path (prompt) |
| Ctrl+O or F3 | Fuzzy file finder |
| Ctrl+B or F9 | Project tree: show/focus → hide (Esc = back to editor) |
| Ctrl+W or Ctrl+X | Close tab (last one quits) |
| Alt+← / Alt+→, Alt+1..9 | Switch tabs |
| F1 or Ctrl+E | Help overlay |
| Ctrl+Q or Ctrl+C | Quit |

Consecutive typing collapses into a single undo step; any other operation
starts a new one.

## Layout

```
internal/buffer/    pure text buffer + undo engine (unit tested)
internal/editor/   Bubbletea model: tabs, finder, rendering
main.go             CLI entry point
```

## License

TBD
