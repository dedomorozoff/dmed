# dmed v0.10.3

Maintenance release over v0.10.2: LSP project setup is now one command, Arch
users get proper packages, and the project is stress-tested against generated
codebases.

## Added

### LSP add and auto-install
- A palette command adds an LSP server for the current file type and, when the
  server is missing, offers to install it. The chosen configuration is written
  to the project's `.dmed.conf` and picked up on reload.
- A new `docs/` guide walks through the supported servers, the configuration
  keys and the key chords for definition, rename, code actions and formatting.

### Packaging
- Arch Linux PKGBUILDs for the `dmed` and `dmed-bin` packages, plus an install
  guide covering AUR and the other supported systems.

### Testing
- A bulk-apply test drives hundreds of `Change`s through `agent.Applier` in a
  single series: new files, edits to existing files, rejection of a stale
  patch and rollback after an injected write failure.
- A large-project test indexes a generated tree, checks the walking caps and
  fuzzy search, and records the cost as a benchmark.
- A pty scenario opens a generated project in a real terminal and drives the
  file finder over the whole index.

## Fixed

- The restart build tag now covers OpenBSD and NetBSD, so those cross-builds
  no longer pull in the Unix-only restart path.
- A few formatting-only fixes in the editor and i18n tables that kept the CI
  format check red.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.10.2...v0.10.3
