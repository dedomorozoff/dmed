# Release v0.10.1 — dmed

**Release run:** git tag v0.10.1 (version is taken from the Makefile) or
VERSION=0.10.1 make <target> manually. The auto-release
(.github/workflows/release.yml) triggers on any v* tag push: it builds the
8 platform binaries, packages (deb/rpm/pkg/win-zip/termux) and publishes the
GitHub Release. The release text comes from docs/RELEASE-<TAG>.md when that
file exists, otherwise from the tag message.

---

## What's new in v0.10.1

v0.10.1 is cut from release/v0.10.1, 16 commits ahead of main (v0.10.0): M10–M13
features and fixes. New functionality was mostly unreleased — v0.10.0 was built
from main alone and was self-contained.

### M12 — decision flow, chat, agents, tools
- **One park model for every decision shape.** Chat, agent, question and
  sub-agent outcomes queue through a single park model: y/n arms read the head
  only; parkPeek shows the next item without moving the position.
- **Token cards** in the chat and in the agent panel, plus a quick model
  switch.
- **Ghost completion as FIM** (f35823b): fill-blank completion of a word or a
  phrase, debounced by the typing pause (750/450/250 ms, median of the last
  keystrokes); [ai] ghost_auto turns the typing trigger off.
- **Slash commands**: /name args expands a named template before submit —
  project .dmed/templates beats ~/.dmed/templates beats built-ins. A first
  # line is the description, not part of the prompt; .lua templates run on a
  fresh VM under a 3 s budget.
- **Agent persistence** (9b60819): queues, errors and the full Change set
  survive an editor restart and are stored in .dmed_agent.json.
- **Worker pool in the agent queue**: several workers drain the queue through
  their own wake channels; one Enqueue wakes all of them, so bursts are
  processed in parallel.
- **MCP stdio servers** (f18119d): JSON-RPC 2.0 over the attached stdio
  process. Flat mcp_<server>_<tool> tool names (over-long ones are reported,
  never sent), per-call confirmation, plan mode drops mcp_ names by prefix.
- **Single park rule** (52208b3): every ask goes through one park model;
  y/n arms read the head only.

### M13 — config, theme, panels, keys, tests
- **[theme] section**: token name to style. Entries are staged into the model's
  own map and painted over a fixed sample, synchronously — no server to probe,
  no reply to stamp, no tea.Cmd a pick can't return. Ctrl+S is the only write;
  Esc leaves the file untouched.
- **Per-file .editorconfig** (3128381): the FileOpts cache is invalidated per
  path on save or on a config change.
- **Panels dispatch on command names** (39d2e86): pane handlers match on
  command names, never on key literals. A chord removed through [keys] is
  swallowed rather than typed into the field; y/n review arms read the head
  only.
- **[keys] rebinds commands, not keystrokes** (c90e486): ResolveKeys joins the
  section to the defaults; an unknown action name is refused with a status-bar
  notice, never half-applied.
- **Named key actions + generated F1 cheat sheet** (f61f7eb, 082ba8e):
  dmed --dump-keys md|roff|actions round-trips through the resolver
  unchanged; Config: Open Keys shows the default tables.
- **TUI regression harness** (082ba8e): real pty, frames read back through
  vt10x; sleep-free scenarios (WaitFor, WaitAbsent, WaitForAtLeast, WaitFile).
  F-keys, Alt chords and three-modifier chords stay manual.
- **Status bar always last** (guarded by TestStatusBarAlwaysLastRow): the
  status row is the final frame line no matter how many dividers chain up.

### Fixes
- **terminal / git panel**: termquery.go answers terminal capability queries
  (fish waits ten seconds on the primary device attribute query without a
  reply), the shell exit flow and status-bar updates; tests aligned.
- **Ctrl+B** toggles the sidebar's binary source again.
- gofmt alignment in buffer, editor, i18n and lsp.

### Internal
- CI: fmt-check, vet, cover-check (floor 70.3, raised 0.2), test, race, tui
  and the native build; 8-platform cross-builds run on main/master and in PRs.

---

## Release checklist (for humans)

- [ ] make fmt-check — gofmt-clean.
- [ ] make vet — static analysis.
- [ ] make cover-check — 70.3% floor.
- [ ] make test — unit suite.
- [ ] make race — race detector over agent and editor.
- [ ] make tui — TUI regression harness (real pty, painted frames).
- [ ] Native build: make build and dmed -v prints dmed 0.10.1.
- [ ] 8 cross-builds: make build-linux-amd64 build-linux-arm64 build-freebsd-amd64 build-openbsd-amd64 build-netbsd-amd64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64.
- [ ] Packages: make deb rpm pkg win-zip termux (release.yml does this
      automatically).
- [ ] Tag v0.10.1 (annotated, message = short line).
- [ ] docs/RELEASE-v0.10.1.md — GitHub Release takes it as its body.

## After tagging

1. CI: vet + test, cover-check, tui, 8 cross-builds, packages and the GitHub
   Release run automatically.
2. release/v0.10.1 merges into main (no conflicts), the branch is deleted;
   main keeps the v0.10.1 tag.
3. Deployment (deb/rpm/termux into AUR/COPR/PPA) is deferred to project work.
4. Golden rule: there is no make release target — a tag runs everything
   above.

*Release script: make build + make <target> VERSION=0.10.1 locally; CI does
the rest on tag.*
