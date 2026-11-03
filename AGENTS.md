# AGENTS.md

Guidance for AI coding agents working on this repository.

## Project

dmed is a terminal code editor (Bubbletea/TUI in Go) with AI agents as
first-class participants. It is well past the original "single-file skeleton":
milestones M0–M7 of [ROADMAP.md](ROADMAP.md) are done (version 0.9.x) — the
editor has splits, multi-cursor, rope-based buffers, syntax highlighting,
file watching, git integration, AI chat/inline/ghost, a background agent
queue with diff-review, LSP client (diagnostics, definition, rename, code
actions, formatting), autocompletion, Lua plugins, an AI
onboarding wizard, DAP debugging (Go/Delve by default, any DAP adapter
wired through `[debug]` config), a persistent terminal, a SQLite browser and
a debugger panel. Update ROADMAP checkboxes when you complete
work.

The architectural rule of the project — now implemented, not aspirational —
is that agent-proposed changes always pass through **diff review → atomic
apply** before touching buffers or files. Agents never write to a buffer
directly; they emit `Change`s that land through `internal/agent.Applier`
(see below). Keep this rule for all future AI-related code.

## Toolchain gotchas (this machine)

- Use plain `go` from PATH (1.26.0, installed at `D:\go\bin`). No special
  path needed; the old `/usr/lib/go/bin/go` (1.22) is gone. `go.mod` requires
  `go 1.26.0`.
- A stale `GOROOT` used to be exported pointing at Go 1.15; it is clean now,
  but the Makefile still does `GOROOT :=` plus a value-less `export GOROOT`
  as insurance — leave it. The exports and phony lists are written in a form
  that works under both GNU make and FreeBSD's bmake.
- Network fetches need explicit `GOPROXY=https://proxy.golang.org,direct`
  and `GOSUMDB=sum.golang.org` (system defaults are wiped). The Makefile and
  `.github/workflows/ci.yml` both set these.
- Dependencies live on `charm.land` (bubbletea **v2**, lipgloss **v2**):
  `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`. Other notable deps:
  chroma/v2 (syntax), go-git/v5 (git), fsnotify (watching), atotto/clipboard
  (system clipboard), sergi/go-diff (diffs), yuin/gopher-lua (plugins).
  These are pinned in `go.mod`; only bump deliberately and re-run
  `make build && make vet && make test`.

## Commands

```sh
make build          # ./dmed binary
make test           # unit suite (the TUI harness excluded, see below)
make tui            # tests/tui: real pty, real frames (DMED_BIN names the binary)
make vet            # go vet ./...
make cover          # the unit suite with a coverage profile + total
make cover-check    # the suite, then fail below COVERAGE_BASELINE - 1
make run FILE=path  # build + run editor on a file
```

Run `make build && make vet && make test` before considering any change done.
`make tui` is slower — it spawns the editor in a pseudo-terminal — so it runs as
its own CI step rather than inside the loop above; `go test ./...` still reaches
it when you want everything.

CI (`make vet` + `make cover-check` + `make tui` + native build + 8-platform
cross-build) runs on every push to `main`/`master` and every PR; keep it green.
`COVERAGE_BASELINE` in the Makefile is the recorded total — raise it when
coverage goes up, and only lower it deliberately.

## Architecture

The code is split into focused internal packages:

- `internal/buffer/` — pure text buffer. Rope-based document
  (`buffer.go`, `doc.go`, `internal/rope`) with cursor, sticky goal column,
  undo/redo with typing-run grouping, multi-cursor support
  (`multicursor.go`), the delimiter matcher (`match.go`: the pair highlight, the
  jump target and the inside-pair range — brackets by depth, quotes by line
  parity, escapes skipped, every walk bounded by `maxScanLines`, no TUI imports
  and no re-tokenizing) and the selection transformations
  (`transform.go`: case fold, block sort, trailing trim, indent alignment,
  list numbering). Line-range operations — these plus `IndentLines`,
  `ToggleComment`, `MoveLineUp/Down` — all take the same shape: `beginChange` +
  `pushUndo`, `linesCopy`, rewrite `mat[from..to]`, `setLines`, then clamp the
  caret and the selection anchor (`commitRange`). One undo step each, and an
  operation that changed nothing pushes nothing, so `Ctrl+Z` never "undoes" a
  no-op. Never rewrite text by replacing the buffer with `buffer.Load`: that
  drops the undo history, the caret and the selection along with the rope.
  A command that already knows both ends of a selection sets them with
  `SelectRange`, never `DragSelect`: the latter re-clamps the column through the
  sticky goal column and silently moves the end it was handed.
  Must stay free of TUI imports; behavior changes belong
  in the corresponding `_test.go`. Undo grouping: consecutive inserts at
  adjacent positions form one undo step; any other operation breaks the group
  (`beginChange`/`breakGroup`). `pushUndo` is also the only place that learns
  where text last changed: it stores the caret its snapshot takes, and `LastEdit`
  reports it, so every edit shape — typing runs, the line commands, the
  transformations, the multi-cursor edits, redo — is answered without a second
  implementation of "did this change text".
