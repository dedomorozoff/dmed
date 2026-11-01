# dmed — the extended AI tool set

A plan to bring tool-calling up to a proper level: project navigation, a
plan mode with a task list, a dialog with the user, delegation to a nested
agent, and web search.

The plan's context is the 2026-10-01 audit, kept in git history as
`docs/AUDIT-2026-10-01.md` at rev `a86f2da`; one of its open items —
splitting `editor.go` — closes in the same effort: new tools are new code in
`internal/editor`, and the split goes with it.

---

## 0. What exists now

Five native function-calling tools, `internal/editor/tools.go:63`
(`chatToolDefs`) and `internal/editor/tools.go:125` (`execChatTool`):

| Tool | What it does | Gate |
|---|---|---|
| `READ` | whole file | `restrict_to_root` |
| `SEARCH` | `path:line:content`, `regex:true` = RE2 | read-only |
| `RUN` | shell in the project root, 30 s, 8 KiB cap | `allow_run = never/ask/always` |
| `REPLACE` | targeted replacement of a unique block | → `agent.Change` → diff review |
| `EDIT` | whole-file rewrite / creation | → `agent.Change` → diff review |

The tool loop: `chat.go:504` `handleChatToolsDone`, depth
`[ai] tool_rounds` (default 6); `allow_run=ask` parks the loop via
`runConfirmMarker` (`tools.go:230`) and waits for a y/n.

What is missing: `list_dir`, `glob`, `web_search`, `todo_*`, `ask_user`,
`sub_agent`, `switch_mode`, plan/act modes. In neither the code nor the
branch history (`main`, `dev`) — verified 2026-10-01.

**The invariant no item of this plan may break:** everything that writes
files goes as an `agent.Change` through `internal/agent.Applier`
(`Orig` validation + atomic write). This applies to the new `EDIT`/`REPLACE`
and to `sub_agent` alike — the nested agent gets no direct write access.

---

## 1. Common decisions (settled before any code)

> Status: **1.1, 1.2, 1.4 done** (2026-10-01); the rest is in force.

**1.1 Tool names.** The existing `READ/SEARCH/RUN/REPLACE/EDIT` are not
renamed: the models are used to them, and a rename breaks every saved chat
thread (see `chat.go:883`, where historical `ToolCalls` are replayed). New
tools are named in snake_case — done as follows: `LIST_DIR`/`GLOB`
(uppercase, like the rest) plus the `legacyToolName` alias table
(`read_file→READ`, `write_file→EDIT`, `edit_file→REPLACE`, `grep→SEARCH`,
`run_command→RUN`, `list_dir→LIST_DIR`, `glob→GLOB`). The open question
about mixed naming is settled: aliases cover both sides, and `toolKey`
compares names case-insensitively.

**1.2 The model's attention budget.** Done: config sections

```ini
[ai]
tools_enabled = read,search,run,replace,edit,list_dir,glob
tools_disabled = web_search,sub_agent
```

`activeChatToolDefs()` filters the registry by these two fields
(`AIConfig` + `parseList`); the default is the full set. `WriteAI` does not
write these keys: the list is hand-curated and the wizard must not clobber it.

**1.3 Conversation history.** Every new tool's result is a plain
`ai.Message{Role: "tool"}`, so `trimChatHistory` (`chat.go:448`) works
unchanged. Caps: `READ` capped at `ContextMax`, `SEARCH` at 24 hits,
`glob`/`list_dir` at 200 entries, `web_search` top-8 results with a snippet,
`todo_read` the whole list (usually < 1 KiB).

**1.4 One common tool runner.** Done: `execChatTool` returns a single
`ToolResult` (`Text` + `Change` + `Park`) instead of the
`(string, *Change)` pair. The binary `runConfirmMarker` was removed — in its
place is `Park` and the `chatParks`/`chatPark` queue with a readable
placeholder that `resolveParkedResult` swaps for the real outcome. Park
kinds are added to `ParkKind` together with the corresponding tool (one so
far — `ParkRunConfirm`); the common entry points are `startNextPark` plus
`handleChatParkKey`.

**1.5 The single tool outside the workspace** is `web_search`. Therefore it:
(a) is off by default; (b) gets no `RUN`/`EDIT` access — neither on its own
nor for the model it invokes; (c) writes a
`[web_search] <query> → <n> results` line next to every result in the
transcript; (d) has a per-session request cap (`[ai] web_search_budget`,
default 20).

**1.6 Delegation must not bypass review.** `sub_agent` returns not text but
`[]agent.Change` to the calling tool — they land in the common
`m.chatPendingChanges` and go through the same diff review. No separate
"apply at once" path is provided.

