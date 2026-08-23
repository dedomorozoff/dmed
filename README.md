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

Requires Go ≥ 1.22.

```sh
make build        # produces ./dmed
make test         # unit tests
make vet          # static checks
```

## Usage

```sh
./dmed path/to/file.txt   # opens, or creates if missing
```

Keybindings:

| Keys | Action |
|------|--------|
| Arrows, Home/End, PgUp/PgDn | Cursor movement |
| Ctrl+S | Save |
| Ctrl+Z / Ctrl+Y | Undo / Redo |
| Ctrl+Q or Ctrl+C | Quit |

Consecutive typing collapses into a single undo step; any other operation
starts a new one.

## Layout

```
internal/buffer/   pure text buffer + undo engine (unit tested)
internal/editor/   Bubbletea model and rendering
main.go            CLI entry point
```

## License

TBD
