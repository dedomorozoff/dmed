# dmed — terminal editor with AI agents

A terminal editor with AI agents as first-class participants in editing,
full file change tracking, and humans approving every change through
diff review.

## Architecture

```
┌─────────────────────────────────────┐
│ TUI layer (bubbletea)               │
│  editor / diff view / chat panel    │
├─────────────────────────────────────┤
│ Core                                │
│  buffers (rope) │ undo │ multi-file │
├─────────────────────────────────────┤
│ Change tracking                     │
│  fsnotify + git + internal events   │
├─────────────────────────────────────┤
│ Agent layer                         │
│  LLM clients │ task queue │ diffs   │
└─────────────────────────────────────┘
```

Key principle: changes from agents go through diff → review → apply,
never directly into a buffer.

## Stack

- Go 1.26 (`D:\go\bin`, plain `go` from PATH)
- bubbletea + lipgloss — TUI
- fsnotify — file watching
- chroma → tree-sitter — highlighting (later)

## Milestones

### M0 — editor skeleton
- [x] Project scaffolding, `dmed` module
- [x] Buffer: insert/delete/backspace/newline, cursor, undo/redo (typing-run grouping)
- [x] Open/save file (`dmed <file>`)
- [x] TUI: rendering, cursor, scroll, status bar, gutter with line numbers
- [x] Bindings: arrows, home/end/pgup/pgdn, C-S save, C-Q/C-C quit, C-Z undo, C-Y redo
- [x] Unit tests for buffer + pty smoke tests (input, saving, navigation)

Run: `make build && ./dmed file.txt` (Go ≥ 1.26 from PATH; GOROOT is reset
in the Makefile as insurance).

### M1 — a real editor
- [x] Multi-file: tabs (`C-T` open via prompt, `C-W` close,
      `Alt+←/→` switch, `Alt+1..9` jump to tab)
