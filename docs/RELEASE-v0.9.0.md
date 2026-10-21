# dmed v0.9.0

**dmed** — a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes — but every diff is approved by a human. Nothing happens behind your back.

This release is about editor quality-of-life: project-wide search, smart typing behavior, and language-server refactoring — plus a round of safety hardening.

## ✨ New & improved

### Find in Project
- **Project-wide text search** (`Ctrl+Shift+F`, or the command palette) with an async, streaming result panel: hits appear as they are found, `↑↓` moves the cursor, `Enter` opens the file at the match with the panel kept open.
- It walks the project through the same skip rules as the AI tools — `.git`, vendored trees, binaries and generated files can never pollute your results or the model's view.
- **Smart case** (all-lowercase queries match case-insensitively) and **regex mode** (`Tab` in the search prompt): `\d`, `.*`, whatever you need.
- **Regex replace-all** (`Ctrl+A` in the replace prompt) expands `$1`/`$name` capture groups; the buffer gained `ReplaceAllRE` — one undo step like everything else.

### Smart typing
- **Block indent**: `Tab` with a selection indents all selected lines; `Shift+Tab` unindents — content is never eaten.
- **Enter continues the line's indentation**, and goes one tab deeper after an open bracket before the caret.
- **Auto-close** for `(`, `[`, `{`, `"` with type-over of the closing character and a pair-consuming Backspace.
- Typing over a selection no longer swallows the first keystroke.

### Language server: rename & quick fixes
- **Rename symbol** (`Ctrl+Alt+R`, or the palette): an input pre-filled with the word under the cursor, and the server's `WorkspaceEdit` applied across files — open tabs land in the buffer (the user reviews and saves), closed files are updated on disk.
- **Code actions** (`Ctrl+Alt+Q`): the server's quick fixes and refactors in a bottom panel.
- Both: legacy terminals that lose `Ctrl+Alt+*` chords have the palette commands. LSP character offsets are converted correctly (UTF-16 units → rune columns, astral planes included).

### Bookmarks
- **Bookmarks survive a restart**: marked lines are stored in the project's session file and restored on the next run — gutter marks and jumps included.

### Command palette
- **Fuzzy filtering**: the palette now matches subsequence-style with the file finder's scorer, not substring-only.

## 🛡 Safety & housekeeping
- The keyless public AI fallback now respects **every** user decision: a custom `SystemPrompt`, a hand-tuned model list or endpoint are counted as configuration — nothing of yours leaks to a public service.
- **Lua plugins run under a deadline** (3 s per call, 3 s to load): a `while true do end` handler fails with a status-bar message instead of freezing the editor. Plugin errors are no longer silently dropped.
- Git operations survive alternative path spellings: a workspace opened via an 8.3 short name (`C:\Users\ALEX~1\...`) or with different letter casing no longer breaks staging, blame, or diff on Windows.
- Lua plugins run under a wall-clock budget (3 s per call, 3 s to load): a `while true do end` handler fails with a status-bar message instead of freezing the editor. Plugin errors are no longer silently dropped.
- Git operations survive alternative path spellings: a workspace opened via an 8.3 short name (`C:\Users\ALEX~1\...`) or with different letter casing no longer breaks staging, blame, or diff on Windows.
- The plugin store's network fetches went behind a test seam; generation-stamping for async sessions got one shared helper.
- `.dmed_session.json` is hidden from project walks (search results and agent tools).

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.8.4...v0.9.0
