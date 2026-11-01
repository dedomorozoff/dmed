# dmed v0.9.7

Feature release over v0.9.6: bracket matching and JetBrains-style reformat.

## Added

- **Bracket/quote delimiter matching.** The delimiter under or next to the cursor is highlighted together with its pair — brackets, braces, parentheses and quotes — so the matching side is visible without leaving the key.
- **JetBrains-style reformat on Ctrl+Alt+L.** Reformatting the document through LSP (gofmt, prettier, …) now has the IntelliJ chord. The previous key, Ctrl+Alt+F, is kept as a fallback for terminals that mangle Ctrl+Alt+L. Plain Alt+L (agent panel) is unaffected.

## Changed

- **Version visible in-app.** The version is exposed in the palette and on the command line, so a running instance reports exactly what it was built from.
- **Search everywhere polish.** Double-shift detection is more robust across layouts; opening a directory no longer leaves an untitled tab behind.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.6...v0.9.7