- [x] Multi-file: splits (vertical/horizontal, `C-\`/`F6` and `Ctrl+Alt+H`/`F7`,
      switch panes `Ctrl+Alt+P`/`F8`, close `Ctrl+Alt+W`)
- [x] Fuzzy file finder (`C-O` or `F3`; subsequence scoring,
      focuses an already-open tab instead of duplicating)
- [x] Open a folder (`dmed dir/`): project tree in the sidebar (`C-B`/`F9`
      focus, `Esc` — back to the editor), finder searches inside the opened
      folder. File operations in the tree: `n`/`N` — new file/folder, `r` —
      rename, `d` — copy, `Del` — delete, `t` — move to trash
      (key hints at the bottom of the panel)
- [x] Move lines (`Alt+↑/↓`): current line or selection up/down, undo
- [x] Multi-cursor: multiple cursors (`Alt+Click`, `Alt+D` — next occurrence),
      editing/navigation across all cursors simultaneously (typing, backspace,
      delete, newline, paste; arrows move all cursors; `Esc` — reset)
- [x] Rope structure instead of `[][]rune`: the buffer is stored as a persistent
      balanced line tree (`internal/buffer/doc.go`), access and edits
      O(log n), undo/redo by root pointer (O(1), no cloning).
      Multi-cursor operates on the materialized view. + `internal/rope` —
      the base rune-rope (foundation).
- [x] Syntax highlighting (chroma), find/replace (`Ctrl+F` find, `Ctrl+H` replace, `F3`/`Shift+F3` navigation)

### M2 — change tracking
- [x] fsnotify: external changes → prompt to reload / auto-merge clean buffers
- [x] Git out of the box (repository detected automatically, zero-config,
  works immediately when opening a file inside a repo):
  - status in the gutter (added/modified/deleted: `+`, `~`, `_`),
  - diff view of changed lines + hunk navigation (`Alt+[` / `Alt+]`),
  - basic operations: stage/unstage, commit via the Git panel (`Ctrl+G`).
  Implementation: go-git (pure Go) — does not require an installed git.
  - [x] Inline diff preview in the git panel: side-by-side diff of the selected
        file is shown automatically while navigating the file list.
  - [x] Syntax highlighting in the diff view (chroma): both sides of the diff
        are colored by the file language's lexemes.
  - [x] Diff focus: Tab/right switches to the diff area (scrolling),
        left/Esc back to the files. Mouse: click = focus, wheel = scroll.
  - [x] Commit history (`l` in the git panel): list of the last 50 commits,
        side-by-side diff of each commit, j/k navigation, Tab→diff focus.
  - [x] Branches (`b` in the git panel): view branches, switch (Enter),
        create a new branch (N → name → Enter).
  - [x] Git init (`i` in the git panel): initialize a repository if missing;
        action hints in the git panel's status line.
  - [x] Fetch / push (`f` / `p` in the git panel, status mode): background
        sync to/from `origin` (go-git, embedded credentials, idempotent).
  - [x] Inline git blame (`Alt+B`): author · when annotations, right-aligned
        per line like Zed; only on unchanged lines and when no wrap.
- [x] Internal event bus (`internal/events`) (buffers ← watchers ← agents)

### M3 — AI v1
- [x] Chat panel with streaming, file/selection context
      (`Alt+A` — right panel; backends — provider presets: local
      Ollama, OpenAI, DeepSeek, Groq, LM Studio, vLLM, Unsloth; `DMED_PROVIDER`/
      `DMED_API_KEY`/`DMED_MODEL`/`DMED_OLLAMA_URL` override settings;
      with an empty config the first model from the server is used; Enter —
      send, Esc — close, PgUp/PgDn — scroll, Ctrl+U — new thread with a hint
      at the bottom of the panel)
- [x] Inline requests ("rewrite this") → diff preview → accept/reject
      (`Alt+I` — enter the instruction, Accept/Reject after streaming)
- [x] Providers: Ollama (local) + OpenAI-compatible (SSE streaming).
      Config: `provider`, `model`, `api_key`, `ollama_url`.
      Compatible with OpenAI, DeepSeek, Groq, Together, vLLM, LM Studio, Unsloth.
- [x] Chat with tools (native tool calling): the AI calls READ/SEARCH/RUN/EDIT
      via structured JSON functions (not text markers), results
      are returned to the dialog (loop up to 6 iterations). EDIT proposals are
      shown as a side-by-side diff (y accept / n reject, Tab — next file)
      and applied atomically via the agent Applier with reload/opening of tabs;
      tool results are rendered as compact cards, affected
      files are opened in tabs automatically.
- [x] Chat history: dialogs/threads are saved to `.dmed_chat.json` (last 20)
      and restored when the panel opens; navigation `Ctrl+P`/`Ctrl+N`
      (from the last — new thread, `Ctrl+U`), thread counter in the header;
      prompt history in the input field via `↑`/`↓` with a draft.

### M4 — agents
> Implementation plan: [docs/M4-AGENTS.md](docs/M4-AGENTS.md) (done)
> Package `internal/agent` (queue/runner/applier/committer) + TUI: `Alt+L` or
> the "Agent: New Task" palette — background task, queue with progress and
> cancellation, series diff-review (`Tab` across files, `y` accept / `n`
> reject), atomic apply + git commit + reload of clean buffers. Config `[agent]`.
- [x] Background tasks over multiple files ("refactor module X")
- [x] Task queue, progress, cancellation
- [x] Agent edits = a series of patches with atomic apply

### M5 — polish
- [x] Text selection (`Shift+arrows`, `Shift+Home/End`) with visual highlighting
- [x] Clipboard: copy (`Ctrl+C`), paste (`Ctrl+V`), cut (`Ctrl+X`)
      (`Ctrl+X` without a selection — close the tab, as before).
      Native system clipboard (`atotto/clipboard`): copy/cut write
      to the system clipboard, paste reads from the system + fallback to the
      internal one.
- [x] Terminal cursor (bubbletea v2 `View.Cursor`): blinking, on-screen
      position accounting for the gutter, scroll, split layout.
- [x] Window title: `dmed — <file name>` in the terminal title.
- [x] Mouse support (bubbletea v2 `MouseModeAllMotion`): click = move the
      cursor, wheel = scroll, drag = select text; gutter click = breakpoint
      (left) / bookmark (middle); hover over the status-bar
      panel icons (`▤` tree, `⎇` git, `✦` AI, `◉` debug, `❯` terminal) shows a
      callout and click toggles the panel.
- [x] Command palette (`Ctrl+P` / `F2`) — fuzzy search across all editor commands
- [x] Sessions: auto-save and restore open files on restart
- [x] LSP client (`internal/lsp`): JSON-RPC 2.0 over stdin/stdout, diagnostics,
      `Definition`, `DidOpen`/`DidChange` (integration with `gopls`/`pyright`)
- [x] "Go to definition" navigation: `F12` and `Ctrl+Click` on an identifier
      via LSP `textDocument/definition` (opens the file and places the cursor)
- [x] Command palette opens on double `Shift` (like JetBrains), in addition to
      `Ctrl+P`/`F2`; on terminals with the Kitty protocol / Windows Console API
- [x] Autocompletion: popup (`Ctrl+Space`, auto-trigger), sources — buffer
      words + LSP (gopls/pyright/typescript/rust-analyzer/clangd/lua/ruby/php/
      zls/json/yaml/css/html), asynchronous, with a fallback to words. Docs in
      `docs/AUTOCOMPLETION.md`. LSP diagnostics are rendered in the gutter.
- [x] Plugins/scripts
      — Lua framework (`internal/plugin`, gopher-lua): `.lua` in
      `~/.dmed/plugins` and `<project>/.dmed/plugins`, API `dmed.*`
      (on_key/command/on + text/set_text/cursor/insert/status/save).
      Auto-reload when a `.lua` file changes on disk. Plugin store
      ("Plugins: Install..."): built-in (Emmet, snippets) + remote from
      `plugins/` on GitHub (`[plugins] repo/dir/branch`), install/uninstall,
      without network — the built-in set. Uppercase is built into the editor (Ctrl+U).
      Docs in `docs/PLUGINS.md`. More events/API: done in M13 (bus events,
      async delivery, `dmed.spawn`).
- [x] Configuration (`.dmed.conf` INI): tab_width, syntax_theme, line_numbers,
      skipped_dirs, model, ollama_url, system_prompt, context_max,
      tree_width, chat_width_pct. `Settings: Open Config` in the palette,
      hot-reload on config save.
- [x] Built-in terminal (`Alt+T`): persistent ConPTY/PTY session at the bottom
      of the editor, ANSI screen emulation, colors/cursor addressing, interactive
      and full-screen TUI programs, resize, paste and native shell history.
- [x] Go to line (`Ctrl+L` in the editor / "Go to Line" palette): formats
      `N` (absolute), `N:C`, relative `+N`/`-N` (with a `+N:C` column).
- [x] Duplicate lines: `Ctrl+D` (copy below), `Alt+Shift+Down`/`Up`
      (copy below/above), with a selection — block duplication.
- [x] Line comments (`Ctrl+/`): by file type via the chroma lexer
      (`//`, `#`, `;`, `%`, `--`, `'`, `!`, `/* */`, `<!-- -->`), block based on
      the selection, independent per multi-cursor.
- [x] `internal/agent`: the task queue wakes the worker via a channel
      (`Queue.Wake`), without polling.
- [x] Word wrap (`Alt+Z` / "Toggle Word Wrap" palette): long lines
      are rendered in segments by pane width, breaking at words; setting
      `[editor] word_wrap`; wrap-aware scroll, caret, mouse click/drag/wheel.
- [x] Crash-safety of goroutines (`internal/debug.CapturePanicReport`):
      every spawned goroutine is wrapped so that a panic is logged to
      stderr instead of crashing the whole process (Windows exit status 2).

### M6 — AI onboarding
- [x] Provider presets in the AI: Preferences wizard (`←`/`→`): Ollama (local),
      OpenAI, DeepSeek, Groq, LM Studio (local), vLLM (local), Unsloth (local), Custom — the
      choice fills in the base URL and model; the provider is stored as the
      human-readable preset label, old `ollama`/`openai` values are normalized
- [x] Test button in the wizard (`t`/Enter): background polling of `Models()`
      with a 3s timeout, result ✓ connected · N models / ✗ with a human hint
      (refused → start ollama, 401 → check the key, unreachable → check the URL)
- [x] Base URL normalization in `internal/ai`: a trailing `/v1` is stripped so
      that an endpoint pasted from a provider's docs does not turn into `/v1/v1/...`
- [x] Env variables `DMED_PROVIDER` and `DMED_API_KEY` (over the configs)
- [x] Onboarding hint in the empty chat: the path to the wizard + a quick local
      route `ollama pull llama3.2`
- [x] CLI wizard `dmed setup-ai`: provider → key → test → write to
      `~/.dmed.conf` via `config.WriteAI` (`internal/setup`)

### M7 — debugging (DAP/Delve)
- [x] DAP client (`internal/dap`): its own transport over
      Content-Length framing (no external dependencies), reverse-connect
      mode (`dlv dap --client-addr`), initialize/launch/configurationDone,
      breakpoints, continue/next/stepIn/stepOut, threads/stackTrace/scopes/
      variables/evaluate, stopped/continued/output/exited/terminated/
      breakpoint/disconnected events. Unit tests on a small mock adapter over net.Pipe.
- [x] Debug panel (`Ctrl+Alt+D`): state header, columns
      threads / stack / variables (tree with expansion on Enter, Backspace —
      back out), process console (`l` — view with scroll-back, `Ctrl+L` —
      clear, eval line `> expr`). Fully mouse-driven: a click selects a
      thread/frame/variable (reloading the derived columns), a double click
      expands an expandable variable, the wheel walks the focused column. The
      lists scroll so the selection always stays on screen.
- [x] Breakpoints in the gutter (`F4`): `●` confirmed by the adapter; the
      current stop line is `▶`; one shared marker column with bookmarks (`◆`) —
      left click toggles a breakpoint, middle click (the wheel) a bookmark.
- [x] Controls: `F5` run/continue (and pause a running debuggee),
      `Shift+F5` stop, `F6` step over,
      `F7` step in, `Shift+F7` step out (panel-local; with the panel closed
      `F6`/`F7` keep the split bindings); auto-navigation to the file and line
      of the stop.
- [x] `[debug]` config: mode (debug/test/exec), program, args, stop_on_entry,
      dlv_path; "Debug: Toggle Debug Panel" palette command; i18n en/ru.
- [x] Generic adapters: `adapter_cmd`/`adapter_mode` (reverse|stdio|connect|
      dbgp)/`adapter_args`/`launch_type`/`launch_request`/`launch_json` — any
      DAP adapter (debugpy, lldb-dap, node, ...); Go/Delve is the default.
      `connect` dials a listening DAP endpoint instead of spawning an adapter
      process; `dbgp` spawns the interpreter and serves the session over the
      DBGp wire protocol, which is how PHP is debugged (Xdebug has no DAP).
      Asynchronous
      adapter start without blocking the UI, restart after the session ends,
      eval line with focus (Tab), unconfirmed breakpoints `○`.

### Post-M7 — polish
- [x] F1 help scrolls (`j`/`k`, PgUp/PgDn, Home/End, mouse wheel): the window
      is sized to the terminal height instead of being clipped; debug
      combinations were added to the help (Ctrl+Alt+D, F4/F5/F6/F7/S+F7/S+F5).
- [x] Debugger correctness pass: the launch sequence now sends launch →
      setBreakpoints → configurationDone (Delve rejects breakpoints before
      launch with "No debug session started", which used to abort the launch
      and leave every later F5 swallowed by the "session already attached"
      guard); `F5` pauses a running debuggee; the panel takes mouse clicks and
      its own scroll offsets; the status bar / hovered tooltip / overlay start
      rows account for the docked panel. Real-Delve integration tests cover a
      breakpoint hit and a pause.
- [x] PHP debugging through Xdebug: `internal/dbgp`, a DBGp client (a second
      protocol behind the same panel, since Xdebug does not speak DAP) —
      breakpoints, run/step, threads/stack/scopes/variables with expandable
      arrays, expression eval, and clean end-of-session handling. Detected from
      the active `.php` file; `php` is spawned with the session env and dials
      back over 9003. Verified against a real interpreter by
      `TestRealXdebugSession` and `TestPHPXdebugBreakpointHit` (both skip
      without PHP/Xdebug), which caught the wire details a mock had gotten
      wrong: the `<length>\0<xml>\0` framing of every engine document, the
      `iso-8859-1` XML prolog, the breakpoint id being a response attribute,
      errors arriving as an `<error>` child with no `success="0"`, and the
      base64-encoded `eval` expression.
- [x] Debug panel scrolling and fit: `PgUp`/`PgDn` move a screenful (clamped —
      they were a fixed six entries that wrapped around and lost the place),
      `Home`/`End` jump to the ends, and `+`/`-` resize the panel so a deep
      stack or a long variable list is not squeezed into a quarter of the
      screen. The console peek scrolls from the keyboard too (`↑↓`/`PgUp`/`PgDn`/
      `Home`/`End`, wheel direction fixed) and keeps its place while new output
      arrives. The row holding the focused selection is drawn across the whole
      panel, since a column is far too narrow for a long value or path.
- [x] Fixed PgDn across the whole application: in bubbletea v2 the key string is
      `pgdown`, while handlers matched the outdated `pgdn` (dead branches in chat,
      DAP, git, terminal, diff, AI panels, completion).

### M8 — agentic tooling

The AI stops answering and starts working through a proper tool surface. The
plan with per-item estimates, design decisions and open questions lives in
[docs/AI-TOOLS-PLAN.md](docs/AI-TOOLS-PLAN.md).

- [x] One tool result type for every tool: `ToolResult` (text + optional
      `Change` + optional `Park`) instead of a `(text, change)` pair plus a
      binary marker smuggled through the transcript.
- [x] One parking mechanism instead of one per tool: `Park`/`chatParks` queue,
      readable placeholders, `resolveParkedResult` on answer. Several approvals
      in one round are asked one by one and no decision is lost.
- [x] `[ai] tools_enabled` / `tools_disabled`: a small local model picks tools
      worse as the list grows, so the list is configurable and the wizard never
      rewrites it.
- [x] Alias table (`legacyToolName`): `read_file`, `write_file`, `edit_file`,
      `grep`, `run_command`, `list_dir`, `glob` all reach the shipped tool.
- [x] `LIST_DIR` and `GLOB` tools: the model learns the project layout without
      shelling out to `ls` (which needs confirmation under `allow_run = ask`).
- [x] `GLOB` understands `**` (zero or more path segments) and prunes the walk
      conservatively — a wrong prune would hide a file from the model.
- [x] One shared project walk (`walkProject` + `newProjectSkip`) for
      SEARCH/LIST_DIR/GLOB, with `.git`/`.dmed` skipped unconditionally.
- [x] `internal/todo` + `TODO_WRITE`/`TODO_SET`/`TODO_READ`: the model keeps a
      visible plan, `TODO_SET` ticks a known step instead of duplicating it, and
      the same rendered text goes to the model and to the chat panel.
- [x] `ask_user` (`internal/ask`): a question with a real overlay answer
      (choices with ↑↓, free text, Esc to skip) — the loop parks, the chat opens
      itself, and the answer replaces the placeholder. Several questions in one
      round queue up.
- [x] `web_search` (`internal/websearch`): DuckDuckGo HTML endpoint, own
      timeouts, pure parser tested against a fixture. Opt-in, capped per session,
      and when the budget is spent the tool is withdrawn from the model's list
      instead of only being rejected.
- [x] `switch_mode` + plan/act: plan mode exposes read-only tools only (a
      guarantee, not a policy — the tool is absent), the model must ask the human
      to leave it, and the status/header shows which mode is active.
- [x] Keyless default provider: `internal/ai` learned `APIPath`/`ModelsPath`
      ("OpenAI-compatible" is not always `/v1`), Pollinations is the second
      wizard preset, and a genuinely unconfigured session falls back to it once
      with a `CLOUD` badge and an explicit "this leaves your machine" notice.
      `free_fallback = false` opts out.
- [x] `sub_agent` (`internal/agent/subagent.go`): delegation to a nested agent
      with its own tool loop, capped rounds and a timeout. It can only read and
      propose (no shell, no questions, no second level of delegation), its task
      goes through the same queue and the same diff review, and the chat parks
      instead of blocking the UI while it runs.

### Post-M8 — housekeeping
- [x] `config.WriteAI`/`WriteLang` converted onto the shared `writeSection`
      engine (they were byte-for-byte near-duplicates of it; a new `[ai]` key
      no longer needs two edits).
- [x] The file finder and agent panel index the project through the same
      checker target `walkProject` + `newProjectSkipFor` as the AI tools,
      so the finder list cannot drift out of the AI's skip rules.
- [x] `maxInt`/`minInt` replaced with the Go builtins `max`/`min`.
- [x] Stray hardcoded English strings routed through i18n (m.t): terminal
      exit/error messages, "provider offline", "no models available",
      "replaced N occurrence(s)" — new keys `term.*`, `chat.provider_offline`,
      `chat.no_models`, `msg.replaced_many` in both catalogs.
- [x] `fetchRemoteSource` escapes each URL path component like the listing
      fetch already did (a branch/file name with a space or "?" produced a
      broken URL).
- [x] Dead `DMED_SHELL` branch removed from config.Load (the editor reads
      the variable itself).
- [x] `AIConfig.Unconfigured` is complete: it also counts a hand-tuned
      `ModelsPath` and a custom `SystemPrompt` as configuration. Your own
      prompt or model list must not leak to the keyless public fallback.
- [x] Lua plugins run under a wall-clock budget (`Manager.timeout`, 3s
      default): `SetContext` makes the VM check the deadline between
      instructions, so a `while true do end` handler or load fails with
      "context deadline exceeded" in the status bar instead of freezing the
      editor forever. Plugin call errors are no longer silently dropped.
- [x] The plugin store's HTTP fetches go through a test seam
      (`storeHTTPTransport`): unit tests redirect both the listing and the
      source downloads to a local httptest server, as with
      `aiFreeURLOverride`/`webSearchOverride`.
- [x] One generation-stamp helper: `termGen`/`chatGen`/`dapGen` bumps all go
      through `bumpGen`, keeping the supersede-session convention documented
      in one place for future async subsystems.

### Post-M8 — editor UX
- [x] **Find in Project** (`Ctrl+Shift+F`, the palette "Edit: Find in Project..."):
      async full-text search shared with the AI tools' walk rules (`walkProject` +
      `newProjectSkip`), results streamed in batches with a generation guard,
      live hit list (`↑↓`/Enter opens the file at the match, panel stays open),
      500-hit cap. Empty dir / no-file roots supported.
