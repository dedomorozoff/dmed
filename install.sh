#!/bin/sh
#
# dmed — one-line installer
#
#   curl -fsSL https://raw.githubusercontent.com/dedomorozoff/dmed/main/install.sh | sh
#
# Downloads the latest dmed release binary from GitHub Releases and installs
# it into ~/.local/bin (or %LOCALAPPDATA%\dmed.exe on native Windows).

set -u

REPO="dedomorozoff/dmed"
VERSION="${DMED_VERSION:-}"

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# --- Detect OS -------------------------------------------------------------
OS="$(uname -s 2>/dev/null || echo unknown)"
case "$OS" in
  Linux)               os_name="linux" ;;
  Darwin)              os_name="darwin" ;;
  FreeBSD)             os_name="freebsd" ;;
  OpenBSD)             os_name="openbsd" ;;
  NetBSD)              os_name="netbsd" ;;
  CYGWIN*|MSYS*|MINGW*|MINGW64*|"Windows"*)
                       os_name="windows" ;;
  *) die "unsupported OS: $OS" ;;
esac

# --- Detect arch -----------------------------------------------------------
ARCH="$(uname -m 2>/dev/null || echo unknown)"
case "$ARCH" in
  x86_64|amd64|AMD64)  arch="amd64" ;;
  aarch64|arm64|arm64v8) arch="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

exe=""
[ "$os_name" = "windows" ] && exe=".exe"

# --- Pick install location ---------------------------------------------------
if [ "$os_name" = "windows" ]; then
  # %LOCALAPPDATA%\dmed or C:\Users\<user>\AppData\Local\dmed
  bindir="${LOCALAPPDATA:-$HOME/AppData/Local}/dmed"
  mkdir -p "$bindir" || die "cannot create $bindir"
  inst="$bindir/dmed$exe"
else
  bindir="${HOME}/.local/bin"
  mkdir -p "$bindir" || die "cannot create $bindir"
  inst="$bindir/dmed$exe"
fi

# --- Resolve latest version if none given ------------------------------------
if [ -z "$VERSION" ]; then
  VERSION="latest"
  url="https://github.com/$REPO/releases/latest/download/dmed-$os_name-$arch$exe"
else
  url="https://github.com/$REPO/releases/download/v$VERSION/dmed-$os_name-$arch$exe"
fi

tmp="$(mktemp 2>/dev/null || echo "$bindir/.dmed-download.$$")"
trap 'rm -f "$tmp"' EXIT

log "Downloading dmed-$os_name-$arch from $REPO ($VERSION)"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url" -o "$tmp" || die "download failed: $url"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$tmp" "$url" || die "download failed: $url"
else
  die "neither curl nor wget found"
fi

[ -s "$tmp" ] || die "downloaded file is empty"

mv "$tmp" "$inst" || die "cannot move binary into place"
[ "$os_name" != "windows" ] && chmod +x "$inst"

# --- Add to PATH if missing ---------------------------------------------------
case ":$PATH:" in
  *":$bindir:"*) ;;
  *)
    warn "add $bindir to your PATH to run 'dmed' from anywhere"
    ;;
esac

log "Installed dmed to $inst"
"$inst" --version 2>/dev/null || true
log "Run 'dmed' to get started."
