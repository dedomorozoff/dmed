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
      Docs in `docs/PLUGINS.md`. Remaining: more events/API.
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
- [x] **Find in Project** (`Ctrl+Shift+F`, палитра "Edit: Find in Project..."):
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
- [x] **LSP rename + code actions** (`Ctrl+Alt+R` / `Ctrl+Alt+Q`, палитра
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
      the palette.
      Arrows
      and PgUp/PgDn skip over hidden lines so the cursor can never enter a
      folded block; a cursor that ends up on a hidden line (undo, an external
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
