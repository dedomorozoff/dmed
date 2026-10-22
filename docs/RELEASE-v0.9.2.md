# dmed v0.9.2

**dmed** - a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes - but every diff is approved by a human. Nothing happens behind your back.

This release is about speed: nothing expensive happens between a keystroke and the next frame anymore - the whole heavy pipeline (syntax highlighting, git diff, token cache) moved off the input path and into the background. Plus JetBrains-style Search Everywhere.

## ⚡ Performance

### Syntax highlighting left the input path
- Highlighting **never runs on the UI thread** anymore. The matching between keystroke and frame used to re-tokenize the whole file synchronously per change; now a background pass waits for the typing to settle (150 ms), runs chroma in a goroutine and delivers the result over a channel. A pass over text the buffer already moved away from is dropped, and at most one pass per tab can be in flight.
- Highlighting is **streamed**: chroma hands out finished lines through a callback while the tokenizer is still running, and the editor publishes them in chunks - the whole token list is never materialized in memory.
- The highlight cache stores **one token per character instead of a full style**. A lipgloss.Style costs ~800 bytes per character (a 20k-line file held a ~440 MB cache); a token is 8 bytes - **6.3 MB** on the same file. Styles are resolved once per equal-style run when a line is painted, and lines are painted in runs, not character by character.
- Language detection went through a **static extension/name table** - asking chroma for a language used to initialize its global lexer registry, which could stall seconds on the UI thread; the registry now warms up in the background behind the first frame.

### The rest of the per-keystroke work too
- **Git diff in the background**: the `+`/`~` gutter markers were computed by a synchronous diff every keystroke (42 ms on a large buffer); they follow the same settle-and-run-in-background pattern as highlighting now.
- **Per-frame caches**: word-wrap caret rows and other per-frame lookups are cached on the tab instead of recomputed per frame; indentation width reads the line in place instead of copying it.

On a 60k-line file a keystroke took seconds before this chain and takes 16-43 ms now; painting a fully folded 20k-line frame costs ~1.6 ms.

## ✨ New: Search Everywhere

One list that finds **commands, project files and settings** at once, like JetBrains' Search Everywhere:

- `Alt+P` opens it - in **any** terminal: `Alt+letter` arrives as `ESC+letter` even on legacy input that cannot tell `Ctrl+Shift+*` from `Ctrl+*`. `Ctrl+Shift+P` does the same where the terminal reports both modifiers, and a **double `Shift`** works wherever the terminal speaks the Kitty keyboard protocol.
- Every result says what it is: `internal/editor/fold.go` comes with a `[file]` badge, `editor.tab_width` with `[setting]` and its current value. **Picking a file opens it**, picking a setting **opens the configuration file**. The API key is masked (`***`) in the settings list.
- `Ctrl+P` / `F2` keep searching commands only - the way JetBrains keeps Find Action apart from Search Everywhere - and a `Search Everywhere` palette command reaches the same list from any terminal.

## 🛠 Fixes

- **Ctrl+C never quits the editor**: with a selection it copies (as before); without one it explains that `Ctrl+Q` is the way out. An editor that dies on the key a terminal uses to interrupt a program is a trap - Ctrl+C no longer reaches it, SIGINT is filtered out, SIGTERM still stops it, and a dirty buffer still asks before quitting on `Ctrl+Q`.

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.1...v0.9.2
