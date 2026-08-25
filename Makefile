GO       := go
GOPROXY  := https://proxy.golang.org,direct
GOSUMDB  := sum.golang.org

export GOROOT :=
export GOPROXY
export GOSUMDB

VERSION  ?= 0.3.0
LDFLAGS  := -s -w -X main.version=$(VERSION)
PREFIX   ?= /usr/local

.DEFAULT_GOAL := help

.PHONY: help build test vet run clean dist install install-man
.PHONY: deb rpm pkg termux freebsd-port

help: ## Show this help
	@echo "dmed $(VERSION)"
	@echo ""
	@echo "Usage: make <target> [OPTIONS]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Options:"
	@echo "  VERSION=X.Y.Z    Set version (default: $(VERSION))"
	@echo "  PREFIX=/usr       Install prefix (default: $(PREFIX))"
	@echo "  DESTDIR=/staging  Install DESTDIR for staged installs"

build: ## Build for current platform
	$(GO) build -ldflags "$(LDFLAGS)" .

test: ## Run unit tests
	$(GO) test ./...

vet: ## Run static analysis
	$(GO) vet ./...

run: build ## Build and run on FILE
	./dmed.exe $(FILE)

clean: ## Remove build artifacts
	rm -f dmed dmed.exe
	rm -rf dist/

install: build ## Install binary + man page to PREFIX
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 dmed $(DESTDIR)$(PREFIX)/bin/dmed
	install -d $(DESTDIR)$(PREFIX)/share/man/man1
	install -m 644 docs/dmed.1 $(DESTDIR)$(PREFIX)/share/man/man1/dmed.1

install-man: ## Install man page only
	install -d $(PREFIX)/share/man/man1
	install -m 644 docs/dmed.1 $(PREFIX)/share/man/man1/dmed.1
	@mandb 2>/dev/null || true

# --- Cross-compilation ---

DISTDIR := dist

build-linux-amd64: ## Build for Linux amd64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-linux-amd64 .

build-linux-arm64: ## Build for Linux arm64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-linux-arm64 .

build-freebsd-amd64: ## Build for FreeBSD amd64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-freebsd-amd64 .

build-openbsd-amd64: ## Build for OpenBSD amd64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-openbsd-amd64 .

build-netbsd-amd64: ## Build for NetBSD amd64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=netbsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-netbsd-amd64 .

build-darwin-amd64: ## Build for macOS amd64
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-darwin-amd64 .

build-darwin-arm64: ## Build for macOS arm64 (Apple Silicon)
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-darwin-arm64 .

PLATFORMS := linux-amd64 linux-arm64 freebsd-amd64 openbsd-amd64 netbsd-amd64 darwin-amd64 darwin-arm64

dist: $(addprefix build-,$(PLATFORMS)) ## Build all platforms
	@echo "Built binaries:"
	@ls -lh $(DISTDIR)/dmed-*

# --- Packages ---

# Debian/Ubuntu (.deb)
deb: build-linux-amd64 ## Build .deb package (amd64)
	@mkdir -p dist/deb/dmed_$(VERSION)_amd64/DEBIAN
	@mkdir -p dist/deb/dmed_$(VERSION)_amd64/usr/bin
	@mkdir -p dist/deb/dmed_$(VERSION)_amd64/usr/share/man/man1
	cp dist/dmed-linux-amd64 dist/deb/dmed_$(VERSION)_amd64/usr/bin/dmed
	chmod 755 dist/deb/dmed_$(VERSION)_amd64/usr/bin/dmed
	cp docs/dmed.1 dist/deb/dmed_$(VERSION)_amd64/usr/share/man/man1/dmed.1
	echo "Package: dmed" > dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo "Version: $(VERSION)" >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo "Architecture: amd64" >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo "Maintainer: dmed contributors" >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo "Description: Terminal code editor with AI agents" >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo " A terminal-native code editor with AI agents as first-class" >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	echo " participants. Supports Ollama and OpenAI-compatible backends." >> dist/deb/dmed_$(VERSION)_amd64/DEBIAN/control
	dpkg-deb --build dist/deb/dmed_$(VERSION)_amd64 dist/dmed_$(VERSION)_amd64.deb
	@echo "Built: dist/dmed_$(VERSION)_amd64.deb"

