GO       := go
GOPROXY  := https://proxy.golang.org,direct
GOSUMDB  := sum.golang.org

export GOROOT :=
export GOPROXY
export GOSUMDB

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test vet run clean dist install-man

build:
	$(GO) build -ldflags "$(LDFLAGS)" .

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

run: build
	./dmed.exe $(FILE)

clean:
	rm -f dmed dmed.exe
	rm -rf dist/

# --- Cross-compilation ---

DISTDIR := dist

build-linux-amd64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-linux-amd64 .

build-linux-arm64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-linux-arm64 .

build-freebsd-amd64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-freebsd-amd64 .

build-openbsd-amd64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-openbsd-amd64 .

build-netbsd-amd64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=netbsd GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-netbsd-amd64 .

build-darwin-amd64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-darwin-amd64 .

build-darwin-arm64:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DISTDIR)/dmed-darwin-arm64 .

# --- Distribution ---

PLATFORMS := linux-amd64 linux-arm64 freebsd-amd64 openbsd-amd64 netbsd-amd64 darwin-amd64 darwin-arm64

dist: $(addprefix build-,$(PLATFORMS))
	@echo "Built binaries:"
	@ls -lh $(DISTDIR)/dmed-*

install-man:
	@echo "Install man page:"
	@echo "  sudo cp docs/dmed.1 /usr/local/share/man/man1/"
	@echo "  mandb"
