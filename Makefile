GO       := go
GOPROXY  := https://proxy.golang.org,direct
GOSUMDB  := sum.golang.org

export GOROOT :=
export GOPROXY
export GOSUMDB

.PHONY: build test vet run clean

build:
	$(GO) build ./

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

run: build
	./dmed.exe $(FILE)

clean:
	rm -f dmed dmed.exe
