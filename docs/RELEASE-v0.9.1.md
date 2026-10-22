# dmed v0.9.1

**dmed** — a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes — but every diff is approved by a human. Nothing happens behind your back.

This release adds **code folding** — indentation-based blocks, gutter chevrons, a mouse path and a full keyboard path — and makes it fast enough to use on large files.

## ✨ New: code folding

### Fold blocks, not syntax trees
- Any non-blank line whose next non-blank line is indented deeper starts a block; the block ends where the indentation falls back to its own level. **Nested blocks** work, so an outer function folds to one line while the `if` inside it can still be folded on its own.
- Blank lines belong to the block around them, and a line without a body is never foldable — no phantom folds in a file you cannot collapse.
- Folding is **display-only state**: it never touches the buffer, so undo/redo, `Ctrl+S`, the dirty flag and agent diffs are exactly as they were. Folded sets are per tab.

### How you fold
- **Gutter chevrons**: `▾` on a block's start line, `▸` once it is folded. The gutter grows one column **only** when the buffer has something to fold, so plain files keep their geometry.
- **Mouse**: click the `▾`/`▸` cell to toggle a block, or click anywhere on the `… N lines hidden` summary row to open it. The fold column is part of the reported gutter width, so the click lands on the glyph under the pointer — and every other gutter click (breakpoints, bookmarks) and cursor placement stays aligned too.
- **Keyboard**: `Alt+C` folds the block on the cursor line, `Alt+O` opens it (vim `zc`/`zo`).
- **Palette**: `Edit: Toggle Fold` and `Edit: Toggle All Folds` — the second one flips the whole file at once.
- The help screen and status-bar hints (`nothing to fold here`, `folded every block`) tell you what happened.

### Folded files stay honest
- A folded block collapses to a `… N lines hidden` hint right under its start line, and the hint is a real screen row: it survives word wrap, and hit-testing, the wheel and cursor clamping all count it like any other row.
- The cursor can never sit on a hidden line: arrows and `PgUp`/`PgDn` skip over a folded block to its nearest visible edge, and a cursor that ends up inside one anyway (undo, a jump, an external edit, an AI-applied change) forces the block open in `clampScroll` instead of silently losing the caret.
- Drag-selecting across a folded block reveals it, and starting a drag on the summary row expands the block and continues the selection from its start line.
- Folded markers heal themselves: edit a file so a block stops being a block and the stale marker is dropped on the next render, so the folded count never lies.

## ⚡ Performance

- The display-row list, the fold scan and the gutter chevrons are **linear in the size of the buffer**. `dispRows` walks the folded blocks with a moving pointer instead of rescanning them per row, `foldAtStart` is a binary search over the sorted ranges (the gutter asks for it on every visible row) and the block scan itself is a single stack pass.
- The document text is now cached in the buffer and invalidated by the tree root, so the view-layer caches (syntax, wrap, folds) stop rebuilding the whole file string several times per keystroke.
- Net effect on a 20k-line file with every block folded: **11 s → 17 ms per frame**, allocations **13.6 GB → 10 MB**. Rendering a folded file is no longer distinguishable from rendering an unfolded one.

## 🛠 Fixes

- Gutter rendering now matches `gutterWidthForTab` exactly: with the fold column present the painted gutter used to be one cell wider than reported, which pushed every content click one character to the right of the glyph under the pointer and made the chevron cell itself unclickable (a click on it toggled a breakpoint instead).
- The fold column only folds: clicking its blank cell on a line that starts no block no longer falls through to the breakpoint toggle, which lives in the marker column to its left.
- Clicking the gutter of a `… N lines hidden` summary row expands the block, the same as clicking the summary text.

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.0...v0.9.1