---

## 2. Item 1 — `LIST_DIR` and `GLOB` (3–4 h, low risk) — ✅ done

The cheapest item and the most visible benefit: today, to learn the project
layout, the model either calls `RUN ls` (noisy, needs confirmation under
`allow_run=ask`) or `SEARCH`es the whole tree (expensive).

- `LIST_DIR(path)` — directory entries: name + `dir|file` + size; sorted
  directories-first, cap 200, `skipped_dirs` and dot-files skipped.
  Done: `chatListDir`, `tools_fs.go`.
- `GLOB(pattern)` — paths matching the pattern, with `**` support
  ("zero or more path segments") on top of `path.Match`; the walk prunes
  conservatively via `globMayContain` (a false "cannot contain" would hide
  a file from the model, so `false` only when the pattern provably cannot
  match). Directories are included too. Done: `chatGlob`, `matchGlob`.
- The shared walk lives in `internal/editor/tools_fs.go`:
  `walkProject` + `newProjectSkip` are used by SEARCH, LIST_DIR and GLOB
  alike; `.git`/`.dmed` are always skipped, even without a loaded config.
- Tests: `internal/editor/tools_fs_test.go` — the tool-config filter,
  aliases, the LIST_DIR output format, GLOB (including nested `**`
  patterns, the root, skipping ignored trees, a broken pattern, an absolute
  path), `matchGlob` as a table, `globMayContain` on "never prune too much",
  `walkProject` with `skipped_dirs`.

Acceptance (done): `LIST_DIR`/`GLOB` need no confirmation under
`allow_run=ask`; `authPath` applies to both; `**/*.go` never enters
`.git`/`node_modules`.

---

## 3. Item 2 — plan mode and `internal/todo` (1 day, low risk) — ✅ done

Three tools: `todo_write` (replace the list), `todo_set` (append items),
`todo_read` (read it). The store is pure data without TUI, as the package
separation requires.

- `internal/todo/todo.go`: `Item{Text, Done}`, `List` with `Replace`/`Merge`/
  `Complete`/`Items`/`Render`, thread-safe with copies on the way out
  (the same invariant as `agent.Queue`), caps of 50 items and 200 runes
  per item.
- A `todo *todo.List` field on `Model` (`New` initializes; `todoList()`
  creates it lazily, because tests assemble `Model` as a literal); the plan
  block renders last in the chat — `todoBlock` in `tools_plan.go`, styles
  `label-todo`/`todo`/`todo-done`.
- The `TODO_*` result is a compact `[1] done  read README.md` text; the same
  text goes to the model and is drawn (one source of truth).
- i18n: `chat.todo` in en/ru in sync.
- `resetChatConversation` clears the plan and drops pending questions —
  otherwise they would survive changing the thread.

Acceptance (done): tests on `todo.List` (caps, trimming, `Merge` does not
duplicate, copies on the way out, `-race` on concurrent access) and on the
chat block's rendering.

---

## 4. Item 3 — `ask_user` / `internal/ask` (0.5 d, medium risk) — ✅ done

The model asks a question and waits for the answer — the only tool that
requires a real dialog rather than a line in the transcript.

- `internal/ask/ask.go`: `Request{ID, Question, Choices, Asked}` + `Queue`
  (`Next`/`Answer`/`Cancel`/`Pending`), no TUI, thread-safe.
- `ask_user(question, choices?)` → `ToolResult{Text: placeholder, Park:
  {Kind: ParkAskUser, Ask: *Request}}` — the same parking mechanism as RUN.
- The overlay: the question + choices (`↑↓` to select, `Enter` to answer
  with the selection, `Tab` puts the choice into the input field for
  editing, `Esc` skips) and an answer line; the chat opens by itself
  (`startAsk`), otherwise the question is invisible and it hangs as a
  pending call, not a decision.
- The keyboard is intercepted globally (`editor.go`: `askReq != nil` before
  every other handler) — the only cursor sits on the question.
- The overlay's height is counted in `viewHeight`/`contextBottomExtraRows`,
  the drawing lives in `View`, the hint line in `statusIconsVisible`.
- The answer replaces the placeholder; `Esc` tells the model
  "cancelled — continue without it".

Acceptance (done): tests on parking and substituting the answer, on choosing
a selection, on `Esc`, on a RUN+ASK_USER queue within one round, on the
overlay height (`askExtraRows` == rendered rows + 1) and on the reset at a
new thread. The layout is verified by hand in a real terminal — per the
AGENTS.md caveats winpty does not fit.

