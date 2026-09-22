# dmed v0.8.2

**dmed** — a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes — but every diff is approved by a human. Nothing happens behind your back.

This release brings **PHP/Xdebug debugging** (dmed speaks DBGp directly — Xdebug has no DAP), **fetch/push and inline git blame** in the editor, per-pane status bars in splits, an Unsloth provider preset, and fixes for Cyrillic/Ctrl-Alt keyboard layouts.

## 📦 Install

One-line install on Linux, macOS, and BSD (needs only `curl`):

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

Scriptable installer / package builds for Linux `.deb` (Ubuntu), `.rpm` (Fedora), `.pkg.tar.zst` (Arch), Termux `.deb`, and Windows `.zip` are attached to this release.

## ✨ New & improved

### Git
- **Fetch / push from the Git panel** (`Ctrl+G`, status mode: **`f`** / **`p`**) — background sync to/from `origin` over go-git, with an inline "no remote configured" note and status-line result/error messages.
- **Inline git blame** (`Alt+B`, palette "Git: Toggle Blame") — right-aligned `author · when` annotations on each unchanged line, like Zed. Dim, non-intrusive, and skipped when there is no room on the row or word wrap is on.

### Debugging — PHP out of the box
- **PHP/Xdebug via DBGp** — a native DBGp client (`internal/dbgp`): breakpoints, run/step, stack/context/property variables and eval, all exposed through the same panel as DAP. Xdebug needs no DAP adapter.
- **Connect-mode DAP** — a third `adapter_mode: connect` wires adapters that *listen* (Xdebug's port 9003) instead of being spawned.
- **Language auto-detection** — Go → Delve/reverse, PHP → Xdebug/connect applied automatically at `F5`; explicit `[debug]` settings always win (`auto_detect` toggles this).

### Editor
- **Per-pane status bars in splits** — each pane in a split owns the bottom row of its cell: its own file name, branch, line-ending/encoding, and `Ln/Col`. Vertical splits get one status per column; horizontal splits dock one above the separator and one at the screen bottom. The app-wide row keeps icons, messages, and hints.

### AI
- **Unsloth (local)** provider preset — OpenAI-compatible, `http://localhost:8000` (+ a built-in installer hint), same zero-config flow as Ollama/LM Studio/vLLM.

## 🐛 Fixes
- **Ctrl/Alt chords on non-US keyboard layouts** — the Windows Console API reports `Ctrl+<physical key>` with Cyrillic `Code`/`Text`, and `Ctrl+G` used to type a letter instead of opening the Git panel. The key name is now rebuilt from modifiers when Ctrl or Alt is held; Shift-only input keeps its form so `g`/`G` top/bottom moves survive.
- **DBGp continued/stopped race** — `EventContinued` is now emitted *before* the next continuation command, so the stopped→running transition no longer drops (the panel could get stuck in `stopped` while the debuggee ran).
- **DBGp nil-connection guard** — `Close`/`sendText` are safe before the `<init>` handshake accepts a session.

## ⚠️ Note
`dist/` is a gitignored build output directory — the release binaries and packages are attached to the GitHub Release itself, not stored in the repo.