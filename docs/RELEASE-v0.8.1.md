# dmed v0.8.1

**dmed** — a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes — but every diff is approved by a human. Nothing happens behind your back.

This release brings the **DAP debugger (Milestone M7)** as a first-class feature, a built-in TUI folder picker, code bookmarks in the gutter, and a pass of debugger/palette reliability fixes.

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

### DAP debugging (M7)
- **Debugger client** (`Ctrl+Alt+D`, `F4`–`F7`) — breakpoints (`F4` / `F5` run/continue, `Shift+F5` stop, `F6` step over, `F7` step in, `Shift+F7` step out); threads / stack / variables tree with mouse-driven expansion and wheel scrolling; process console with scroll-back and eval line.
- **Generic DAP adapters** — `adapter_cmd` / `adapter_mode` (reverse | stdio) / `adapter_args` / `launch_type` / `launch_request` / `launch_json` let you wire any adapter (debugpy, lldb-dap, node, …). Go/Delve is the default.
- **Line-edit palette commands + DAP settings wizard** — the wizard prompts for adapter/provider/launch config with sensible Go defaults.
- **Execution-point highlight** — the stopped line is followed and highlighted (`▶`), auto-navigating to the file and line.

### Editor
- **Built-in TUI folder picker** replaces the system-dialog Open Folder command; focus moves to the project tree after opening a folder.
- **Code bookmarks** — `Alt+M` toggle, `Alt+N` navigate; middle-click (wheel) toggles a bookmark in the gutter, left-click toggles a breakpoint.
- **Clickable status-bar panel icons** with hover callouts (`▤` tree, `⎇` git, `✦` AI, `◉` debug, `❯` terminal).
- **LSP reliability** — cleaner completion popup and a working Delve debug integration.
- **Normalized paste** — CRLF pasted from the Windows clipboard is normalized to LF, so pasted text no longer garbles the buffer.

### UI / help
- **Scrollable F1 help** (`j`/`k`, PgUp/PgDn, Home/End, mouse wheel); fixed `PgDn` app-wide (bubbletea v2 sends `pgdown`).

## 🐛 Fixes
- **Ctrl+P palette no longer lists duplicate commands** — the duplicated `duplicate_line … goto_definition` block has been removed (45 unique commands, no repeats).
- **Debugger launch ordering** — `launch → setBreakpoints → configurationDone` (Delve rejects breakpoints before launch), so `F5` and breakpoints work reliably; `F5` now pauses a running debuggee; step keys and mouse-driven panel fixed.
- DAP: "no debug session started" guard fixed so early breaks no longer abort the launch.

## ⚠️ Note
`dist/` is a gitignored build output directory — the release binaries and packages are attached to the GitHub Release itself, not stored in the repo.
