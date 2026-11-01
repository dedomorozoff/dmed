# dmed v0.9.4

**dmed** - a terminal-native, keyboard-driven code editor built in Go, designed for AI-assisted development. AI agents read, propose, and apply changes - but every diff is approved by a human. Nothing happens behind your back.

This release closes the three points of an external review head-on: formatting goes through the language server, AI rounds on big repositories stop crawling the whole project tree, and accepted AI work can be undone from inside the editor - no external terminal, no hand-typed `git revert`.

## ✨ New: Format Document via LSP (`Ctrl+Alt+F`)

dmed speaks `textDocument/formatting` now: the language server lays the whole file out the way it is supposed to - **gofmt via gopls**, **prettier via the TypeScript server**, and every other server implementing the request.

- `Ctrl+Alt+F` or the palette command **Edit: Format Document** asks the server (the language server starts on demand if it is not running yet).
- The reply is applied through the same multi-file edit path rename uses - every touched open tab is edited in place, as the server sees the document.
- The buffer **stays dirty afterwards**: the layout lands on screen immediately, but saving it stays your call (`Ctrl+S`). An already-formatted file answers with `[already formatted]` instead of a fake edit.

(Rename and code actions existed before; the review's "no LSP actions at all" was out of date - formatting was the real gap, and it is closed.)

## ⚡ Fixed: AI rounds no longer walk the whole project

Every batch of AI tool calls cost **two full directory walks** over the project root (a snapshot before and a snapshot after, to detect what the tools created or rewrote) - even when the batch was pure reading: `SEARCH`, `GLOB`, `LIST_DIR`, the todo machinery. On a large repository that was the stall you felt *between every AI iteration*.

The walks are gated now: only a batch containing a tool that can touch files (`RUN`, `EDIT`, `REPLACE`) pays the walk. Read-only rounds walk nothing.

## ✨ New: Undo of AI work inside the editor (`u`)

Accepted agent changes are committed immediately - that is the point of the atomic-apply pipeline - but a commit should not be a dead end. The editor undoes its own work now:

- **`u` in the agent panel** (or the palette command **Agent: Undo Last Apply**) reverts the most recent `agent: …` commit. Every file the agent touched goes back to its pre-agent content, agent-created files are removed, agent-deleted files return.
- **Refuse-first safety**: every working-tree file must still match exactly what the agent apply wrote. If you edited a file after accepting, the revert aborts as a whole - before a single byte is written - so an undo can never eat your own newer work.
- The revert lands as its own **`agent: revert <hash>`** commit, so git history keeps telling the truth, and the reverted files open (re-loaded) in tabs like any other agent series.

## 📦 Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.3...v0.9.4