- [x] **Smart case + regex search**: a query without uppercase runes matches
      case-insensitively, in-file (Ctrl+F) and Find in Project alike; Tab in
      the search prompt toggles regex (`.*`), the hint row shows the state;
      invalid patterns match nothing and say so instead of degrading to a
      literal search. Regex replace-all (`Ctrl+A` in the replace prompt)
      expands `$1`/`$name` groups; the buffer gained `ReplaceAllRE` (a single
      undo step, line-wise like the literal path).
- [x] **Fuzzy command palette**: `filterPalette` now uses the file finder's
      scorer on titles (descriptions as fallback) instead of
      `strings.Contains` — consistent UX with `Ctrl+O`, subsequence queries
      work.
- [x] **Smart indent + typing niceties** (`internal/editor/smartedit.go`):
      block indent — Tab with a selection indents the selected lines
      (selection kept for Shift+Tab), Shift+Tab unindents one level without
      eating content (`buffer.IndentLines`/`UnindentLines`, single undo step);
      Enter continues the line's leading whitespace and goes one tab deeper
      after an open bracket before the caret (`buffer.IndentedNewline`); auto-
      close for `(`/`[`/`{`/`"` with type-over of the closing character and a
      pair-consuming Backspace. Fixed a real bug on the way: typing over a
      selection swallowed the first keystroke (`buffer.Insert`/`InsertNewline`
      deleted the selection and returned without inserting).
- [x] **Bookmarks survive a restart**: the session file gained a `bookmarks`
      section (abs path → sorted 1-based lines); `New(dir)` restores them next
      to files/cursors, and bookmark jumps work right away on the restored
      set. The session file itself (`.dmed_session.json`) is hidden from
      project walks (SEARCH/LIST_DIR/GLOB/agent tools). Persisting undo
      stacks across restarts was considered and deliberately left out: an
      undo history that survives a file being reloaded is more confusing
      than useful.
- [x] **LSP rename + code actions** (`Ctrl+Alt+R` / `Ctrl+Alt+Q`, the palette
      "Edit: Rename Symbol..." / "Edit: Code Actions..."): rename asks the
      server for the symbol under the cursor (`internal/lsp.Client.Rename`),
      shows an input pre-filled with the word, and applies the reply
      `WorkspaceEdit` across files — open tabs edit in-buffer (one
      `ReplaceSpan` per edit, tab stays dirty, server notified via
      didChange), closed paths go straight to disk. Code actions list the
      server's quick fixes in a bottom panel (↑↓/Enter/Esc); a zero-width
      range at the cursor asks for them. Protocol plumbing in
      `internal/lsp`: `ColToRune` (UTF-16 → rune, astral planes correct),
      `SortedEdits` (right-to-left), `ApplyEditsToText` (multi-line insert/
      replace), `parseWorkspaceEdit` (both `changes` and `documentChanges`
      reply shapes); command-only actions are shown but refuse to run.
      Legacy terminals that lose `Ctrl+Alt+*` use the palette commands.
- [x] **Code block folding**: indentation-based fold ranges (a non-blank line
      whose next non-blank line is deeper, ending where the indent falls back —
      nested blocks included), display-only state that never touches the
      buffer. The whole pane renders from one display-row list (`dispRows`):
      folded blocks collapse to a `… N lines hidden` hint right under the
      start line, working with and without word wrap. Folding goes through
      chevrons in the gutter (▾/▸, one extra column only when the buffer is
      foldable, and that column is part of the reported gutter width so a
      click lands on the cell under the pointer and the content stays aligned
      with the gutter), a click anywhere on the summary row, Alt+C / Alt+O
      (vim zc/zo), and "Edit: Toggle Fold" / "Edit: Toggle All Folds" from
      the palette. The keyboard commands act on the block the cursor
      *belongs to* (`foldTargetAt`): the one starting on its line, the
      innermost one around it — the closing brace included, even though the
      scan leaves that line visible — or the block whose lone brace sits on the
      line below an Allman signature, which is what made a
      `function foo()` fold from the line above. Folding from inside moves the
      caret onto the block's start line: left on a hidden line, the next
      `clampScroll` would force the very block open again.
      Arrows and PgUp/PgDn skip over hidden lines so the cursor can never enter
      a folded block; a cursor that ends up on a hidden line (undo, an external
      edit, a jump) forces the block open in `clampScroll`. Fold ranges are
      cached like wrap segments and stale folded marks heal themselves on the
      next render.
- [x] **Folding performance**: the display-row list, the fold scan and the
      gutter chevrons are linear in the size of the buffer. `dispRows` walks
      the folded blocks with a moving pointer instead of rescanning them per
      line, `foldAtStart` is a binary search over the sorted ranges (the
      gutter asks for it on every visible row) and the block scan itself is a
      single stack pass. The document text is cached in the buffer and
      invalidated by the tree root, so the view-layer caches (syntax, wrap,
      folds) stop rebuilding the whole file string several times per
      keystroke. On a 20k-line file with everything folded this took the
      frame from 11 s to 17 ms.
- [x] **Background syntax highlighting**: chroma re-tokenizes the whole
      buffer, which on a large file costs seconds and hundreds of megabytes
      of garbage. The highlighting now runs off the UI thread: the editor
      asks for a pass when the cached highlight no longer matches the
      buffer, the pass runs in the background and the result is swapped in
      when it lands. A pass for text the buffer has moved away from is
      dropped instead of painted. Typing never waits for the tokenizer any
      more; the colors of a line that changed catch up a moment later.
      The cache itself holds one token per character instead of a whole
      style: a file with 60k lines went from 443 MB of styles to 6 MB of
      tokens, and the styles are resolved when a line is painted.
      A pass also waits for the typing to settle: while the buffer keeps
      changing, the wait is restarted and no tokenizing happens at all, so a
      typing burst in a huge file costs nothing.
- [x] **Cheaper frames**: the per-frame lookups that do not change with the
      buffer are cached on the tab — the syntax language of the file (chroma
      asks the filesystem for it) and the repository of the file's directory
      (opening a repository touches the filesystem). The fold scan reads the
      indentation of a line in place instead of copying the line out of the
      buffer, which allocated once per line of the file on every edit.
      Typing and rendering a 20k-line file now costs about 7 ms per
      keystroke (was 457 ms) and 4 MB of garbage (was 493 MB).
