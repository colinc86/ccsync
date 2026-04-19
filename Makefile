BINARY := ccsync
PKG    := ./cmd/ccsync
PREFIX ?= $(HOME)/.local

.PHONY: build test vet install clean release-dry release-snapshot

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "installed: $(PREFIX)/bin/$(BINARY)"

clean:
	rm -f $(BINARY)
	rm -rf dist/

release-dry:
	@command -v goreleaser >/dev/null || { echo "goreleaser not installed — brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean --skip=publish

release-snapshot:
	@command -v goreleaser >/dev/null || { echo "goreleaser not installed — brew install goreleaser"; exit 1; }
	goreleaser build --snapshot --clean