- `internal/editor/` — Bubbletea model and UI. `editor.go` holds the model;
  key handling is in `keys.go`: `keyOf` produces the one normalized key name
  bindings are matched on (control-byte re-encoding, Cyrillic→QWERTY, the
  physical key for layouts that table cannot translate, special keys named
  from the code when `Text` carries a raw control byte, bundled Text chords),
  then `globalBindings` (chords that always answer first: quit, copy, panels,
  debugging) → `dispatchPanes` (the pane/overlay that owns the keyboard) →
  `editorBindings` (the text pane) → `typingFallback`. An entry is a
  `bindingDef{act, fn, flags}`: the chord is the map key and the action id
  (`keyAct`, `"file.save"`) is in the value, so aliases are one identity, the
  F1 cheatsheet is generated from the tables (`keydoc.go`, `dmed --dump-keys
  md|roff|actions`) and a `[keys]` section addresses the command rather than the
  keystroke. `keymap.go`'s `ResolveKeys(cfg)` joins that section to the defaults
  into the map one model reads (`m.keys`, re-set on a `.dmed.conf` reload, so a
  remap re-keys macro playback too — a replay feeds the same `dispatchKey`); the
  package tables are immutable defaults, never a state. A resolver refuses a line
  rather than half-applying it and the refusals are reported on the status bar: a
  silent non-move is the failure mode nobody notices. Applying is two passes —
  every named action is cleared, then its chords are written — because one pass
  cannot express a swap. Two of the refusals are structural: a chord is protected
  (Esc, Enter, Tab, the movement keys, `ctrl+q`, the review `y`/`n`) or another
  action already owns it, *in either table*, and because a chord can only be
  claimed once the lines are applied in sorted order so a conflict resolves the
  same way every run. The claim that is *not* a conflict is a chord shipped in both
  tables under two actions — F6 and F7, the debug step-over and the split — which
  either owner may restate without taking the other's half away: the fallthrough
  between them is machinery rather than a preference, and it is what lets
  `dmed --dump-keys actions` round-trip back through the resolver unchanged (a test
  feeds the whole list in). `type binding func(m *Model, msg tea.KeyPressMsg) (tea.Cmd, bool)`;
  a `handled == false` return falls through to
  the next stage (this is how f6/f7 mean step-over in the debug panel and split
  outside it). Every pane handler takes
  that name as `handleX(s string, msg tea.KeyPressMsg)` and matches on `s`,
  never on `msg.String()`; text entry reads `msg.Text`, which `keyOf` hands
  back unrestored, so a typed character is never replaced by its binding name.
  A new binding is a table entry, not a `case` in a switch; a chord that means
  different things in different states returns `handled == false` to fall
  through to the next stage. The rest is split by concern: `view.go`
  (rendering), `tabs.go` (open/close/switch/save/quit confirmation, and the MRU
  `Ctrl+Tab` cycle over the pane's recency order — the walk freezes the order it
  derived on the first press and focuses through `setPaneTabQuiet`, because a walk
  that recorded each tab it left would reorder the list under itself and ping-pong
  between two tabs),
  `prompt.go` (bottom prompts, including go-to-line), `search.go` (in-file
  find/replace), `projectsearch.go` (Find in Project: the streamed scan, the
  generation stamp and the hit panel), `projectreplace.go` (Replace in
  Project: the human's batch edit — a per-file preview, a side-by-side diff
  review and an atomic `agent.Applier` apply, with files an open tab still
  holds unsaved and non-UTF-8 files listed as excluded instead of written),
  `mergeconflict.go` (merge conflicts: the strict marker scan — column 0,
  exactly seven characters, the diff3 `|||||||` base always dropped, markers that
  never close refused rather than guessed at — and the ours/theirs/both
  resolutions, each one `buffer.ReplaceSpan` in the buffer, so there is no
  overlay, no new mode and nothing written to disk; `Alt+[` / `Alt+]` navigate
  conflicts while the file has any and fall back to Git hunks after the last
  one. `conflictOpen`/`handleConflict` is the watcher's external-change reload
  prompt, not this feature),
  `session.go` (save/restore of the session),
  `finder.go` (fuzzy file finder), `split.go` (the panes and their layout; a
  `pane` holds the tab index, the scroll offsets, word wrap, the two jump-list
  stacks — positions named by path, not by tab index, which every close
  renumbers — and the tab recency list the MRU cycle walks, which is index-based
  on purpose: unlike a back step it must never reopen a tab the user closed, and
  the untitled buffer has no path to resolve. `setActiveTab` is the one focus
  choke point that records it and `fixPaneTabsAfterClose` the one place that
  renumbers both), `tree.go`, `mouse.go`,
  `completion.go`, `palette.go`, `terminal.go`, `chat.go`
  (AI chat), `ai_inline.go` (inline rewrite → diff review), `ghost.go`
  (Copilot-style ghost text), `agent_panel.go` (agent task queue UI),
  `git_panel.go`, `diffview.go`, `sqlite_view.go` (SQLite browser: table
  strip + paged row grid, immediate cell edits through `internal/sqlitedb`),
  `imageview.go` (picture tabs: ASCII/half-block render via
  `internal/imgprev`, empty buffer + decoded frames, read-only key
  handling, scroll, GIF tick chain), `plugins.go`, `plugin_store.go`, `lsp.go`,
  `ai_settings.go`, `transform.go` (the selection-transformation commands pick
  the target — selection lines, or the whole buffer for a trim — and the
  status-bar message; the text work is the buffer's), `delimiter.go` (the
  bracket jump and select-inside commands: the same thin shape, and pure caret
  moves — no edit, no undo step, no dirty flag; the scanning is the buffer's),
  `jumplist.go` (the jump-list commands: `Ctrl+Alt+←`/`→` walk the pane's two
  stacks. `pushJump` is called by the discrete jumps — the definition reply,
  a project-search hit, go-to-line, the debugger's stop line and
  `lastedit.go`'s go-to-last-change — and in the handler
  that moves the caret, not where the request started, because the reply is async.
  Nothing else records a position: arrows, clicks, the in-file find moves and the
  chords that already walk back (`Alt+N`, `Alt+[`/`]`, `Ctrl+]`) would flood the
  history, and a tab switch is a non-producer on purpose — `Ctrl+Alt+←` answers
  *where the caret was*, `Ctrl+Tab` *which tab was in front*. Like the bracket
  commands these are caret and focus moves only),
  `lastedit.go` (`Ctrl+Shift+Backspace` lands the caret where this file was last
  changed: the position the buffer's `pushUndo` recorded, so no edit shape needs
  its own bookkeeping. Per file, never cross-file — the jump list already carries
  the caret across files. Both refusals (a file never edited, a caret already on
  the change) move nothing and push nothing, so a repeated press cannot spend a
  jump slot on a no-op),
  `fold.go` (the indentation fold ranges and the display-row list the whole
  pane renders from. The fold commands act on the block the cursor *belongs
  to* — `foldTargetAt`: the block starting on its line, the innermost one
  around it (the closing brace included, although the scan leaves that line
  visible), or the block whose lone brace sits on the line below an Allman
  signature. Folding from inside must then move the caret onto the block's
  start line: `clampScroll` forces any block hiding the caret open again, so a
  caret left inside would undo the fold it just made),
  `tools.go` (agent tools:
  READ/SEARCH/LIST_DIR/GLOB/RUN/REPLACE/EDIT), `tools_fs.go` (shared project
  walk plus the LIST_DIR/GLOB tools), `tools_core.go` (`toolEnv`: the TUI-free
  tool bodies shared by the chat and by background sub-agents), `tools_plan.go`
  (TODO_* / ASK_USER / SWITCH_MODE tools and the question overlay),
  `tools_web.go` (WEB_SEARCH plus the plan/act modes),
  `tools_subagent.go` (SUB_AGENT: the tool, the tool surface a sub-agent gets,
  and the park that waits for it),
  `dap.go` (DAP session lifecycle: async adapter start, launch/attach,
  breakpoints, continue/step, panel state), `dap_panel.go`
  (threads/stack/variables/console panel rendering).
- `internal/agent/` — background AI agents (the project-rule core).
  `queue.go` — thread-safe task queue with progress/cancel; `runner.go` runs
  tasks; `apply.go` is the **atomic Applier**: it validates that every
  `Change.Orig` still matches on-disk content (stale patches reject the whole
  series) and writes all-or-nothing with rollback; `commit.go` turns an
  approved, applied series into a single git commit. See `model.go` for the
  `Task`/`Change`/`Status` lifecycle. `subagent.go` adds a delegated task
  (`Task.Kind = sub`, `Queue.EnqueueSub`): its own tool loop driven by a
  `ToolExecutor` the editor supplies, reviewed through the very same panel and
  Applier, so delegation cannot become a back door around review.
- `internal/ai/` — LLM providers: `provider.go` interface, `ollama.go`
  (local), `openai.go` (OpenAI-compatible, SSE streaming). `[ai]` config.
  `Config.APIPath`/`ModelsPath` exist because "OpenAI-compatible" does not mean
  `/v1`: Pollinations serves the same protocol under `/openai` and lists models
  at `/models`. Anything adding a provider preset must carry those paths into
  both `NewProvider` and `WriteAI`, or the probe will 404 and be reported as a
  connection failure.
- `internal/config/` — INI `.dmed.conf` loader (`[editor]`, `[ai]`, `[agent]`,
  `[ui]`, `[plugins]`, `[debug]`, `[lsp]`, `[keys]`); priority defaults < global <
  project < env vars; hot-reload on save; `WriteAI`/`WriteLang` merge helpers.
  `[keys]` maps an *action name* to chords and config deliberately does not check
  the spelling — only the editor knows which keys exist, so `ResolveKeys` validates
  and reports. An unknown key in a section with an open-ended grammar (`lang.<id>`,
  an action) is kept, not dropped.
  `WriteAI` deliberately does not write `tools_enabled`/`tools_disabled`: they
  are a hand-curated list and the wizard must not clobber it.
  `AIConfig.Unconfigured()` is the gate for the keyless fallback: only a
  genuinely untouched configuration may reach for a public provider, never one
  the user pointed at their own server.
- `internal/dap/` — own DAP client (no external deps): Content-Length framed
  JSON-RPC 2.0 over any stream, stdio and reverse-connect (`--client-addr`)
  launchers, initialize/launch/configurationDone, breakpoints,
  continue/next/stepIn/stepOut, threads/stackTrace/scopes/variables/evaluate,
  and event dispatch. The transport and protocol are adapter-agnostic; the
  Go/Delve wiring lives in `internal/editor/dap.go`.
- `internal/dbgp/` — own DBGp client for engines that do not speak DAP
  (Xdebug/PHP): NUL-framed XML over TCP, the `<init>` handshake, breakpoints,
  run/step, stack/context/property variables, and eval, all exposed through the
  same method surface as `internal/dap.Client` so the existing panel drives it.
  Wire details that each cost a real bug and are easy to get wrong: every
  engine → IDE document is framed `<byte length>\0<xml>\0`, documents declare
  `iso-8859-1`, `breakpoint_set` returns its id as a response attribute,
  failures arrive as an `<error>` child with no `success="0"`, and the `eval`
  expression must be sent base64-encoded. `TestRealXdebugSession` drives a real
  interpreter and skips when PHP/Xdebug is absent — keep it, the mock engine
  cannot catch this class of drift. Set `DMED_DBGP_DEBUG=1` to trace the wire.
- `internal/lsp/` — JSON-RPC 2.0 LSP client over stdin/stdout (diagnostics,
  definition, rename, code actions, formatting via textDocument/formatting,
  didOpen/didChange); wired into autocompletion, the gutter, the palette and
  the Ctrl+Alt+Q/R/F chords. Reply shapes differ per method: rename and code
  actions return WorkspaceEdit maps, formatting a bare TextEdit array —
  `parseWorkspaceEdit` and `parseFormatEdits` cover them separately.
- `internal/sqlitedb/` — TUI-free SQLite data layer behind the browser
  (`modernc.org/sqlite`, pure Go — no CGO, the 8-platform cross build must
  stay green). `IsSQLite` checks the magic header (opening a candidate file
  never creates one); identifiers are double-quoted with "" escaping and
  user values always go in as bound parameters — never concatenate user text
  into SQL. Editing is immediate (one parameterized UPDATE/INSERT/DELETE per
  action); views and WITHOUT ROWID tables are read-only (no rowid address).
  A `.db`/`.sqlite` extension only earns the magic peek in `openPath`: the
  header decides.
- `internal/imgprev/` — TUI-free ASCII terminal preview of images (no TUI
  imports): decodes png/jpeg/gif, renders half-blocks (`▀`, two pixels per
  cell, box-average samples, terminal colour profile via charmbracelet, ASCII
  ramp fallback), frames an animation through `Animation.FrameDelay`, caps
  frames at MaxFrames. Consumed by the editor's picture tabs.
- `internal/plugin/` — gopher-lua plugin framework (`dmed.*` API); plugins
  load from `~/.dmed/plugins` and `<project>/.dmed/plugins` with hot-reload.
- `internal/vcs/` — git via go-git (pure Go, no system git needed): status in
  the gutter, hunks, side-by-side diffs, stage/unstage, commit, history,
  branches, and `RevertCommit`/`LastCommitWithSubject` — the refusal-safe
  revert the editor's agent undo is built on (see Conventions). Paths returned
  by `RevertCommit` are worktree-relative; resolve them against `repo.Root`,
  not the editor's `baseDir()` (the two differ when `dmed file` runs outside
  the project). `internal/watcher/` — fsnotify external-change detection.
  `internal/events/` — pub/sub bus (`file:changed`, `buffer:modified`,
  `buffer:saved`, `git:updated`, `agent:updated`) linking buffers ← watchers
  ← agents.
