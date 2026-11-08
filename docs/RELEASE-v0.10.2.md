# dmed v0.10.2

Maintenance release over v0.10.1: the auto-update feature, which was written
but never connected to the editor, is now wired in end to end.

## Added

### Auto-update
- **Background update check.** At startup, when `[editor] check_updates` is
  on (the default), dmed asks GitHub's Releases API whether a newer version
  exists. The check runs in the background two seconds after the first frame
  so a slow connection cannot stall the UI, and the result is cached for 24
  hours. Development builds (`version == "dev"`) skip the check: there is no
  asset to download.
- **Semver comparison.** `internal/update.CompareSemver` handles pre-release
  tags (`0.10.1-rc1`), treats `dev` as older than any release, and picks the
  release asset matching the running OS/architecture.
- **Atomic apply.** `update.Apply` downloads the new binary into a temp file
  in the executable's directory and renames it over the running one, rolling
  back on any failure. On Windows the old binary is moved to `dmed.exe.old`
  first, because the running process holds the file open.
- **Relaunch.** After a successful apply the editor restarts itself with the
  same arguments in a fresh process, so the user sees the new version rather
  than a stale one. On Windows the relaunch is the only way to free the
  `.old` backup the rename left behind.
- **Palette & CLI.** `Ctrl+P` offers `update.check` and `update.apply`. The
  new `dmed update` command does the same from a shell: check, download,
  install, relaunch.
- **Config.** `[editor] check_updates` (default `true`) gates the background
  check; `[editor] skipped_dirs` and friends are unchanged.

### Testing
- `TestUpdatePaletteCommands`, `TestApplyUpdateWithoutRelease`, and
  `TestApplyUpdateCheckDropsStaleGeneration` cover the editor-side wiring.
- Coverage baseline raised 70.3 → 71.0 (total 73.9%).

## Changed

- `Makefile`: `COVERAGE_BASELINE` 70.3 → 71.0.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.10.1...v0.10.2