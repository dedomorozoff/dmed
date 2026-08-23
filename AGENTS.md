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

- Use plain `go` from PATH (1.25.0, installed at `D:\go\bin`). No special
  path needed; the old `/usr/lib/go/bin/go` (1.22) is gone.
- A stale `GOROOT` used to be exported pointing at Go 1.15; it is clean now,
  but the Makefile still does `export GOROOT :=` as insurance — leave it.
- Network fetches need explicit `GOPROXY=https://proxy.golang.org,direct`
  and `GOSUMDB=sum.golang.org` (system defaults are wiped). The Makefile
  sets both.
- Dependencies are currently pinned: bubbletea v1.2.4, lipgloss v1.0.0.
  They were pinned for the old Go 1.22 toolchain; with Go ≥ 1.24 bumping
  is possible again, but only bump deliberately and re-run tests.

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
