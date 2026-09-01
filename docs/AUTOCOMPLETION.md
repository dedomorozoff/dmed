# Autocompletion & LSP

dmed shows a completion popup while you type. It combines two sources:

1. **Buffer words** — identifiers found in the current file. Always available.
2. **LSP** — from a language server for the file's language, when one is
   installed (async; the UI never blocks on the server).

## Usage

- Type an identifier — the popup appears with matching candidates.
- `Ctrl+Space` — force the popup, listing all identifiers (or everything from
  the LSP server).
- `↑/↓` or `Ctrl+P/Ctrl+N` — navigate; `PgUp/PgDn` page.
- `Tab` / `Enter` — accept the highlighted item (replaces the partial word).
- `Esc` — dismiss.

LSP items are listed first; buffer-word matches follow. Everything is
deduplicated.

## Supported languages & servers

The editor starts a language server for the current file's extension if the
server binary is on `PATH`. If it is missing, completion silently falls back to
buffer words. The mapping lives in `lspServerFor` (`internal/editor/lsp.go`):

| Extension | Server binary |
| --- | --- |
| `.go` | `gopls` |
| `.py` | `pyright-langserver --stdio` |
| `.ts/.mts/.cts/.tsx/.js/.mjs/.jsx` | `typescript-language-server --stdio` |
| `.rs` | `rust-analyzer` |
| `.c/.h` | `clangd` |
| `.cpp/.cc/.cxx/.hpp/.hh/.hxx` | `clangd` |
| `.lua` | `lua-language-server` |
| `.rb` | `solargraph stdio` |
| `.php` | `intelephense --stdio` |
| `.zig` | `zls` |
| `.json` | `vscode-json-languageserver --stdio` |
| `.yaml/.yml` | `yaml-language-server --stdio` |
| `.css/.scss/.less` | `vscode-css-languageserver --stdio` |
| `.html` | `vscode-html-languageserver --stdio` |

Example:

```sh
go install golang.org/x/tools/gopls@latest   # for Go
```

Then open a `.go` file and type `fmt.` — gopls supplies package members.

## Adding a language

1. Add an entry to `lspServerFor` (command, args, language id).
2. Optional: verify the server talks JSON-RPC over stdin/stdout (`--stdio`).

## Notes / roadmap

- Completion requests carry a 6-second timeout so a hung server can't stall
  anything.
- Diagnostics are collected by the LSP client but not yet rendered in the
  gutter.
- Editor → LSP wiring is per-file-lazy: the server starts on the first
  completion trigger for a supported file.