# Fedora/RHEL (.rpm)
rpm: build-linux-amd64 ## Build .rpm package (x86_64)
	@mkdir -p dist/rpm/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
	cp dist/dmed-linux-amd64 dist/rpm/SOURCES/dmed
	cp docs/dmed.1 dist/rpm/SOURCES/dmed.1
	echo 'Name:           dmed' > dist/rpm/SPECS/dmed.spec
	echo 'Version:        $(VERSION)' >> dist/rpm/SPECS/dmed.spec
	echo 'Release:        1%{?dist}' >> dist/rpm/SPECS/dmed.spec
	echo 'Summary:        Terminal code editor with AI agents' >> dist/rpm/SPECS/dmed.spec
	echo 'License:        BSD-3-Clause' >> dist/rpm/SPECS/dmed.spec
	echo 'URL:            https://github.com/user/dmed' >> dist/rpm/SPECS/dmed.spec
	echo '' >> dist/rpm/SPECS/dmed.spec
	echo '%description' >> dist/rpm/SPECS/dmed.spec
	echo 'A terminal-native code editor with AI agents as first-class' >> dist/rpm/SPECS/dmed.spec
	echo 'participants. Supports Ollama and OpenAI-compatible backends.' >> dist/rpm/SPECS/dmed.spec
	echo '' >> dist/rpm/SPECS/dmed.spec
	echo '%install' >> dist/rpm/SPECS/dmed.spec
	echo 'install -D -m 755 %{SOURCE0} %{buildroot}/usr/bin/dmed' >> dist/rpm/SPECS/dmed.spec
	echo 'install -D -m 644 %{SOURCE1} %{buildroot}/usr/share/man/man1/dmed.1' >> dist/rpm/SPECS/dmed.spec
	echo '' >> dist/rpm/SPECS/dmed.spec
	echo '%files' >> dist/rpm/SPECS/dmed.spec
	echo '/usr/bin/dmed' >> dist/rpm/SPECS/dmed.spec
	echo '/usr/share/man/man1/dmed.1' >> dist/rpm/SPECS/dmed.spec
	rpmbuild --define "_topdir $(CURDIR)/dist/rpm" -ba dist/rpm/SPECS/dmed.spec
	@echo "Built: dist/rpm/RPMS/x86_64/dmed-$(VERSION)-1.*.rpm"