- `internal/bundled/` — built-in plugins shipped with the binary (emmet,
  snippets). `internal/i18n/` — en/ru strings. `internal/session/` —
  save/restore open files across restarts. `internal/syntax/` — highlighting.
- `internal/terminal/` — the persistent built-in terminal (`internal/ptyterm/`
  underneath is the per-OS pty plumbing: ConPTY on Windows, creack/pty
  elsewhere; platform tests split the two).
- `internal/setup/` — the CLI onboarding wizard (`dmed setup-ai`); it and the
  in-editor AI preferences write the same keys through `config.WriteAI`.
- `internal/debug/` — `CapturePanicReport`: every goroutine's panic lands in
  stderr as a report instead of killing the process.
- `internal/todo/` — the model's checklist (thread-safe, no TUI): `Replace`
  from TODO_WRITE, `Merge` from TODO_SET (ticks a known step instead of
  appending a duplicate), `Render` is the single text both the model and the
  chat panel show.
- `internal/ask/` — questions the model asks the user: `Queue.Next` pushes,
  `Answer`/`Cancel` resolve by id, so a round that asks twice keeps both.
- `internal/websearch/` — the only component allowed to reach outside the
  workspace: DuckDuckGo HTML endpoint, own timeouts, `Parse` is pure so the
  fragile markup can be tested against a fixture. Opt-in (`web_search`), capped
  per session (`web_search_budget`), and when the budget is spent the tool is
  withdrawn from the model's list, not just rejected.