- [x] **Git diff in the background**: the gutter shows which lines differ
      from HEAD, and that diff is a whole-file job — the text is diffed line
      by line against the committed version, which cost 42 ms per keystroke
      on a file of 3000 lines. It runs in the background now, like the
      highlighting, and waits for the typing to settle before it starts.
      The line painting groups the characters of equal style into runs: a
      lipgloss render per character was thousands of calls per frame.
- [x] **Search everywhere**: a double `Shift` opens the JetBrains-style
      search: commands, project files and settings in one list, each result
      marked with what it is. Picking a file opens it, picking a setting
      opens the configuration file. `Ctrl+P` / `F2` keep searching commands
      only. Since barely any terminal reports a bare `Shift` (and on legacy
      input `Ctrl+Shift+P` collapses into the `Ctrl+P` control byte), the
      bindings that actually reach the list are `Alt+P` — distinguishable
      everywhere, because `Alt+letter` is sent as `ESC+letter` — and
      `Ctrl+Shift+P` on terminals that tell Shift apart.
- [x] **Ctrl+C never quits**: Ctrl+C copies the selection (as before) and
      otherwise says how to leave the editor — an editor that dies on the key
      a terminal uses to interrupt a program is a trap. `Ctrl+Q` is the way
      out, and the interrupt signal is filtered out too (SIGTERM still stops
      the editor).
- [x] **Partial highlighting**: chroma hands out the tokens of a file in one
      go, so the colors of a large file only appeared when the whole file
      was tokenized. The tokenizer is now consumed as it goes and the pass
      publishes the lines it has done — the visible part of a file is
      painted in a fraction of a second and the rest catches up.
      The language tag of a file comes from a table instead of chroma:
      asking chroma for it initializes its lexer registry, which parses every
      embedded grammar and takes seconds.
- [x] **SQLite browser**: opening a `.sqlite`/`.db` file (magic-header
      verified, so a text-only `.db` stays a text tab) opens a full-screen
      browser instead of a garbage binary tab: a table/view strip (Tab
      cycles), paged row grid (50 rows), substring filter over every column
      (`/`), sorting (`o` cycles the cursor column asc/desc/off), the CREATE
      statement (`s`), and immediate editing with the rowid as the row
      address — Enter edits the cell (empty value = NULL, values go in
      bound), Ctrl+N inserts an all-defaults row, Ctrl+D deletes the cursor
      row. Views and WITHOUT ROWID tables are shown read-only: without a
      rowid there is no stable row address. The data layer is
      `internal/sqlitedb` on modernc.org/sqlite (pure Go, no CGO — the
      8-platform cross build stays green); user-supplied values move as bound
      parameters, all identifiers are double-quoted with "" escaping.

- [x] **ASCII picture preview**: a `.png`/`.jpg`/`.jpeg`/`.gif`/`.bmp` file
      (extension list + decoder as the real check) opens as a picture tab:
      the buffer stays empty (the pane draws the picture, never loads the
      bytes in), the pane renders half-blocks (`▀`, two pixels per cell, box
      average with the aspect through the 2:1 cell) with the terminal's colour
      profile via `colorprofile` (ANSI-256/ANSI/ASCII degradations) and the ASCII
      ramp fallback for a colourless terminal, GIF frames animated through a
      single tick chain (`imgFrameMsg` + `imgTick` + one pending flag, frame
      counter in the caption, delay from the file's own numbers, clamped to
      something watchable). Keys on a picture tab are the workspace's
      (`Ctrl+S`/`Ctrl+C`/`Ctrl+V` refused as read-only with an explanation, `Ctrl+E`
      chat still reachable via palette), scrolling a tall picture is the pane
      offset, the wheel scrolls it too. Reload (watcher) re-decodes and falls
      back to text with the reason if the file stopped being one.
- [x] **Tree panel: names survive the width**. Three related failures, one
      cause: entries were composed without a true width budget. An entry whose
      name plus its tree prefix overflowed the panel used to slice the plain
      string with `len+pad`, which lands at or before the string start for
      deeply nested names and rendered the row blank; the same overflowing
      rows were one cell wider than the panel, which shifted the whole editor
      row (line numbers included) one character right on those lines only; and
      deep ancestors ate the name outright (`│   │   ├── ` costs 4 cells per
      level). The render now composes each row to the panel width: the
      ancestor gauge collapses to an ellipsis before the final connector so a
      nested file keeps its full name, only a name too long for whatever is
      left is cut — from the head, keeping the readable tail (extension
      included) — and no row ever grows past the panel width.
- [x] **LSP formatting**: `textDocument/formatting` reaches the language
      server (`gopls`→gofmt, the TypeScript server→prettier, whatever else
      implements it): `Ctrl+Alt+F` and the palette "Edit: Format Document"
      ask for a whole-document layout, the reply (a bare TextEdit array,
      parsed apart from the WorkspaceEdit shapes rename/code actions return)
      lands in the buffer through the same `applyWorkspaceEdit` path and the
      buffer stays dirty — the layout is on screen, saving stays the human's
      call. Rename and code actions already existed; the analysis calling
      them absent was out of date.
- [x] **AI rounds stop walking the whole project**: the touched-file detection
      after every tool batch cost two full `snapshotFiles` walks over the root
      (`before` + `after`) even when the batch was pure reading (SEARCH,
      GLOB, TODO_*). The walks are gated by `toolBatchTouchesDisk` now: only
      a batch containing RUN/EDIT/REPLACE pays the walk, so read-only rounds
      between AI iterations no longer crawl a large repository.
- [x] **Undo of AI work from inside the editor**: accepted agent changes are
      committed at once, so Ctrl+Z never could reach them. `u` in the agent
      panel (or the palette command) reverts the newest `agent: …` commit
      through `vcs.RevertCommit`: refuse-first — every working-tree file must
      still match what the agent commit produced, one user edit after the
      apply aborts the revert without writing a byte — and the restore is
      staged into its own `agent: revert <hash>` commit, so the undo is
      traceable in history rather than a silent tree rewrite. No external
      terminal needed.

## M9–M13 (M9, M10, M11 and M13 done; M12 planned)

The list below is drawn from the aftermath of Post-M8: the "chroma →
tree-sitter (later)" note in the stack, the plugins' "Remaining: more
events/API", the hard-coded keybindings and gaps found in daily use.
Order and contents will be refined as work proceeds.

### M9 — editing: the last gaps
- [x] **Bracket matching**: the pair under the cursor is highlighted, `Ctrl+]`
      jumps to it and `Ctrl+Shift+]` selects inside it. Pairs come from the
      scanner in `internal/buffer/match.go`, not from the fold scan: it walks the
      buffer itself — brackets by depth, quotes by line parity, `\(`-style escapes
      skipped — inside a 2000-line window, so no AST is needed and nothing is
      re-tokenized. The jump answers from anywhere, not only with the caret on a
      delimiter: first the partner of the delimiter it sits on (brackets *and*
      quotes), otherwise the closing delimiter of the innermost enclosing pair.
      Enclosing is brackets-only, because deciding "am I inside a string" needs the
      line-wide quote parity that is a different question from scanning a region.
      The left walk raises a per-type debt at every closer and lets an opener pay
      it, so in `f(a) + g(b)` the caret inside `g` gets `g`'s paren and a pair
      already closed to the left cancels itself out. Landing *on* a delimiter is
      what makes one chord walk the pair back — necessary, since a backward
      `Ctrl+[` cannot exist: it *is* Escape. `Ctrl+]` and its legacy-terminal alias
      `ctrl+}` (the two share one control byte) jump, while the shifted select
      chord needs a terminal that can tell them apart and is palette-only elsewhere.
      Select inside takes the content only, in one step, delimiters excluded; empty
      brackets are not a refusal — the caret moves between them and the status line
      says so. With no pair anywhere nothing moves and no message lies about a
      match. Both commands drop secondary cursors, dirty nothing and push no undo
      step, and the selection needed its own setter (`SelectRange`) for exactly that
      reason: `DragSelect` re-clamps the column through the sticky goal column and
      would quietly move an end the command had just computed.
