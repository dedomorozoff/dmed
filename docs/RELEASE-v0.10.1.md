# dmed v0.10.1

Feature release over v0.10.0: M12 and M13 roadmap milestones complete. Highlights include Model Context Protocol (MCP) tool servers, persistent background agent queues with a worker pool, FIM ghost completions with typing cadence debounce, slash command templates, a unified decision park flow with peek stack, interactive theme styling, and customizable keyboard mappings.

## Added

### AI & Agent: M12 complete
- **Model Context Protocol (MCP) stdio servers.** Integrate external MCP servers over stdio via JSON-RPC 2.0. Server tools map to flat `mcp_<server>_<tool>` names with per-call confirmation prompts; plan mode safely disables tool execution.
- **Persistent agent task queue & worker pool.** Agent tasks, execution status, errors, and unapplied diffs survive editor restarts in `.dmed_agent.json`. The queue pool processes tasks in parallel across multiple background workers with dedicated wake channels.
- **FIM ghost completions.** Copilot-style ghost suggestions using Fill-In-the-Middle (FIM) for code-specialized models (Codestral, StarCoder, etc.). Suggestions trigger dynamically based on typing cadence (750/450/250 ms adaptive debounce); `[ai] ghost_auto` controls background triggering.
- **Chat slash commands.** Typing `/name args` expands user, project, or built-in prompt templates before sending. Lua templates run in an isolated VM with a 3-second budget for dynamic prompt generation.
- **Unified decision park flow & peek stack.** Chat tool executions, agent reviews, sub-agent delegations, and user questions route through a single park presenter with a `[k/N]` queue badge. Press `Tab` (`parkPeek`) to preview pending decisions without popping the queue.
- **Token usage cards & quick model switch.** Chat messages and agent tasks report token usage metrics. The "AI: Switch Model…" palette overlay enables instantaneous LLM switching.

### Configuration, Themes & Keymaps: M13 complete
- **Theme customizer & preview panel.** A new `[theme]` configuration section maps Chroma tokens to custom styles. The interactive theme picker previews token styles live over a syntax sample; changes save on `Ctrl+S` or cancel cleanly on `Esc`.
- **Per-file `.editorconfig` support.** Indentation, tab stops, and trailing newline preferences resolve per file with cached lookup and automatic cache invalidation on save or configuration reload.
- **Custom keybindings (`[keys]`).** Rebind actions directly by command name in `.dmed.conf`. Unknown actions and key conflicts report directly to the status bar rather than half-applying.
- **Named key actions & generated F1 cheat sheet.** All editor shortcuts have discrete action IDs. The F1 cheat sheet and `dmed --dump-keys md|roff|actions` are automatically generated from default action definitions.
- **Pane command dispatching.** Panel handlers dispatch strictly on semantic command names rather than raw keypresses, preventing unbound keys from leaking into text inputs.
- **Plugin event bus.** Lua plugins communicate via asynchronous bus events with delivery timeouts.

### Testing & Quality
- **TUI regression test harness.** An end-to-end regression harness running in a real pseudo-terminal using `vt10x`, providing deterministic, sleep-free screen assertions (`WaitFor`, `WaitAbsent`, `WaitFile`).
- **Status bar invariant testing.** Automated test guarantees the status bar remains strictly the final frame row across all split and panel combinations.

## Fixed

- **Terminal hangs & capability queries.** Persistent shell terminal responds properly to capability queries (`termquery.go`), eliminating 10-second startup delays in shells like `fish`.
- **Sidebar toggle.** `Ctrl+B` reliably toggles the sidebar's binary display state.
- **Data race in ghost completion.** Resolved a concurrent access race between background ghost streaming and configuration updates during test runs.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.10.0...v0.10.1