- `main.go` — CLI entry only (`dmed [dir | files...]`, `-h`, `-v`).
- `tests/tui/` — the regression harness: the built binary in a real pty, its
  frames read back through vt10x (`package tui_test`, test-only, so it adds no
  coverage and no dependency to the binary).

Conventions:

- Buffer state is the single source of truth; the view renders from it and
  holds no editable state of its own.
- No global mutable state; models are values, mutations happen through
  pointer receivers on small, named methods.
- Every goroutine spawned by the app runs its body under
  `debug.CapturePanicReport` (`internal/debug`), so a panic is logged to
  stderr instead of terminating the whole process with a Windows exit code 2.
- Files are stored with a trailing newline; dirty check compares against the
  normalized saved snapshot (`MarkSaved`/`Dirty`).
- Agent edits go through `internal/agent.Applier`: validate all
  `Change.Orig` against current content, then write atomically with rollback;
  an approved series becomes one git commit (`agent: <prompt first line>`).
  Do not bypass this for AI-produced edits. Replace in Project is the human's
  use of the very same pipeline (preview, diff review, `Applier.Apply`), and
  its series is deliberately *not* committed: the `agent:` subject is what
  `undoLastAgentApply` looks for, and a bulk rename is ordinary working-tree
  work.
- Chat tools return one `ToolResult` (text + optional `Change` + optional
  `Park`). Anything that needs a human decision *parks* the loop instead of
  writing a marker into the transcript: the result carries a readable
  placeholder and `resolveParkedResult` swaps in the real outcome once
  answered. Parks queue (`chatPark` active, `chatParks` waiting), so one round
  may need several approvals. A `ParkSubAgent` park is resolved by
  `pollSubAgentPark` when the queue task ends — never block the tool loop on a
  background task, it runs on the UI thread. Add new tool names to
  `legacyToolName` so the aliases models reach for keep working. Touched-file
  detection after a tool round costs two full project walks; the walks are
  gated by `toolBatchTouchesDisk` — a batch without RUN/EDIT/REPLACE walks
  nothing, keep that gate when new tools appear (a write-capable tool must be
  added there or its output will not open tabs).