- [x] Column (rectangular) selection: `Ctrl+Alt+Shift+arrows` grows a block
      (first press enters column mode from the caret or from a linear
      selection's bounding box), the palette "Edit: Column Selection"
      reinterprets an existing selection as a rectangle. The block is the
      buffer's two selection corners read as a rectangle (`internal/buffer/
      column.go`) — no new mode, the ~20 places that clear a selection end the
      block with it, and the linear extend chords clear the flag so a stale
      block cannot turn an ordinary selection rectangular. The corner column
      is the caret's sticky goal column, so a vertical grow keeps its width
      across short rows; cells are derived per read and clamped to each row, a
      row past the block's left edge holds nothing. Typing puts one line into
      every cell, a multi-line paste distributes one line per row, a block
      copied to the clipboard unfolds back down a column on paste
      (`clipboardBlock` remembers it — the system clipboard carries no type),
      Backspace/Delete work per cell with the character in front of an empty
      one, Enter splits every row at the block's left edge, case folding
      touches one cell per row, and a row-permutation command does not narrow
      the corner. Every edit is one undo step and leaves a caret per row, so
      the block turns into ordinary multi-cursor state. A Shift+drag grows the
      block with the pointer (the chords need a terminal that reports three
      modifiers; the drag and the palette toggle do not).
- Fold ranges are still purely indentation-based, which no target lookup can
  fix: a `switch` whose `case` labels sit at the `switch` column opens no block
  of its own, so Alt+C there folds the enclosing function instead, and a body
  written at its header's indent folds nowhere at all. The honest fix is
  brace-aware ranges built on the delimiter scanner in
  `internal/buffer/match.go`, which belongs with the tree-sitter item in M11.
- [x] **Selection transformations**: upper/lower case (`Ctrl+U`/`Ctrl+Shift+U`),
      sort lines (`Alt+Shift+S`), trim trailing whitespace (`Alt+Shift+T`),
      align indentation (`Ctrl+Alt+J`) and sequence numbering 1..N
      (`Ctrl+Alt+N`), all also palette commands. Case folds the selected
      characters; the line-wise commands take the whole lines the selection
      touches — the cursor line when there is none, the rule `DuplicateLine` and
      `IndentLines` already follow. Sort compares case-folded and breaks the tie
      on the exact runes, so the order is total, and moves blank lines to the end
      of the block where a gap in a list does not read as a bug. Trim cuts
      trailing spaces and tabs only (indentation and interior blank lines are
      content) and, with nothing selected, cleans the whole file — the pre-commit
      case. Align never rewrites an indent: it pads the shallower lines with
      spaces up to the deepest content column the block already has, measured in
      expanded columns, so a tab-indented line stays byte-for-byte its own.
      Numbering writes `N. ` after the line's indentation, skips blank lines
      without consuming a number and replaces an existing `N.`/`N)` marker, so
      pressing it twice cannot yield `2. 1. item`. Each command is one undo step,
      keeps the selection (none of them changes the line count, so the same block
      can be transformed again) and drops secondary cursors, because a block
      rewrite permutes whole lines under them. Fixing the primitive also fixed
      `Ctrl+U` on an unselected buffer: it no longer rebuilds the buffer, so the
      undo history and the caret survive it.
- [x] **Macros**: record/playback (`Alt+R` starts and stops, `Alt+Shift+R`
      replays), respecting multi-cursor. Only the keys the *text pane*
      consumes are recorded — the editor bindings and the typing fallback;
      the global chords and the pane/overlay keys run as usual while
      recording but never land in the macro, so a macro can neither swallow
      Ctrl+Q nor replay into whatever panel has focus. Playback feeds every
      recorded key through `dispatchKey` — the very chain the live keys ran
      through (it was factored out of `handleKey` for exactly that) — so a
      replay respects multi-cursor, smart indent and the column edits by
      construction, one key per UI step along a tick chain like the GIF
      frames. Replaying while recording is stop-then-replay, an empty
      recording is discarded with a message, and the recording lives in the
      Model unpersisted — a keyboard memory of a few seconds, not a session
      artifact.
- [x] **Jump list**: `Ctrl+Alt+←` / `Ctrl+Alt+→` (also the `Edit: Jump Back` /
      `Edit: Jump Forward` palette commands) walk the positions the discrete jumps
      came from, per pane. Only a jump the user cannot retrace by hand records one:
      goto-definition (`F12` and `Ctrl+Click`, both through the async reply), a
      Find-in-Project hit, `Ctrl+L` Go-to-Line and the debugger's stop line.
      Arrows, clicks, the in-file find moves and the walks that already own a chord
      (`Alt+N` bookmarks, `Alt+[` / `Alt+]` hunks, `Ctrl+]` the pair) push nothing:
      a chord that can retrace itself needs no history, and a history that grows
      while the user types is a history nobody can read. An entry is a
      `(path, line, column)` triple, never a tab index — indices are renumbered by
      every close, while a path still resolves through `focusOrOpen`, so a back
      step reopens a file whose tab was closed meanwhile. A new jump abandons the
      forward branch, an entry equal to where the caret already stands is skipped
      at both ends so no press can be visibly a no-op, and both stacks are capped
      at 64 with the oldest dropped. The pair of stacks lives in the `pane` next to
      the scroll offsets: each split answers for its own navigation and nothing is
      persisted. Pure caret and focus moves — no edit, no dirty flag, no undo step,
      secondary cursors dropped first, `clampScroll` unfolding a block the landing
      line hides inside. A terminal that cannot tell `Ctrl+Alt+←` from `Alt+←`
      gives the tab switch, which is why the palette route is the documented one.
- [x] **MRU tab switching and last change**: `Ctrl+Tab` (portable alias
      Alt+backquote, the `View: Cycle Recent Tabs` palette command) walks this pane's
      recency order one tab per press and wraps back through the tab the walk started
      on; `Ctrl+Shift+Backspace` (`Edit: Go to Last Change`) lands the caret where
      this file was last changed. The recency list is `pane` state holding tab
      *indices*, unlike the jump list: it must never reopen a file the user closed
      and it has to address the untitled buffer whose path is empty, so the one
      place a close already renumbers the pane renumbers it too. The walk freezes
      the order it derived on the first press and focuses through a quiet setter —
      a walk that recorded each tab it left would reorder the list under itself and
      turn the cycle into a ping-pong between two tabs. Every other focus change
      (`Alt+1..9`, a click on the tab bar, the finder, a split) records and resets,
      a held chord repeats and so walks the list, and a pane with fewer than two
      live tabs refuses. Neither command is a jump producer: tab switching keeps
      meaning *which tab was last in front of me* while `Ctrl+Alt+←` keeps meaning
      *where my caret was*. The last change needed no change counter — `pushUndo`
      is the choke point every mutating method already passes through (typing runs,
      the line commands, the transformations, the multi-cursor edits, redo) and its
      undo snapshot takes the caret with it, so the tracker is three fields written
      next to that snapshot plus one getter. It records where the change *started*,
      which is also what one `Ctrl+Z` restores, and survives the undo that removes
      it: after `Ctrl+Z` that spot is still the answer. It is per file, never
      cross-file — the jump list already carries the caret across files. Both
      refusals move nothing and push nothing, a target equal to the caret is
      refused instead of spending a jump slot on a no-op, and both commands are
      pure caret and focus moves: no edit, no dirty flag, no undo step, secondary
      cursors dropped, `clampScroll` unfolding the block a landing line hides
      inside. A legacy terminal sends `Ctrl+Tab` as the plain `\t` (the indent
      binding) and `Ctrl+Shift+Backspace` as `0x08` (`Ctrl+H`, Find and Replace),
      and Windows Terminal routes `Ctrl+Tab` to its own tab bar — which is why the
      palette is the documented route for both.
- [x] **Replace in Project**: the counterpart of Find in Project
      (`Ctrl+Shift+R`, also in the palette). A two-step prompt (query, then
      replacement) streams a file-level preview, one whole-file `agent.Change`
      per matching file, and `Enter` opens a side-by-side diff review of that
      series — `y` applies the file under review, `a` the whole batch, `n`
      discards. Nothing is ever written around the review: `agent.Applier`
      re-validates every `Orig` against the disk and writes all-or-nothing, so
      one edited file between the preview and the answer rejects the series
      without touching a byte. Files an open tab still holds unsaved and
      non-UTF-8 files are listed as excluded rather than silently rewritten,
      and the file cap (200) is reported, never applied quietly. The applied
      series is deliberately not committed — a human bulk edit is ordinary
      working-tree work, and git is the undo.
- [x] **Merge conflicts**: navigate the `<<<<<<<`/`=======`/`>>>>>>>` markers and
      take ours/theirs/both under the cursor (`Ctrl+Shift+O`/`T`/`B`, the same
      three in the palette; `Alt+[`/`Alt+]` jump between conflicts). No overlay
      and no new mode: the markers are text the buffer already holds, so one
      resolution is one `buffer.ReplaceSpan` — one undo step per hunk, `Ctrl+Z`
      peels exactly one. Markers are matched strictly (column 0, exactly seven
      characters, then git's label or the end of the line; the separator only as
      a bare `=======` line, so a Markdown setext underline is never read as a
      conflict), the diff3 `|||||||` base section is recognized and always
      dropped as context, and a `<<<<<<<` that never closes is reported as
      malformed and never edited — guessing there would delete text the user
      still has to read. `Alt+[`/`Alt+]` prefer conflicts over Git hunks while
      the file holds any (a conflicted file always has a HEAD diff too, so the
      other order would make conflicts unreachable by keyboard) and go back to
      hunk navigation once the last one is resolved. An empty side leaves no
      blank line behind, opening a conflicted file reports how many there are,
      and the action touches neither disk nor the git index — saving and staging
      stay `Ctrl+S` and the Git panel.

### M10 — LSP to the end
- [x] Diagnostics navigation: `Alt+E` next / `Alt+Shift+E` previous (the
      palette "Edit: Next Diagnostic" is the documented route for legacy
      terminals), per active tab, sorted by position, wrapping, the walk
      counts a caret already parked on a diagnostic so the first press shows
      the hint where the cursor stands. Each jump records a jump-list entry
      (`Ctrl+Alt+←` walks back), and the status line carries the index, the
      severity and the message — the inline hint of the line under the cursor.
      (F8/Shift+F8 from the plan stayed taken: F8 switches panes.)
- [x] Hover documentation (`Ctrl+K`, the palette "Edit: Hover"): the server's
      markdown as a bottom overlay — code fences lose their markers, backticks
      unwrap, long lines clip to the pane width; Esc closes, an empty reply
      says so instead of opening an empty box.
- [x] References ("Find Usages", `Shift+F12`, the palette "Edit: Find
      References"): a bottom list like the code-actions panel (↑↓/j/k, Enter
      jumps through `focusOrOpen` + a jump-list entry, Esc closes). The
      declaration is included, so the definition the usages hang off is in
      the list.
- [x] Outline (`Ctrl+Alt+O`, the palette "View: Document Outline"): the
      document symbols in a bottom list with a kind glyph, the line number and
      the detail; Enter jumps. Both reply shapes parse — hierarchical
      DocumentSymbol[] (flattened in document order) and flat
      SymbolInformation[].
- [x] The language table moved into the config: `[lsp] lang.<id> = command
      [args...]` replaces a built-in entry (`lang.go = my-gopls --stdio`),
      an empty value removes the language (`lang.python =`), and a bare
      extension id adds a language the table never knew (`lang.foo =
      foo-ls`). The lookup answers by the built-in language id first, then the
      bare extension.
- [x] Auto-restart of a crashed server: the client marks itself dead when its
      read loop hits EOF (`Alive`), `ensureLSP` drops a dead client and
      lazily spawns a fresh one on the next LSP request, with a status
      message.
- [x] `workspace/didChangeWatchedFiles`: a watcher event outside the editor
      (an agent commit past the open tabs, a tool run) is forwarded to the
      server, so its caches drop the stale file and the diagnostics follow
      the working tree.
- [x] Health-check in the palette: "LSP: Server Status" already existed
      (`lspStatusMsg`); this milestone's restart command (`msg.lsp_respawned`
      on a crash) completes the picture.
- Workspace symbols in the palette / search-everywhere: deliberately left
  out — document outline covers the daily navigation, and a workspace-wide
  symbol query needs result streaming and a cap the current panel shape does
  not have. Revisit if it hurts.
- Signature help while typing: left out with the same reasoning — the hover
  overlay shows the same information on demand, and an auto-popping signature
  window fights the completion popup for the same corner of the screen.

### M11 — highlighting: incremental
Careful: go-tree-sitter pulls cgo and breaks the 8-platform no-CGO
cross-build. The cheap part first:
- [x] **Incremental highlighting on chroma** (`internal/syntax/incremental.go`,
      the `Incremental` per-line token cache): tokenization is a deterministic
      function of the text prefix, so lines whose text is byte-for-byte what
      the previous pass saw can be spliced from the cache — the lexer reaches
      them in the same state. The pass splices the unchanged prefix, then
      tokenizes forward watching a re-sync window: when 3 consecutive fresh
      lines equal their cached tokens (only where the line's text hash also
      matches, so the cached entry really is that line's previous self), the
      lexer is demonstrably back in the state the cache was built with — a
      state shift (a new block comment, an unterminated string) would have
      made at least one window line differ — and the iteration *stops*, the
      remaining tail splices from the cache without tokenizing it. Correctness
      never depends on the heuristic: a wrong decision to keep walking fresh
      is slow, not wrong. The tab adopts the cache its pass ran through
      (`tab.inc`, stamped with the language it was built for; a language
      change resets it), an unchanged text short-circuits before any lexer
      pass at all, and a language change between passes drops the cache. A
      one-line edit in a 10k-line Go file went from ~500 ms (full re-tokenize)
      to ~49 ms — 10x, the bulk of it from not draining the lexer's iterator
      past the re-sync point. The chunked publishing (`syntaxReadyMsg.offset`)
      keeps the visible-part-first paint.
- [x] **Embedded languages**: chroma's composite lexers already cover the
      plan's cases through the plain `HighlightBuffer` path — a fenced go
      block in markdown arrives as Go tokens (the markdown lexer delegates to
      the language lexers), and JavaScript template-string interpolation is
      tokenized as `LiteralStringInterpol`. Pinned by `TestEmbeddedLanguages`
      so a chroma upgrade cannot silently drop either.
- Tree-sitter stays a stretch goal, only if pure-Go bindings appear; then
  folding and bracket matching move from the buffer's own scans
  (`buffer/match.go`, the fold scan) to the AST — that is also where the
  M9 fold-ranges item landed.

### M12 — AI v3: agents without stops
- [x] MCP client: attaching external tool servers (`[ai] mcp_servers`), their
  tools join the same tool loop; their changes still go through the
  Applier only. Transport: stdio first, HTTP/SSE later.
  - [x] **The keys are the `[lsp] lang.<id>` shape.** `[ai] mcp.<name> = command
        [args…]` attaches a server; an empty value removes one the other config
        file added. The client is `internal/mcp`: JSON-RPC 2.0 over stdio, one
        frame per line (the stdio transport is *not* Content-Length framing),
        initialize → tools/list (with cursor pagination) → tools/call, and a
        server request to the client answered method-not-found rather than left
        to hang. A genuinely configured server also silences the keyless
        fallback: `Unconfigured()` counts an attached `mcp.<name>`.
  - [x] **The round is async, the call is not.** Spawning N processes and
        shaking hands with each runs as one stamped generation like `dapGen`
        and `termGen`, its results land as a message, and a late reply from a
        superseded server set is dropped; a hot reload respawns only when the
        `mcp.*` fingerprint actually moved, and the old processes die on the
        UI thread first. One tool call then runs synchronously with a 60 s
        deadline, exactly like RUN: the loop already waits on RUN that way.
  - [x] **Exposure and gating.** Tools arrive as `mcp_<server>_<tool>` (a name
        too long for the providers is reported, never sent), curated by the same
        `tools_enabled`/`tools_disabled` as the built-ins, and dropped wholesale
        in plan mode — an MCP tool's claimed capability is a claim, not a
        guarantee the absence of tools can make. A direct MCP write is an
        external change like a RUN one: every MCP call counts for the
        touched-file walk, so what a server rewrote opens in a tab; a server's
        proposal would go through the review like any other Change.
  - [x] **`mcp_ask` (default on) parks every call for a human y/n** — the same
        park queue, the same `y`/`n` protection as RUN's confirmation, the
        placeholder replaced by the real outcome. The unit tests drive the
        real protocol over `net.Pipe`, so no test spawns a server process.
- [x] Agent tasks survive a restart: persist the task transcript, resume from
  the panel, parallel agents in the queue.
  - [x] **`.dmed_agent.json` beside the session and chat files.** Every status
        change writes the durable shape (ids, statuses, errors — a progress
        tick rewrites nothing); a running or queued task cannot outlive the
        editor that streamed it, so it is stored cancelled with "interrupted —
        the editor exited" naming the cause, and a review left unfinished comes
        back whole, changes included, ready to `y` after a crash. `persist =
        false` switches the file off; a corrupt one opens an empty queue rather
        than failing the editor.
  - [x] **Restore is a seed, not a resurrection.** `Queue.Restore` appends the
        saved tasks, skips an ID it already has, and moves the order counter
        past every restored one — and the ID itself gained the date, because
        "same minute of a different day" used to be a collision. `r` on a
        finished row retries its prompt as a *new* task: the interrupted one
        keeps its row, its error and its place in history. A delegated (sub)
        task is refused — the chat waiting on its result is long gone.
  - [x] **The queue got a pool.** `[agent] workers = 2` (1..4, an out-of-range
        value keeps the default: a typo must not quietly halve or stampede the
        parallelism): each worker registers its own wake channel, one enqueue
        signals all of them, and a burst coalesces per channel without losing
        a task — the runner was always keyed by task id, so two tasks stream,
        review and cancel independently.
- [x] Slash commands / prompt templates: `/refactor`, `/test`, user ones in
  `.dmed/templates` (plain files or Lua).
  - [x] **A command is a template, not a protocol.** Enter on `/name rest`
        expands the named template and sends *that* to the model; the raw
        command goes to the prompt history so `↑` recalls what was typed,
        while the transcript shows what the model actually saw. A bare `/` or
        `/help` answers with the listing; an unknown name answers with it too
        instead of burning a round.
  - [x] **Project shadows home shadows built-in.** `<project>/.dmed/templates`
        beats `~/.dmed/templates`, which beats the shipped `/refactor` and
        `/test`; the scan runs at submit, because templates are edited in the
        very editor that reads them. A plain file's first `# ` line is its
        description and never reaches the prompt; `{args}` is replaced, and a
        body without the placeholder gets the arguments appended — no
        arguments get the model a line telling it to ask, so an empty target
        never reads as named.
  - [x] **`.lua` templates run under the plugin's own budget.** The script
        sees a global `args` and answers with the prompt (returned string or
        the global `prompt`) on a fresh VM with the 3-second deadline — a
        hanging template fails with a message, not with a frozen editor. Its
        output is the final prompt: post-processing would be rewriting what
        the script deliberately wrote.
