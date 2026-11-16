# LSP — Language Server Protocol in dmed

dmed ships a full-featured, in-process **LSP 3.x client** (JSON-RPC over
stdin/stdout, no third-party LSP library). A server is started **per file**,
only when the current tab holds a supported extension **and** the server binary
is on `PATH`, so an absent server degrades gracefully to plain buffer-word
completion instead of crashing the editor.

Everything below is in the open-source source under `internal/editor/` and
`internal/lsp/`; keys are the shipped defaults and can be re-bound in `[keys]`
in `.dmed.conf` (see `dmed --dump-keys actions` and `dmed --dump-keys md`).

---

## 1. Supported languages & servers

| Extension | Server binary | Language id |
| --- | --- | --- |
| `.go` | `gopls` | go |
| `.py` | `pyright-langserver --stdio` | python |
| `.ts` `.mts` `.cts` `.tsx` `.js` `.mjs` `.jsx` | `typescript-language-server --stdio` | typescript |
| `.rs` | `rust-analyzer` | rust |
| `.c` `.h` | `clangd` | c |
| `.cpp` `.cc` `.cxx` `.hpp` `.hh` `.hxx` | `clangd` | cpp |
| `.lua` | `lua-language-server` | lua |
| `.rb` | `solargraph stdio` | ruby |
| `.php` | `intelephense --stdio` | php |
| `.zig` | `zls` | zig |
| `.json` | `vscode-json-languageserver --stdio` | json |
| `.yaml` `.yml` | `yaml-language-server --stdio` | yaml |
| `.css` `.scss` `.less` | `vscode-css-languageserver --stdio` | css |
| `.html` | `vscode-html-languageserver --stdio` | html |

The mapping lives in `lspServerFor` (`internal/editor/lsp.go`). A hand-written
`[lsp] lang.<id> = <cmd>` in `.dmed.conf` **always wins** over the built-in
table, and an empty value (`lang.go = ""`) removes a language (per-file opt-out).

---

## 2. What you can do (feature → key chord)

| Feature | Default chord | Description |
| --- | --- | --- |
| Autocomplete | `Ctrl+Space` (auto on pause) | LSP candidates first, buffer words after; `Tab`/`Enter` accept, `Esc` dismiss |
| Diagnostics | gutter markers | Errors/warnings rendered in the gutter |
| Diagnostics navigation | `Alt+E` next / `Alt+Shift+E` prev | Walk diagnostics one at a time, wraps, remembers cursor |
| Go to definition | `F12` | Jump to the symbol under the cursor |
| Rename | `Ctrl+Alt+R` | Rename across files (open tabs only) |
| Code actions | `Ctrl+Alt+Q` | Quick fixes / refactors at caret |
| Format | `Ctrl+Alt+L` (legacy `Ctrl+Alt+F`) | Reformat the file via the server |
| Hover | `Ctrl+K` | Peek at symbol docs at caret |
| References | `Shift+F12` | Find all references |
| Outline | `Ctrl+Alt+O` | Document symbols / outline |

Every LSP chord is bound in `internal/editor/keys.go` (keys under `editorBindings`)
and listed in the palette, so any of them can be re-bound with `[keys]`.

---

## 3. Autocomplete

Completion comes from two sources merged by `triggerCompletion` / `complPanel`:

1. **LSP items** (`textDocument/completion`) — requested async when the user
   types or presses `Ctrl+Space`; a 6 s timeout prevents a hung server from
   stalling the UI.
2. **Buffer words** — identifiers scraped from the current file.

LSP items are listed first, buffer words second, and duplicates removed.
`Ctrl+Space` forces a popup even when no candidate exists. The popup is
floating, left-aligned at the edit column, and shows up to `complVisible` (8)
rows.

---

## 4. Diagnostics

Diagnostic markers come from `textDocument/diagnostic` (severity, message,
source). The pool is per open file; markers are rendered in the gutter as
error/warning hints. `Alt+E` / `Alt+Shift+E` walk them one at a time (wrap,
caret-aware), and the inline hint is shown in the status bar while navigating.

---

## 5. Go to definition

`F12` fires `textDocument/definition` on the active tab. The server's reply
(`textDocument/definition` → `Location`) lands as `lspDefinitionMsg`; the editor
moves the caret to the found location and records it on the pane jump list.
If the server returns no target, the overlay is simply empty. `textDocument/
definition` is a *request* — the client awaits one reply at a time.

---

## 6. Rename

