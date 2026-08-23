# AGENTS.md

Guidance for AI coding agents working on this repository.

## Project

dmed is a terminal code editor (Bubbletea/TUI in Go) that will integrate AI
agents as first-class participants. Current milestone: **M0** — single-file
editor skeleton. The full plan lives in [ROADMAP.md](ROADMAP.md); update its
checkboxes when you complete work.

The architectural rule of the project: agent-proposed changes must always pass
through diff review before touching buffers. Keep this in mind for all future
AI-related code.

## Toolchain gotchas (this machine)

- Use Go from `/usr/lib/go/bin/go` (1.22). Do NOT use plain `go` from PATH —
  it is an ancient 1.15 and breaks builds.
- The environment exports a stale `GOROOT` pointing at Go 1.15. Unset it:
  `unset GOROOT` (the Makefile already does `export GOROOT :=`).
- Network fetches need explicit `GOPROXY=https://proxy.golang.org,direct`
  and `GOSUMDB=sum.golang.org` (system defaults are wiped). The Makefile
  sets both.
- Dependencies are pinned: bubbletea v1.2.4, lipgloss v1.0.0, because newer
  releases require Go ≥ 1.24 and we build with 1.22. Do not bump them unless
  you also upgrade the toolchain.

## Commands

```sh
make build          # ./dmed binary
make test           # go test ./...
make vet            # go vet ./...
make run FILE=path  # build + run editor on a file
```

Run `make build && make vet && make test` before considering any change done.

## Architecture

- `internal/buffer/buffer.go` — pure text buffer: rune lines, cursor,
  sticky goal column, undo/redo stacks with typing-run grouping. It must stay
  free of TUI imports; behavior changes belong in `buffer_test.go`.
- `internal/editor/editor.go` — Bubbletea model: key handling, scrolling,
  file I/O. `internal/editor/view.go` — rendering (gutter, cursor cell,
  status bar, tab expansion).
- `main.go` — CLI entry only.

Conventions:

- Buffer state is the single source of truth; the view renders from it and
  holds no editable state of its own.
- No global mutable state; models are values, mutations happen through
  pointer receivers on small, named methods.
- Undo grouping: consecutive inserts at adjacent positions form one undo step;
  any other operation breaks the group (see `beginChange`/`breakGroup`).
- Files are stored with a trailing newline; dirty check compares against the
  normalized saved snapshot (`MarkSaved`/`Dirty`).

## Manual verification

Unit tests cover the buffer core. For TUI behavior there is no automated UI
test yet — smoke-test in a pty, e.g.:

```sh
printf 'text\023\021' | script -qec './dmed /tmp/f.txt' /dev/null
# \023 = Ctrl+S save, \021 = Ctrl+Q quit; then inspect /tmp/f.txt
```

Note: headless ptys may report a zero window size; the model guards against
non-positive `WindowSizeMsg` values, keep that guard intact.
