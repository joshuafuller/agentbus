# agentbus — build & dev tasks
BIN        := agentbus
PKG        := ./cmd/agentbus
DIST       := dist
PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
GO         ?= go
LDFLAGS    := -X main.version=$(shell git describe --tags --always) \
              -X main.commit=$(shell git rev-parse --short HEAD) \
              -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.DEFAULT_GOAL := build

## build: compile the binary for the host platform
.PHONY: build
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

## test: run the full suite with the race detector
.PHONY: test
test:
	$(GO) test -race ./...

## vet: run go vet
.PHONY: vet
vet:
	$(GO) vet ./...

## check: vet + test (what CI runs)
.PHONY: check
check: vet test

## scan: run gitleaks over history and the working tree
.PHONY: scan
scan:
	gitleaks git -c .gitleaks.toml --redact --no-banner
	gitleaks dir -c .gitleaks.toml --no-banner

## hooks: enable the local pre-commit secret scan
.PHONY: hooks
hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit secret scanning enabled"

## install: build and install to ~/.local/bin
.PHONY: install
install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/$(BIN)
	@echo "installed to $(HOME)/.local/bin/$(BIN)"

## release: cross-compile stripped binaries for all platforms into dist/
.PHONY: release
release:
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "building $(DIST)/$(BIN)-$$os-$$arch"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DIST)/$(BIN)-$$os-$$arch $(PKG); \
	done

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
	rm -rf $(BIN) $(DIST)

## help: list targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