`Ctrl+Alt+R` starts the rename flow: you type the new name, then the editor
asks `textDocument/rename` for a `WorkspaceEdit`. Accepted edits are applied
through `applyWorkspaceEdit`, so cleanup, undo and history all behave as with
any other edit. Only open tabs are rewritten by the server; nothing is written
to disk outside the normal edit path.

---

## 7. Code actions

`Ctrl+Alt+Q` opens the quick-fix panel (`lsp.actions`). Each action carries a
title, kind (`quickfix`, `refactor.*`, …) and an optional `WorkspaceEdit`.
`Up`/`Down` pick, `Enter` applies the selected edit (or the first with an edit).
`Up`/`Down` pick, `Enter` applies the selected edit (or the first with an edit).
The panel is answer-only while you type, and `Esc` closes it.

---

## 8. Format

`Ctrl+Alt+L` (legacy `Ctrl+Alt+F`) asks `textDocument/formatting` on the
active tab and applies the returned `TextEdit[]` array, honouring
`tabSize` / `insertSpaces` from the server reply. Formatting is a per-file job
and runs async; an empty reply means "no changes" (not an error).

---

## 9. Hover

`Ctrl+K` asks `textDocument/hover` at the caret (plus `textDocument/didChange`,
so the server always sees the current buffer text). The reply is markdown, which
is stripped of fences/emphasis down to plain lines and drawn as a **read-only
bottom overlay** in the M10 family (`internal/editor/m10.go`). `Esc` closes it.
A server that returns `null` simply opens nothing.

---

## 10. References & outline

- **References** — `Shift+F12` → `textDocument/references` → a location list
  rendered as a panel (`lsp.references`). `Enter` jumps to the hit.
- **Outline** — `Ctrl+Alt+O` → `textDocument/documentSymbol` → a symbol tree
  panel (`lsp.outline`). `Enter` jumps to the chosen symbol.

Both are async requests in the M10 family (`internal/editor/m10.go`), share the
same panel mechanics as the quick-fix list, and jump through `focusOrOpen` +
`pushJump`.

---

## 11. Adding a language server (Command Palette)

1. Open the Command Palette (`Ctrl+P`) and type `lsp add` — choose
   **LSP: Add Language Server…**.
2. Step 1 asks for the **language/extension** (prefilled from the active tab's
   extension when it is recognised, e.g. `go` for `main.go`).
3. Step 2 asks for the **server command line**; it is pre-filled from:
   - the existing `[lsp] lang.<id>` value if one is already set, else
   - the built-in `lspServerFor` command for that language.
4. Confirming writes `[lsp] lang.<id> = <cmd>` into `.dmed.conf` and removes the
   language from `[lsp] disabled` if it was there.

After save, dmed re-kicks the LSP client for the current file's extension if it
matches, so the new server is live immediately.

---

## 12. Installing a missing server

If the command you configured (`[lsp] lang.<id>`) resolves to a binary missing
from `PATH`, the editor shows the **LSP: Install Language Server…** palette
(`lsp_install`), listing `knownLSPServers`:

| Server | Install command |
| --- | --- |
| `gopls` | `go install golang.org/x/tools/gopls@latest` |
| `pyright-langserver` | `npm i -g pyright` |
| `typescript-language-server` | `npm i -g typescript-language-server typescript` |
| `rust-analyzer` | `rustup component add rust-analyzer` |
| `solargraph` | `gem install solargraph` |
| `intelephense` | `npm i -g intelephense` |
| `vscode-langservers-extracted` | `npm i -g vscode-langservers-extracted` |
| `yaml-language-server` | `npm i -g yaml-language-server` |

Pick a row, confirm and dmed runs the install command in a background goroutine
(`startLspInstall` → `cmd.CombinedOutput()`). When it finishes, the result is
delivered as `lspInstallResultMsg` and — if the current file's extension matched
the installed server — the LSP client is re-ensured.

---

## 13. Configuration

### `[lsp]` section of `.dmed.conf`

```
[lsp]
# per-language server override; wins over the built-in table
lang.go = gopls --stdio
# per-file opt-out
lang.py =
# add a language the table never knew
lang.foo = foo-ls
```

- A **blank** value removes the language (completes to the built-in table's answer).
- A **brand-new id** adds a language the table never knew.
- Everything else is ignored (unknown keys are kept, not dropped).

### Key rebinding

`[keys]` maps an **action name** to chords, so:

```
[keys]
lsp.hover = ctrl+k          # keep default
lsp.format = ctrl+alt+f     # explicit legacy chord
lsp.rename = ctrl+alt+r     # keep default
```

The editor reads `[keys].Overrides` and resolves them through `ResolveKeys`
(two-pass: clear first, then write, so a swap has a defined order). Refusals are
reported on the status bar.



