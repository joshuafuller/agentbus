# agentbus — build & dev tasks
BIN        := agentbus
PKG        := ./cmd/agentbus
DIST       := dist
PLATFORM   = $(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH)
CARGO      ?= cargo
GO         ?= go
LDFLAGS    := -X main.version=$(shell git describe --tags --always 2>/dev/null || echo dev) \
              -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) \
              -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.DEFAULT_GOAL := build

## build: compile the binary for the host platform
.PHONY: build
build: helper
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

## test: run the full suite with the race detector
.PHONY: test
test: helper
	AGENTBUS_IROH_BIN="$(CURDIR)/agentbus-iroh" $(GO) test -race ./...

## vet: run go vet
.PHONY: vet
vet:
	$(GO) vet ./...

## check: vet + test (what CI runs)
.PHONY: check
check: vet test
	$(CARGO) fmt --manifest-path transport/iroh/Cargo.toml --check
	$(CARGO) clippy --locked --manifest-path transport/iroh/Cargo.toml -- -D warnings

## scan: run gitleaks over history and the working tree
.PHONY: scan
scan:
	gitleaks git -c .gitleaks.toml --redact --no-banner
	gitleaks dir -c .gitleaks.toml --redact --no-banner

## hooks: enable the local pre-commit secret scan
.PHONY: hooks
hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit secret scanning enabled"

## install: build and install to ~/.local/bin
.PHONY: install
install: build
	mkdir -p "$(HOME)/.local/bin"
	install -m755 $(BIN) agentbus-iroh "$(HOME)/.local/bin/"
	@echo "installed to $(HOME)/.local/bin/$(BIN)"

## helper: build the pinned official Rust Iroh bridge
.PHONY: helper
helper:
	RUSTFLAGS="$(RUSTFLAGS) --remap-path-prefix=$(HOME)=build --remap-path-prefix=$(CURDIR)=agentbus" $(CARGO) build --release --locked --manifest-path transport/iroh/Cargo.toml
	cp transport/iroh/target/release/agentbus-iroh ./agentbus-iroh

## release: package both executables for this native platform
# Run on each supported Linux/macOS architecture; CI supplies the native runners.
.PHONY: release
release: build
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(BIN) $(PKG)
	tar -czf $(DIST)/agentbus-$(PLATFORM).tar.gz agentbus agentbus-iroh
	cd $(DIST) && if command -v sha256sum >/dev/null 2>&1; then sha256sum agentbus-*.tar.gz; else shasum -a 256 agentbus-*.tar.gz; fi > SHA256SUMS

## docker-check: vet + race tests + build in a pinned container (reproducible CI)
.PHONY: docker-check
docker-check:
	docker build --target builder -t agentbus-build .

## docker-image: build the minimal runtime image (agentbus:local)
.PHONY: docker-image
docker-image:
	docker build -t agentbus:local .

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -rf $(BIN) agentbus-iroh $(DIST)

## help: list targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
