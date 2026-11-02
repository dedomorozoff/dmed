# dmed v0.10.0

Feature release over v0.9.7: the language server reaches the end of the
roadmap, highlighting became incremental, and a long list of editing commands
landed — merge conflicts, project-wide replace, macros, column selection,
jump history and a bracket-matching chord.

## Added

### Language server: M10 complete
- **Diagnostics navigation** — `Alt+E` jumps to the next diagnostic, `Alt+Shift+E` to the previous one, per tab, wrapping, with the index, severity and message in the status line. Each jump lands in the jump list, so `Ctrl+Alt+←` walks back.
- **Hover documentation** (`Ctrl+K`) — the server's markdown in a bottom overlay: code fences unwrapped, long lines clipped, Esc closes.
- **Find Usages** (`Shift+F12`) — the server's references as a bottom list; Enter opens the file at the usage.
- **Document Outline** (`Ctrl+Alt+O`) — document symbols with kind glyph, line and detail; both reply shapes (hierarchical and flat) parse.
- **Language table in the config** — `[lsp] lang.<id> = command [args...]` replaces a built-in entry, an empty value removes a language, a bare extension id adds one.
- **Auto-restart of a crashed server** and **`workspace/didChangeWatchedFiles`** — an external change (an agent commit, a tool run) is forwarded to the server, so diagnostics follow the working tree.

### Highlighting: M11
- **Incremental highlighting** — the per-line token cache splices the unchanged prefix and stops tokenizing once three fresh lines match the cache (the text hash included), so a one-line edit in a 10,000-line Go file went from ~500 ms to ~49 ms. Correctness never depends on the heuristic: a wrong re-sync decision costs time, not tokens.
- **Embedded languages** pinned by test — fenced Go in markdown, JavaScript template interpolation.

### Editing
- **Replace in Project** (`Ctrl+Shift+R`) — the human's batch edit: per-file preview, side-by-side diff review and an atomic apply through the same Applier the agents use. Files an open tab still holds unsaved and non-UTF-8 files are listed as excluded instead of written.
- **Merge conflicts** — markers are scanned strictly (column 0, exactly seven characters, the diff3 base dropped) and resolved as ours/theirs/both straight in the buffer. `Alt+[` / `Alt+]` navigate conflicts while the file has any, then fall back to Git hunks.
- **Selection transformations** — case fold, block sort, trailing-whitespace trim, indent alignment and list numbering, each one undo step, each a no-op when it changes nothing.
- **Bracket matching** — `Ctrl+]` jumps to the matching bracket or quote, `Ctrl+Shift+]` selects the text inside the pair.
- **Column (rectangular) selection** — `Ctrl+Alt+Shift+arrow` chords, Shift+drag with the mouse, and a palette toggle for legacy terminals.
- **Macros** — `Alt+R` records a run of text-pane keys, `Alt+Shift+R` plays it back.
- **Jump list** — `Ctrl+Alt+←` / `Ctrl+Alt+→` walk the caret back and forward across definitions, project-search hits, go-to-line, debugger stops and the last change.
- **MRU tab cycle** (`Ctrl+Tab`) and **go to last change** (`Ctrl+Shift+Backspace`).

### Interface
- **Project tree separator** — a vertical rule draws the panel's edge against the editor and brightens while the tree owns the keyboard, like the chat rail.

## Changed

- **One key translator.** Every pane handler now matches the single normalized key name `keyOf` produces; the per-pane translations and the duplicate ЙЦУКЕН table are gone. Letter commands in a Cyrillic layout and Enter/Esc/Tab arriving as control bytes no longer go dead.
- **Coverage gate in CI** — `make cover-check` records the total and fails below the baseline, so a new untested file cannot silently pass.

## Fixed

- **Folding answers on the block the cursor belongs to** — the block starting on its line, the innermost one around it, or the Allman signature whose lone brace sits below. Folding from inside moves the caret to the block's start line instead of letting the clamp reopen it.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.7...v0.10.0