- [x] Ghost completion → a fill-in-middle prompt for code models
  (qwen-coder and kin can do it); the debounce follows typing speed.
  - [x] **The suffix is what the caret already sits inside.** `ghostContext`
        splits the buffer at the caret — 8 lines before, the remainder of the
        line and the next ones after, cut at the first blank line of the
        block. A model tuned for completion gets a `PREFIX:` / `SUFFIX:`
        frame; a chat model keeps the old prose frame, because a FIM-shaped
        request handed to a model that was not trained on one is worse than
        the sentence that works everywhere. The name decides: any tag holding
        *coder*/*codestral*/... gets the split — and *deepseek-chat* does not,
        which is exactly the trap a looser `deepseek-` prefix would have
        walked into.
  - [x] **The window reads the keystroke rhythm.** Every typed character and
        every Enter restarts one clock (a generation-stamped `tea.Tick` like
        every other async subsystem's, so a stale tick cannot fire); the
        median gap of the last 8 edits picks 750 / 450 / 250 ms — a fast
        typist gets one suggestion when they pause, a slow one per stop, a
        burst costs the model nothing. Streaming deltas carry the generation:
        typing past a half-arrived suggestion drops it.
  - [x] **`[ai] ghost_auto`** (default on) is the switch; off puts requests
        back behind Enter and the manual Alt+G, which now also abandons the
        counting window — one chord, one request.
- [x] Usage: token/cost cards in the chat and the agent panel, quick model
  switching from the palette.
  - [x] **The providers finally answered.** `ai.Handler.Usage` fires once per
        stream: Ollama counts ride its final chunk, OpenAI-compatible servers
        send them in a usage-only chunk after `finish_reason` — which meant
        the client could not return at the finish marker anymore, and the
        request must ask with `stream_options.include_usage`. A server that
        reports nothing leaves the field empty: unknown is not zero, and no
        card is drawn for an estimate nobody measured. No cost column: dmed
        has no price source it could state honestly, so the cards show tokens.
  - [x] **Cards key on the message they measured.** The report rides the
        assistant message (`ai.Message.Usage` — a display and history field,
        never serialized to the providers, which build their wire form from
        selected fields), so a rebuilt transcript and a thread loaded from
        `.dmed_chat.json` show the same card. A tool-round turn adds the
        rounds: every round re-sends — and re-bills — the whole context, and
        the card is the cost of the turn. The agent panel shows the same on
        its rows and persists them with the task store; a sub-agent's total
        is the sum of its rounds for the same reason.
  - [x] **Palette → "AI: Switch Model…"**: the provider's list opens as a
        bottom panel in the outline's shape (j/k/Enter/Esc), and the pick
        lands on the chat, on `[ai] model` and on the cached provider through
        the wizard's own save — one switch cannot leave the config and the
        session at odds. The fetch is generation-stamped, an error answers on
        the status bar instead of an empty box, and picking the already-active
        model rewrites no file.
- Park UI: the approval queue already works, but the different kinds
  (file approval, ASK_USER question, waiting for a sub-agent) deserve to
  be one predictable flow with a shared navigation stack.

### M13 — configuration and experience
- Rebindable keys: a `[keys]` section of `action = key`.
  - [x] **The action has a name.** An entry of `globalBindings` / `editorBindings`
        is a `bindingDef{act, fn, flags}` now: the chord stays the map key and the
        action id (`keyAct`, `"file.save"`) lives in the value, so the two entries
        that always meant the same command (`f1` and `ctrl+e`) are one identity and
        an alias set is a fact read off the tables instead of a coincidence. The
        model owns the resolved map (`KeyMap` in `keymap.go`), the package tables
        stay immutable defaults, and `alt+1..9` became nine real `tab.jump` entries
        rather than a chord the dispatcher parsed. The chords that other machinery
        depends on — Esc, Enter, Tab, the movement keys, `ctrl+q`, the `y`/`n` the
        agent review answers — are flagged `protected` and refuse a rebind.
  - [x] **The help is generated.** `helpRows()` builds the F1 cheatsheet from the
        resolved map, so a rebind repaints the help and a stale copy of a chord is
        no longer possible (`dmed --dump-keys md|roff` prints the same list for a
        document). Nine rows whose shape the tables cannot express — the mouse, the
        debug stepping keys, the pane's own letters — name their keys by hand, and
        `TestHelpRowsAreTheTables` fails if a generated row ever shows a chord that
        is not bound. The stale tree row, whose i18n key existed in neither catalog,
        is gone.
  - [x] **`[keys]` overrides.** `action = chord[, chord…]` in `.dmed.conf`: a list
        value is the alias set, an empty value unbinds, and `ResolveKeys` joins the
        section to the defaults on one model-owned map, so a hot reload re-keys the
        running editor and macro playback follows it for free (a replay feeds the
        same `dispatchKey`). The resolver refuses, rather than half-applying, the
        four lines that would leave the user holding a chord that does nothing: an
        unknown action, a chord that cannot arrive as written (`shift+g` *is* `G`,
        and `ctrl+shift+f5` must be spelled in the order the key names are built), a
        protected target, and a chord an action *outside* the section still owns.
        Applying is two passes — every named action is cleared, then the new chords
        are written — because one pass cannot express a swap: `file.save = ctrl+k`
        would be refused over a Ctrl+K that `lsp.hover = ctrl+s` had not released
        yet. Because one chord can only be claimed once, the lines are applied in
        sorted order: two actions asking for the same key produce the same map every
        run, and the loser is the one that reports. Refusals land on the status bar
        instead of in a log nobody reads, and `dmed --dump-keys actions` prints the
        names to write — which the resolver must accept as input, a test feeds the
        whole list back to prove. That property cost one carve-out: F6 and F7 ship
        in *both* tables under different actions, because the global one declines
        outside the debug panel, so a chord with two shipped owners is a restatement
        rather than a claim, and either half naming it keeps both instead of
        silently unbinding the other. `alt+1..9` keeps its meaning as sugar in the
        value, not as a case in the dispatcher. Proven in the shipped binary by the
        `tests/tui`
        harness, which also found the pty bug that hid it: ConPTY is handed an env
        block whose repeated names Windows resolves to the *first* entry, so the
        harness's `HOME=<tempdir>` was shadowed by the developer's own and no
        scenario had ever read a config file it wrote (`ptyterm.mergeEnv`).
  - [x] **The panels dispatch on commands too.** An overlay that owns the keyboard
        asks `m.paneCmd("<pane>", s)` which of its commands the chord runs and
        matches on the answer, so a `[keys]` line reaches the find prompt, the tree,
        every review and the debugger: 54 panels, 333 commands, each declared once in
        `paneCommands` (`panemap.go`) beside the chords it ships with. The handlers
        dispatch on a *name* rather than the closure tables the plan sketched, because
        the arm bodies then never move and the move is checkable: the generator that
        rewrote them (`panegen`, `-mode verify`) compares the chord→arm graph of the
        HEAD copy with the result and says so per panel, which a hand-written
        40-handler refactor cannot. Three properties the migration had to earn:
        a command the user switched off is answered by `paneUnbound` ahead of the
        default arm, or Backspace without a Backspace becomes a character typed into
        the field it was just removed from; a panel command never shares a name with a
        table action, since `find.next` and the text pane's `search.next` naming one
        chord would hand Enter to the buffer the first time someone rebound it
        (`TestPaneCommandsAreWellFormed` keeps them apart); and a panel's protection is
        per *command* — its escape hatch, the `y`/`n` a review answers with — where the
        tables protect a per-*chord* set. The chat retry, which answers `r` and then
        falls through to type it, forced the sentinel to the top of the switch: a
        `fallthrough` lands on the clause that physically follows, not on the one that
        means the same.
        What stays literal on purpose: the ghost-text Tab/Esc inside
        `handleTailBindings` (it must fall through to normal editing, which is what its
        `default` arm is for), the git panel's `switch keyL` family (it folds case, so a
        declared `G` could never fire and the `[keys]` line naming it would be a lie),
        and `terminalKeyBytes`, which encodes bytes for a shell rather than commands for
        a panel.
