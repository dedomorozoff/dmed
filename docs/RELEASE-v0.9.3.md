# dmed v0.9.3

**dmed** - a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes - but every diff is approved by a human. Nothing happens behind your back.

This release is about seeing what you open: a SQLite database or an image is no longer a tab full of garbage bytes - each opens as its own readable view - and the project tree keeps file names legible no matter how deep the hierarchy is.

## ✨ New: SQLite browser

Opening a `.sqlite`/`.db` file used to mean a text tab of mojibake. Now a file that carries a SQLite magic header (a text-only `.db` stays a text tab) opens as a full-screen database browser:

- A **table/view strip** (`Tab` cycles) over a **paged row grid** (50 rows per page).
- **Substring filter** over every column (`/`), **sorting** (`o` cycles the cursor column asc/desc/off), the **CREATE statement** of the cursor table (`s`).
- **Immediate editing with the rowid as the row address**: `Enter` edits the cell (an empty value means NULL), `Ctrl+N` inserts an all-defaults row, `Ctrl+D` deletes the cursor row.
- Views and `WITHOUT ROWID` tables are shown **read-only** — without a rowid there is no stable address to write to.
- The data layer (`internal/sqlitedb`) runs on `modernc.org/sqlite`: **pure Go, no CGO**, the 8-platform cross build stays green. User values travel as bound parameters and identifiers are double-quoted with `""` escaping — user text is never concatenated into SQL.

## ✨ New: ASCII picture preview

A `.png`/`.jpg`/`.jpeg`/`.gif`/`.bmp` file (the decoder is the final judge, not just the extension) opens as a picture tab instead of a binary-text mess:

- The pane renders **half-blocks** (`▀`, two pixels per cell, box-averaged sampling) through the terminal's colour profile via `colorprofile` — degrading cleanly to ANSI-256, plain ANSI, and finally an **ASCII ramp** on a colourless terminal.
- **GIF frames animate** through a single tick chain, with the frame delay taken from the file's own numbers and the frame counter shown in the caption.
- Picture tabs are **read-only by design**: `Ctrl+S`/`Ctrl+C`/`Ctrl+V` answer with an explanation instead of touching anything, the `Ctrl+E` chat stays reachable via the palette. Scrolling a tall picture is the pane offset — the mouse wheel scrolls it too.
- The file watcher **re-decodes changed pictures** and falls back to a text tab with the reason if the file stopped being an image.

## 🛠 Fixes: the project tree keeps file names readable

One cause behind three symptoms — tree rows were composed without a true width budget:

- **Rows no longer shift the editor gutter.** A row whose file name overflowed the panel was one cell wider than the panel, and since the sidebar renders inside the editor rows, every overflowing line pushed its own line numbers one character right - the gutter looked broken precisely on those lines.
- **Deep nesting no longer eats the name.** The ancestor gauges (`│   │   ├── `) cost 4 cells per level and used to consume everything; now they collapse to an ellipsis before the final connector (siblings stay aligned), so a file three or four levels deep keeps its **full name**.
- **An overlong name is cut from the head, not the tail** - `…section_kept_tail.txt` - so the ending with the extension stays visible, and an overflowed row used to render blank instead of a readable prefix.
- **No tree row is ever wider than the panel**, despite the sidebar being only as wide as its content.

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.2...v0.9.3