---

## 5. Item 4 — `web_search` via DuckDuckGo (0.5 d, medium risk) — ✅ done

The only tool crossing the workspace boundary (1.5).

- A new package `internal/websearch`: `Client.Search(ctx, q)`, `Result`,
  `Parse` (pure — tested against a fixture), `Format`. No new dependencies:
  `net/http` + parsing the HTML of `html.duckduckgo.com/html/?q=…`.
- A client with its own timeouts (10 s connect / 20 s header / 30 s for the
  whole exchange), a 1 MiB body cap, a mandatory `User-Agent` (without one
  the page arrives truncated).
- The `WEB_SEARCH` tool enters the list only when `web_search = true`; the
  budget `web_search_budget` (default 20) is per session, and once it is
  spent the tool is **withdrawn from the list**, not merely rejected (a
  small model should not keep hammering a call that refuses anyway).
- The result is printed in the transcript as `[web_search q] N result(s)`;
  a network error is returned to the model as text (`lookup failed: …`)
  and also spends budget.
- Tests: `internal/websearch` (the parser against a fixture, a forgiving
  mode on broken pages, the cap, context cancellation, an HTTP error) and
  `tools_web_test.go` (opt-in, budget + no network request once it is
  spent, the error visible to the model). Network access only via
  `m.webSearchOverride`.

Acceptance (done): with `web_search` absent from the config the network is
never touched; `go test ./internal/websearch` is green without internet.

---

## 6. Item 5 — plan/act modes and `switch_mode` (1–1.5 d, medium risk) — ✅ done

- `AIConfig.Mode_` (`act` | `plan`, default `act`) + `AgentMode()`, which
  maps an unknown value to `act`: a typo must not silently turn the model
  into a mere reader. A palette command: `AI: Toggle Plan/Act Mode`.
- In `plan` mode `RUN`, `EDIT`, `REPLACE`, `SWITCH_MODE` are **removed from
  the request** (`readOnlyToolDefs`) — the model can only read and manage
  the plan. This is not "a write ban" but the *absence* of the tool: there
  is nothing to call.
- `modeNotice()` is appended to the system message on every turn, otherwise
  the model keeps attempting edits and complaining.
- `SWITCH_MODE(mode)` is available **only in plan mode**, parks the loop
  (`ParkSwitchMode`) and waits for a y/n in the status bar. On `n` the model
  gets "switch_mode denied … keep investigating", not silence.
- `act → plan` is switched by the human only (the palette), never the model.
- A `PLAN` indicator in the chat header.

Acceptance (done): a test that a request prepared in plan mode contains no
EDIT/RUN/REPLACE/SWITCH_MODE; tests on y (the switch + the model learns
about it), on n (stays in plan + the model learns about it), on a call from
act mode (no parking, a sensible error) and on a bad argument.

---

## 7. Item 6 — `sub_agent` (2–3 d, high risk) — ✅ done

The most expensive and the riskiest; done last, once 1–5 are test-covered.

- `internal/agent/subagent.go`: the `Subagent.Run` loop — a provider, a tool
  set through the `ToolExecutor` interface, a round cap (6) and a timeout
  (180 s). The package stays TUI-free.
- `Task.Kind` (`KindAgent` | `KindSubagent`) + `Queue.EnqueueSub`: delegation
  goes through **the same queue**, so the task is visible in the agent panel
  with a `[sub]` label and passes **the same** diff review → `Applier` →
  commit.
- The sub-agent's tools are a positive list (`READ`, `SEARCH`, `LIST_DIR`,
  `GLOB`, `REPLACE`, `EDIT`). `RUN` is withheld: its confirmation is a park,
  and a background task has nobody to answer. `ASK_USER` for the same
  reason, `SUB_AGENT` to keep the depth 1.
- A separate prompt (`[agent] subagent_prompt`) and a round cap
  (`[agent] subagent_rounds`).
- The key implementation point: the tools moved into `toolEnv`
  (`tools_core.go`) — the same functions the chat uses, but without a
  `Model`. The sub-agent runs in the worker goroutine and must not touch the
  editor's state; duplicating the tool logic would be worse.
- The chat **parks** for the duration of the delegation (`ParkSubAgent`)
  instead of waiting blockingly: the tool loop spins on the UI thread, and a
  synchronous wait would freeze the editor. Progress goes to the panel,
  `Esc` cancels, `pollSubAgentPark` (on a queue event, inside the update
  loop) wakes the conversation and hands the report to the model.