- Themes: user palettes for syntax_theme in `.dmed.conf` or a separate
  file, live preview in the wizard.
  - [x] **`[theme]` recolours one token at a time.** The section sits over
        `[editor] syntax_theme` in the same `.dmed.conf`, and the preview lives in
        its own panel (palette → *Theme: Syntax Colours…*) rather than in the AI
        settings wizard, which is a form about a server. A separate file was
        rejected on the way: this is one more block of the INI the watcher already
        reads, and a second file would need precedence rules of its own against a
        project config that already beats the global one.
  - [x] **The grammar is chroma's, not an invented one.** `Override` *is* a
        `chroma.StyleEntry` and a rule is parsed by chroma, so `#ff79c6
        bold bg:#101014`, `nobold` and `bg:` alone (clears) mean what the Pygments
        docs say and a short hex is padded rather than refused. The merge happens
        *after* the style resolves a token through its parents, which is the only
        reason `generic.strong = nobold` can outvote a bold monokai inherited. One
        name has three spellings a user may have met (`KeywordConstant`,
        `keyword.constant`, `keyword_constant`) and all three reach one token;
        `dmed --dump-tokens` — the list a refusal message points at — prints both
        halves, the usable names and the HTML/SVG wrappers no terminal paints,
        refused with the reason rather than cached as an unread colour. That list
        also caught a pre-existing bug: `styles.Get` answers an unknown style with
        monokai but never returns nil, so the shipped fallback was dead code and the
        editor reported a name nobody typed; the name is looked up in the registry
        now.
  - [x] **The preview waits for nothing, so nothing is written by a keystroke.**
        The panel is synchronous — there is no probe to run, and with it none of the
        documented trap about a pick path starting a `tea.Cmd` it cannot return: a
        fixed Go sample is painted from the rows as they stand, and typing a readable
        colour previews it before it is staged. Staging is the panel's own map, so
        `Esc` leaves the file byte-for-byte as it was and `Ctrl+S` is the only write.
        A save swaps in a new `Highlighter` and drops nothing from the per-tab caches:
        they hold token *kinds* and the style of a kind is asked per painted run, so
        recolouring a file does not re-tokenize it — the same is true of a hot reload
        of the config from outside, which is the other half of the claim.
        Refused lines are counted on the status bar both at startup and on every
        reload, and repeated inside the panel that exists to explain them.
  - [x] **The highlighter stayed the model's.** `syntaxFor(cfg)` is the one place a
        config becomes colours, called by `New()` and by the `.dmed.conf` branch of
        the watcher; the overrides are written once at construction and read without
        the cache lock, because a package-level default highlighter is exactly the
        global mutable the rules forbid (the history of `SetDefault` in the Update
        loop is why `syntax.New` has no default at all).