# Arch Linux (.pkg.tar.zst)
pkg: build-linux-amd64 ## Build .pkg.tar.zst (Arch, x86_64)
	@mkdir -p dist/pkg/dmed/usr/bin
	@mkdir -p dist/pkg/dmed/usr/share/man/man1
	cp dist/dmed-linux-amd64 dist/pkg/dmed/usr/bin/dmed
	chmod 755 dist/pkg/dmed/usr/bin/dmed
	cp docs/dmed.1 dist/pkg/dmed/usr/share/man/man1/dmed.1
	echo '# Maintainer: dmed contributors' > dist/pkg/dmed/PKGBUILD
	echo 'pkgname=dmed' >> dist/pkg/dmed/PKGBUILD
	echo "pkgver=$(VERSION)" >> dist/pkg/dmed/PKGBUILD
	echo 'pkgrel=1' >> dist/pkg/dmed/PKGBUILD
	echo 'pkgdesc="Terminal code editor with AI agents"' >> dist/pkg/dmed/PKGBUILD
	echo 'arch=("x86_64")' >> dist/pkg/dmed/PKGBUILD
	echo 'url="https://github.com/user/dmed"' >> dist/pkg/dmed/PKGBUILD
	echo 'license=("BSD-3-Clause")' >> dist/pkg/dmed/PKGBUILD
	echo 'source=()' >> dist/pkg/dmed/PKGBUILD
	echo 'sha256sums=()' >> dist/pkg/dmed/PKGBUILD
	echo 'package()' >> dist/pkg/dmed/PKGBUILD
	echo '  install -D -m 755 "$srcdir/usr/bin/dmed" "$pkgdir/usr/bin/dmed"' >> dist/pkg/dmed/PKGBUILD
	echo '  install -D -m 644 "$srcdir/usr/share/man/man1/dmed.1" "$pkgdir/usr/share/man/man1/dmed.1"' >> dist/pkg/dmed/PKGBUILD
	cd dist/pkg/dmed && makepkg -f --noarchive
	mv dist/pkg/dmed/*.pkg.tar.zst dist/
	@echo "Built: dist/dmed-$(VERSION)-1-x86_64.pkg.tar.zst"

# Termux (.deb for Android)
termux: build-linux-arm64 ## Build Termux .deb (aarch64)
	@mkdir -p dist/termux/dmed_$(VERSION)_aarch64/DEBIAN
	@mkdir -p dist/termux/dmed_$(VERSION)_aarch64/data/data/com.termux/files/usr/bin
	cp dist/dmed-linux-arm64 dist/termux/dmed_$(VERSION)_aarch64/data/data/com.termux/files/usr/bin/dmed
	chmod 755 dist/termux/dmed_$(VERSION)_aarch64/data/data/com.termux/files/usr/bin/dmed
	echo "Package: dmed" > dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo "Architecture: aarch64" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo "Depends: libandroid-support" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo "Homepage: https://github.com/user/dmed" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo "Maintainer: dmed contributors" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo "Description: Terminal code editor with AI agents" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	echo " Version: $(VERSION)" >> dist/termux/dmed_$(VERSION)_aarch64/DEBIAN/control
	dpkg-deb --build dist/termux/dmed_$(VERSION)_aarch64 dist/dmed_$(VERSION)_aarch64-termux.deb
	@echo "Built: dist/dmed_$(VERSION)_aarch64-termux.deb"

# FreeBSD port
freebsd-port: ## Generate FreeBSD port Makefile
	@mkdir -p dist/freebsd-port
	echo 'PORTNAME=       dmed' > dist/freebsd-port/Makefile
	echo 'DISTVERSION=    $(VERSION)' >> dist/freebsd-port/Makefile
	echo 'CATEGORIES=     editors' >> dist/freebsd-port/Makefile
	echo '' >> dist/freebsd-port/Makefile
	echo 'COMMENT=        Terminal code editor with AI agents' >> dist/freebsd-port/Makefile
	echo '' >> dist/freebsd-port/Makefile
	echo 'LICENSE=        BSD3CLAUSE' >> dist/freebsd-port/Makefile
	echo '' >> dist/freebsd-port/Makefile
	echo 'USES=           gmake go' >> dist/freebsd-port/Makefile
	echo 'GH_ACCOUNT=     user' >> dist/freebsd-port/Makefile
	echo 'GH_PROJECT=     dmed' >> dist/freebsd-port/Makefile
	echo 'GH_TUPLE=       ...' >> dist/freebsd-port/Makefile
	echo '' >> dist/freebsd-port/Makefile
	echo 'do-install:' >> dist/freebsd-port/Makefile
	echo '	${INSTALL_PROGRAM} ${WRKSRC}/dmed ${STAGEDIR}${PREFIX}/bin/dmed' >> dist/freebsd-port/Makefile
	echo '	${INSTALL_MAN} ${WRKSRC}/docs/dmed.1 ${STAGEDIR}${PREFIX}/share/man/man1/dmed.1' >> dist/freebsd-port/Makefile
	@echo "Built: dist/freebsd-port/Makefile"
