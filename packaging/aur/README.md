# Arch Linux packaging (AUR)

This directory contains ready-to-publish AUR package sources for **dmed**:

- `dmed/` — builds from the tagged source tarball (`vX.Y.Z`)
- `dmed-bin/` — installs the prebuilt Linux binary from GitHub Releases

> AUR packages are installed with an AUR helper (`yay`, `paru`) or manually with
> `makepkg -si`, not with plain `pacman -S` unless/until the package enters the
> official repositories.

## Install now (without AUR)

You can install from the local PKGBUILDs in this repository:

```sh
# build from source (requires Go >= 1.26 and network access to fetch modules)
cd packaging/aur/dmed
makepkg -si

# or install the prebuilt binary package
cd packaging/aur/dmed-bin
makepkg -si
```

Alternatively, install the release Arch package artifact directly when available:

```sh
sudo pacman -U dmed_<version>_x86_64.pkg.tar.zst
```

Or use the generic installer / release binaries: see the main [README](../../README.md#install).

## Publishing to AUR (maintainer)

AUR publication requires an AUR account with an SSH key.

1. Clone the AUR repos (separate from this repository):
   ```sh
   git clone ssh://aur@aur.archlinux.org/dmed.git
   git clone ssh://aur@aur.archlinux.org/dmed-bin.git
   ```
2. Copy `PKGBUILD` and `.SRCINFO` from `packaging/aur/dmed/` and
   `packaging/aur/dmed-bin/` into the corresponding AUR repos.
3. Verify locally on Arch:
   ```sh
   makepkg --printsrcinfo > .SRCINFO   # after any PKGBUILD change
   makepkg -si
   # optional: namcap PKGBUILD *.pkg.tar.zst
   ```
4. Commit and push each AUR repo.

**Maintainer:** `dedomorozoff <dedomorozoff@gmail.com>` (as set in the PKGBUILDs).

## Release/update checklist

- Bump `pkgver` (and reset `pkgrel` to `1`) in each `PKGBUILD`, then regenerate
  `.SRCINFO` (`makepkg --printsrcinfo > .SRCINFO`).
- **`dmed` (source):** update `sha256sums` for
  `https://github.com/dedomorozoff/dmed/archive/v${pkgver}.tar.gz`.
- **`dmed-bin`:** update per-arch checksums for the release assets
  (`dmed-linux-amd64`, `dmed-linux-arm64`) and the bundled `LICENSE`/`docs/dmed.1`
  files once the GitHub Release for that tag is published. Placeholder `SKIP`
  values must be replaced with real checksums before publishing to AUR.
- Note: the source PKGBUILD runs `go mod download`/builds against the network;
  adapting it for fully offline/sandboxed AUR builds (vendoring) may be needed
  depending on builder policy.

## If AUR registration is closed

New AUR account registration is sometimes temporarily closed (e.g. anti-spam).
In that case, the package can still be installed from the local PKGBUILDs or
release artifacts as shown above, and someone who already has AUR access can
publish/update the packages using the files in this directory.
