# dmed v0.9.6

Patch release over v0.9.4: fixes for opening non-text files and CRLF handling.

## Fixed

- **Binary files no longer break the UI.** Opening a binary/non-text file as text is refused with a status message instead of loading garbage into the buffer and corrupting the terminal rendering. UTF-16 files with a BOM are still recognised as text.
- **CRLF line endings.** Carriage returns are normalised on load (both `\r\n` and lone `\r`), so `\r` can no longer leak into the buffer and garble rendering (disappearing line numbers, cursor jumping to column 0). Line-ending detection now keys on `\r\n` sequences rather than any stray `\r`. The same normalisation is applied to file reloads, diff views, and tool reads.
- **Agent undo reload robustness.** Reverted files are reliably reloaded into open tabs even when their stored path differs by cleaning/normalisation.

## Install

One-line install:

```sh
curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
```

Build from source:

```sh
git clone https://github.com/dedomorozoff/dmed.git && cd dmed && make build
```

**Full changelog**: https://github.com/dedomorozoff/dmed/compare/v0.9.4...v0.9.6
```