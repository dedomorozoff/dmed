# dmed v0.8.3

**dmed** — a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes — but every diff is approved by a human. Nothing happens behind your back.

This release delivers a real interactive terminal, persistent status-bar layout, and improved DAP adapter detection.

## ✨ New & improved

### Terminal
- **Real PTY terminal** — Windows ConPTY and Unix PTY backends with persistent sessions.
- ANSI screen emulation with colors, cursor addressing, alternate screen support, resize, paste, keyboard and mouse forwarding.
- Interactive and full-screen TUI programs work inside the bottom terminal panel.

### Editor UI
- **Status bar stays visible** above every bottom panel, including Git and terminal overlays.
- Docked panels have consistent separators between sections.
- Panel mouse hit-testing and folder-picker interactions are synchronized with rendering geometry.

### Debugging
- Improved DAP adapter detection and language-specific defaults, including Node/stdio handling and connect-mode adapters.

## 🐛 Fixes
- Terminal process exit, stale-session events, resize, and shutdown lifecycle are handled safely.
- Git and terminal panels no longer overwrite or hide the application status bar.
- Cross-platform PTY backends compile and run on Unix-like systems and Windows.

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

`dist/` is a gitignored build output directory — release binaries and packages are attached to the GitHub Release itself.