- Accepted agent work is committed immediately, so the editor carries its own
  undo: `undoLastAgentApply` (palette "Agent: Undo Last Apply", `u` in the
  agent panel) reverts the newest `agent: …` commit via
  `vcs.RevertCommit` — refuse-first: a file the user edited after the apply
  aborts the whole revert, and the revert lands as its own
  `agent: revert <hash>` commit so history keeps telling the truth.
- Project walks (SEARCH/LIST_DIR/GLOB) go through `walkProject` +
  `newProjectSkip` in `internal/editor/tools_fs.go`; never re-implement the
  skip list. `globMayContain` prunes conservatively — a wrong prune hides a
  file from the model, so it only returns false when the pattern provably
  cannot match under that directory.
- Plan mode is enforced by *absence of tools*, not by a runtime check:
  `readOnlyToolDefs` removes RUN/EDIT/REPLACE/SWITCH_MODE from the request, so
  there is nothing to call. Leaving plan mode needs the human (`SWITCH_MODE`
  parks for y/n; the palette command is the human's other way in).
- Test seams must stay on the model, never in package globals: `aiFreeURLOverride`
  and `webSearchOverride` redirect the two network features to local servers so
  no unit test ever reaches the internet.
- Syntax highlighting never runs on the UI thread: `internal/editor/syntax.go`
  asks for a pass when the cached highlight no longer matches the buffer, the
  pass runs in a background goroutine and the result arrives as a
  `syntaxReadyMsg` through the `syntaxCh` channel (the waiter is armed in
  `Init` and re-arms itself). A pass for text the buffer moved away from is
  dropped; at most one pass per tab is in flight. Keep it that way — chroma
  re-tokenizes the whole buffer and blocks for seconds on a large file.
  The pass waits for the typing to settle first (`syntaxPassDelay`): the UI
  thread compares the text the wait was started for with the current one and
  restarts the wait while the user keeps typing.
- The cached highlight stores one syntax *token* per character, not a style:
  a lipgloss.Style per character cost ~800 bytes, a token 8. The styles are
  resolved through `Highlighter.StyleFor` when a line is painted (the tokens
  of a line come in runs, so the lookup happens once per run).
- The git diff of the buffer (the gutter markers) is a whole-file job too and
  runs in the background as well — see `internal/editor/gdiff.go`. It follows
  the same shape as the highlighting: a wait for the typing to settle, a
  pass that is dropped when the text moved on.
- Lines are painted in runs of equal style, not character by character: a
  lipgloss render per character was thousands of calls per frame.
- The highlighting is streamed: `syntax.HighlightLines` hands the finished
  lines to a callback while the tokenizer is still running, and the editor
  publishes them in chunks (`syntaxChunkLines`). The whole token list is
  never materialized. The language tag of a file comes from the table in
  `internal/syntax` — asking chroma for it initializes its lexer registry
  (seconds), so `syntax.Warmup` does that in the background at startup.
- The AI wizard loads the provider's model list when it opens and after every
  provider change (`testAIConnection` serves both the Test row and the list).
  `AITestResultMsg.Gen` stamps the request so a late reply from the previous
  endpoint cannot repopulate the Model row. A pinned `[ai] model` is never
  overwritten; only an empty one is auto-filled.
- DAP adapter sessions are async: the adapter is spawned in the background
  (`dapStartCmd`) and results are stamped with a session generation
  (`dapGen`). Stale events/disconnects from superseded sessions are dropped so
  they never clobber a newer live session; keep that guard if you touch the
  DAP message flow.
- New packages get focused unit tests; TUI behavior is verified by the
  `tests/tui` harness and, for chords a pty cannot carry, manually.

## Verifying the TUI

Unit tests cover the buffer core, rope, agent applier/queue/commit, AI
providers, DAP client and editor debug flow, config, LSP, plugins, git, and
editor panels. On top of them sits `tests/tui` (`make tui`, and a CI step in the
ubuntu and windows jobs): it spawns the *built* binary in a real pseudo-terminal
— the editor's own `internal/ptyterm`, so ConPTY on Windows and creack/pty
elsewhere — writes keystrokes into it and reads the painted frames back through
`vt10x`, the same emulator the terminal panel uses. Typing, saving to disk, the
finder, the palette, a docked shell round-trip and "Ctrl+C does not kill the
editor" are all asserted on frames and files rather than on model internals.

Four things this harness learned about driving a pty, all of which cost a
half-day of misreading a screen:

- The editor is not ready when the process is. Startup (plugins, session, the
  chroma registry) takes seconds, and a keystroke written before the console
  input exists is dropped, so `Start` waits for the status bar and for the frame
  to stop changing before any scenario types. Never sleep in a scenario: poll
  the screen (`WaitFor`, `WaitAbsent`, `WaitForAtLeast`, `WaitFile`).
- Static escape sequences (Enter, arrows, PgDn) and *most* control bytes
  (`Ctrl+S`, `Ctrl+K`, `Ctrl+E`) arrive exactly as written. Two things do not:
  **Alt chords**, because ConPTY's VT input parser consumes `ESC`+letter as one
  unrecognized sequence and loses it, so an alt binding cannot be driven from this
  harness — the palette reaches the same action, and the unit tests
  (`internal/editor/key*_test.go`) cover the alt bindings themselves. And **the
  control bytes a console reads as a key, not a chord**: `Ctrl+N` and `Ctrl+A`
  arrive as a bare `leftctrl`, `Ctrl+I` as `tab`, `Ctrl+J`/`Ctrl+M` as `enter`. So
  a scenario picks a ctrl chord it has seen land, and a `[keys]` test that wants a
  chord nobody else owns swaps two that work (`Ctrl+S` ⇄ `Ctrl+K`) rather than
  inventing one.
- `DMED_DEBUG_KEYS=1` must not be on for a scenario: its stderr trace shares the
  pty with the frame under test and scribbles over it. It is still how you find out
  what a chord actually resolved to — the same name also goes to the status bar as
  `k:<name>`, so one throwaway session with the flag set and a grep over the rows
  answers the question without a real terminal.
- F-keys and three-modifier chords are not encodable portably, so they stay
  manual. Headless ptys may also report a zero window size: the model guards
  against a non-positive `WindowSizeMsg` (keep that guard) and the harness always
  passes 120x30.

The old cygwin recipe `printf ... | script -qec './dmed f' /dev/null` still does
not work with the native Windows binary, and the pywinpty recipe it was replaced
by mangles ctrl-chords (`\x02`→`b`) — the Go harness supersedes both. What
remains manual is exactly what a pty cannot carry: F-keys, alt and
ctrl+alt chords, and the look of a theme.