- `.editorconfig`: indent_style/size, trim trailing whitespace, final newline.
  - [x] **The file answers for one file.** `config.LoadEditorConfig(root, path,
        def)` walks from the file's directory up to the project root — nearest
        file wins, last matching section wins, `root = true` ends the walk — and
        `FileOpts` carries what dmed can act on: `tab_width`, `indent_size`,
        `indent_style`, `trim_trailing_whitespace`, `insert_final_newline`. The
        caller's defaults fill every key the file leaves unset, and the two
        widths default *each other* in both directions, since the spec says a
        space file's level is `tab_width` when `indent_size` is absent and a tab
        file's tabs are `indent_size` columns wide. The glob is the editorconfig
        subset (`* ? [seq] ** {a,b} {1..9}`, backslash escapes) compiled in
        `ecglob.go` and deliberately *not* shared with `globMatchSegments`, which
        is a different grammar with no braces; sharing it would quietly change
        what the agent's GLOB tool matches. Matching is case-insensitive on
        Windows and macOS only, and a pattern with no `/` is matched against the
        basename, which is why `[Makefile]` works. A pattern that will not
        compile matches nothing rather than everything: a broken rule must not
        reach files it never mentioned.
  - [x] **Every column calculation agrees with the file.** The ~20 reads of
        `cfg.Editor.TabWidth` became `m.tabStop(t)`, and the `"\t"` literals in
        the indent commands became `m.indentUnit(t)`. The buffer needed no new
        API for that — `IndentLines` already takes the unit as a string, so
        `internal/buffer` stayed both TUI-free and unit-agnostic. The result is
        cached on the tab keyed by its *path* rather than a "known" flag, because
        Save As moves a file into another directory and the old rules must not
        follow it; a changed `.editorconfig` drops every cache from the watcher
        branch, and the wrap and fold caches need nothing because both are keyed
        on the tab stop they were built with. A Tab still enters one rune at a
        time, so it joins the typing run it sits in instead of becoming its own
        undo step.
  - [x] **Save writes what the file asked for.** One `textForDisk` for Ctrl+S
        and Save As both: the trim is the buffer's own `TrimTrailing` — so it is
        one undo step that puts the spaces back, rather than bytes that vanished
        with no way home — then the final newline, then the line endings the file
        already used. The end-of-file newline is stripped before CRLF is written,
        not after, or a file with `insert_final_newline = false` would end in a
        bare carriage return.
  - [x] **An agent proposes the bytes that land.** The `.editorconfig` save
        policy is applied where a `Change` is *created* (`toolEnv.finishFile`,
        wired for the chat and for a background sub-agent), so the diff a human
        reviews is the file that appears on disk. Applying it inside the Applier
        after approval was rejected on purpose: a post-approval rewrite makes the
        reviewed diff a lie. Only the two whitespace rules run there — converting
        the indent unit would rewrite every line of a whole-file proposal,
        including the lines the model did not touch. `Change.Orig` stays the raw
        bytes, because that is what `Applier.Validate` compares against the disk,
        and a trimmed `Orig` would be a snapshot of a file that never existed.
  - Not honoured, and why: `end_of_line` and `charset` (dmed reads and writes
        UTF-8 and re-writes the endings a file already uses, so a rule would only
        be able to fight that), `max_line_length` (the buffer is rune-based and
        the wrap is a display property), `tab_width` of a file no tab holds and
        Replace in Project's own bytes (its contract is exact-occurrence
        replacement across however many files, and appending a newline to 200 of
        them is a change nobody reviewed). A file outside the project root is
        still served: the walk upward has no boundary there, exactly as the
        spec's does, and stops at the first `root = true`.
- [x] TUI regression harness in CI: `tests/tui` (Go, no Python) spawns the built
      binary in a real pseudo-terminal through the editor's own `internal/ptyterm`
      — ConPTY on Windows, creack/pty on Linux — types into it, and asserts on the
      frames read back from vt10x plus the files left on disk. Typing and saving,
      the fuzzy finder, the command palette, a docked shell round-trip and
      "Ctrl+C never kills the editor". `make tui`, one CI step in each of the two
      test jobs, `DMED_BIN` reusing the binary CI already built; a runner without a
      working pty skips instead of failing.
      What a pty cannot carry stays manual: alt chords (ConPTY eats `ESC`+letter),
      F-keys and three-modifier chords — see the "Verifying the TUI" section of
      AGENTS.md.
- [x] **Plugins: bus events, async API, subprocess spawn** — the
      `buffer:modified`/`file:changed`/`agent:updated` events from
      `internal/events` exposed to Lua, an async delivery (callbacks instead
      of blocking), and `dmed.spawn` with a timeout. The three events queue
      in the plugin manager (`Mgr.PostEvent`, safe from any goroutine) and
      reach Lua through one wake channel on the UI thread (`Drain`), so a
      handler never runs on the goroutine that published and typing never
      waits for Lua; handlers are resolved at delivery time, so a hot-reload
      between queue and delivery cannot run a handler into a closed VM. The
      `buffer:modified` publisher is a per-frame compare of the buffer's
      change counter (`buffer.Changes`, bumped by `pushUndo` and by `Undo`,
      which swaps root pointers off the pushUndo path), replacing an O(n)
      full-text compare per keystroke. `dmed.spawn` runs a subprocess under a
      wall-clock deadline (default 10 s, clamped 100 ms–10 min), streams
      stdout/stderr through a 1 MB cap and delivers
      `{code=, out=, err=, timeout=}` to the callback through the same pump.
      The remaining editor events (`ready`, `file_open`, `save`) are
      synchronous as before, being the places plugins hook directly into the
      UI flow.

## Tech debt left by the October audit

The 2026-10-01 audit plan (`docs/IMPROVEMENT-PLAN.md`, v0.8.3, rev `1b0c9b3`)
is closed; both it and the full breakdown (`docs/AUDIT-2026-10-01.md`, last
present at rev `a86f2da` — `git show a86f2da:docs/AUDIT-2026-10-01.md`) were
removed in 2026-11 after folding the open items here. The snapshot was UTF-16,
so no viewer ever rendered it, and closed findings rot faster than they get
reread.

- [x] **Split `editor.go` + a binding table.** `handleKey` was ~565 lines and
      hundreds of `case`s. It is now `keys.go`: one `keyOf(msg)` that returns
      the normalized key name (control-byte re-encoding, Cyrillic→QWERTY, the
      bundled-Text chords that `String()` misreports on the Windows Console
      API) plus two tables — `globalBindings` (chords that answer before any
      pane: quit, copy, panels, debugging) and `editorBindings` (the text
      pane) — with `dispatchPanes` for the focused pane and `typingFallback`
      for the buffer. A binding returns `(cmd, handled)`, which is how F6/F7
      stay step-over inside the debug panel and a split outside it. The rest
      of the file split along the AGENTS.md boundaries: `tabs.go`,
      `prompt.go`, `search.go`, `session.go` (editor.go went 3402 → 1935
      lines). The mechanical move was verified line-exact against `HEAD`, and
      `keys_binding_test.go` is the payoff: table well-formedness, the
      fall-through rules and ~30 chords asserted without a terminal.
      Follow-up, deliberately kept out of that behavior-preserving move, is
      done too: the pane handlers no longer translate keys themselves. All 44
      of them take the name `keyOf` produced (`dispatchPanes(s, msg)` →
      `handleX(s, msg)`), `gitKeyName` and its duplicate ЙЦУКЕН table are gone,
      and the ~39 `msg.String()` matches are gone with them. `keyOf` absorbed
      what the private helper knew: the physical key as a fallback for layouts
      `ruToEn` cannot translate, and names built from the key code when the
      terminal put a raw control byte in `Text` — Enter/Esc/Tab/Backspace
      arriving as `"\r"`/`"\x1b"`/`"\t"`/`"\x7f"` matched no binding anywhere,
      and a letter command (`q` in the diff view, `s` in the SQLite schema,
      `j`/`k` over search hits) was silently dead in a Cyrillic layout. Text
      entry still reads `msg.Text`, so the character typed is the character
      that lands in the query.
- [x] **Test coverage baseline.** Measured and recorded: total 70.5%
      (`make cover`), editor 68.9%, buffer 78.6%, agent 90.3%, ai 84.3%,
      config 75.1%, dap 43.3% (the real-adapter integration tests skip
      without Delve), and 0% for `main`, `internal/setup`, `internal/bundled`.
      `make cover-check` runs the suite and fails below baseline − 1; the CI
      `vet + test` job now runs it in place of the plain `make test`. Pure
      render/geometry functions got the first batch of the cheap-win tests
      (`view_geometry_test.go`: `wrapLine`, `segRowForCol`, `expandTabs`,
      `visCol`, `padTo`, `fitPath`, `blameLabel`, `relWhen`, `viewHeight`).

## Infrastructure: CI and releases

- [x] GitHub Actions CI (`ci.yml`): vet + unit tests + cross-build for 8 platforms
      on every push to `main` and every PR.
- [x] Auto-release (`release.yml`): on a `v*` tag push it automatically
      - builds binaries for 8 platforms (with the version from the tag),
      - builds packages in distro containers: `.deb` (Ubuntu), `.rpm` (Fedora),
        `.pkg.tar.zst` (Arch, makepkg under a non-root user),
        Termux `.deb`, Windows `.zip`,
      - publishes a GitHub Release with all artifacts and auto-generated notes.
- [ ] Publishing packages to external repositories (AUR / COPR / PPA) — not needed yet.

## Development rule

M0–M1 are done without any AI: first a solid editor core,
then integration. The weak spot of such projects is the editor, not the LLM.

## Open questions

- [x] UI i18n (en/ru) — done (`internal/i18n`); the catalogs will keep
      changing as the UI changes.