- Repeated edits of one file are merged (the last wins) — the reviewer does
  not get three conflicting diffs.

Acceptance (done): tests in `internal/agent/subagent_test.go` (proposals →
review, no changes → done, one diff per path, the round ceiling, a timeout
leaves no task in `running`, a provider error is visible, no provider/tools
fails) and `internal/editor/tools_subagent_test.go` (the tool appears only
when the queue is live and not in plan mode, the tool set, the code shared
with the chat, `restrict_to_root`, parking, a report with the file count and
a pointer at the panel, error/cancel/"proposed nothing", an empty task).

---

## 8. Order and estimates

| # | Item | Estimate | Risk | Status | Blocks |
|---|---|---|---|---|---|
| 0 | Tool-runner refactor onto `ToolResult`/`Park` (1.4) | 3–4 h | low | ✅ | 3–6 |
| 1 | `tools_enabled`/`tools_disabled` (1.2) | 2 h | low | ✅ | 1–6 |
| 2 | `LIST_DIR` + `GLOB` | 3–4 h | low | ✅ | — |
| 3 | `todo` + `internal/todo` | 1 d | low | ✅ | — |
| 4 | `ask_user` + `internal/ask` | 0.5 d | medium | ✅ | — |
| 5 | `web_search` + `internal/websearch` | 0.5 d | medium | ✅ | — |
| 6 | plan/act + `switch_mode` | 1–1.5 d | medium | ✅ | — |
| 7 | `sub_agent` + `internal/agent/subagent.go` | 2–3 d | high | ✅ | — |

**The plan is fully closed** (2026-10-01): 7–9 days of work, eight tools
for the model (READ, SEARCH, LIST_DIR, GLOB, RUN, REPLACE, EDIT, TODO_*,
ASK_USER, SWITCH_MODE, WEB_SEARCH, SUB_AGENT), three new packages
(`internal/todo`, `internal/ask`, `internal/websearch`) and one extended
(`internal/agent`). Only item 3 of section 10 (a synchronous `sub_agent`
versus the `agent.Queue`) was left open — it is settled in favor of the
queue: the UI must not block.

---

## 9. Verification before finishing

```sh
make build && make vet && make test && make race && make fmt-check
```

Mandatory: a test on the tool-config filter in `config_test.go`, the new
en/ru keys in sync (`TestCatalogsHaveSameKeys`), `README.md` — a section on
`web_search` ("makes requests to the internet, off by default") and
`AGENTS.md` — the new packages described under Architecture.
`ROADMAP.md` — start an M8 with these checkboxes.

---

## 10. Open questions (settled before item 4)

1. ~~The `glob` syntax~~ — settled: a matcher with `**` over `path.Match`
   (`matchGlob`); item 2 is done.
2. ~~`web_search`: html DuckDuckGo or the Instant Answer API~~ — settled:
   html DuckDuckGo parsed by `result__a`/`result__snippet`, `Parse` is pure
   and covered by a fixture (item 5).
3. `sub_agent`: synchronously inside the tool loop, or via `agent.Queue`?
   Affects the chat's responsiveness.
4. ~~Tool names~~ — settled: mixed case plus aliases in both directions.

## 11. Bonus: a key provider as the fallback default (done)

`internal/ai` no longer assumes "OpenAI-compatible" = `/v1`:
`Config.APIPath`/`ModelsPath` allow attaching Pollinations, which serves the
same protocol under `/openai` and lists models at `/models`. The
`Pollinations (free, no key)` preset stands second in the wizard, asks for
no key; `setup-ai` prints a warning about the public service, and `WriteAI`
saves the prefixes (otherwise a restart would 404 instead of answering).

A separate design decision: if the user **configured nothing** and the local
provider is unreachable, the session switches to Pollinations
(`free_fallback`, default `true`). The conditions are narrow and deliberate:

- `config.AIConfig.Unconfigured()` — an empty model, an empty key, the
  default Ollama. Any user choice (a model, a key, one's own server, an
  explicit preset) turns the fallback off for good;
- it fires once per session and only if the key service answered the probe
  (otherwise the user sees the original error, not silence);
- a `CLOUD` badge in the chat header and a notice in the transcript:
  "prompts, files and tool results now leave this machine", plus the config
  key that turns it off;
- `free_fallback = false` disables it entirely; picking a provider in
  `Ctrl+P` → `AI: Preferences...` also clears the fallback state and the
  notice.

Pollinations' anonymous tier is rate-limited to roughly one request per
15 seconds, so this is a fallback, not a default: useless on a batch.
