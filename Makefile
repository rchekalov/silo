.PHONY: build release sign sign-debug install uninstall test test-vm clean bridge bridge-release release-bundle release-tarball \
        lint lint-fix fmt vulncheck security tools-install

SWIFT_BRIDGE_DIR = swift-bridge
BRIDGE_LIB_DEBUG = $(SWIFT_BRIDGE_DIR)/.build/debug/libSiloBridge.dylib
BRIDGE_LIB_RELEASE = $(SWIFT_BRIDGE_DIR)/.build/release/libSiloBridge.dylib
BIN_DEBUG = bin/silo
BIN_RELEASE = bin/silo-release
ENTITLEMENTS = silo.entitlements

GOLANGCI_VERSION = v2.11.4
GOLANGCI_BIN = ./bin/golangci-lint

# Env needed for any Go analysis that typechecks internal/bridge (cgo).
# Mirrors the `test` target — the Swift dylib must exist and be discoverable.
CGO_LINT_ENV = CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug) -lSiloBridge" \
               DYLD_LIBRARY_PATH=$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug)

# Overridable install paths — Homebrew passes its own PREFIX via release-bundle.
INSTALL_DIR ?= /usr/local/bin
LIB_INSTALL_DIR ?= /usr/local/lib/silo

# Optional version override — baked into the binary via ldflags when set.
# Release workflow passes VERSION=<tag>, Homebrew formula passes VERSION=#{version}.
VERSION ?=
VERSION_LDFLAG = $(if $(VERSION),-X github.com/rchekalov/silo/internal/version.Version=$(VERSION))

# --disable-sandbox prevents SwiftPM from trying to wrap manifest evaluation
# and plugins in its own sandbox-exec, which fails when already running inside
# Homebrew's install sandbox. Harmless in non-nested environments.
SWIFT_BUILD_FLAGS = --disable-sandbox

# Build Swift bridge (debug)
bridge:
	cd $(SWIFT_BRIDGE_DIR) && swift build $(SWIFT_BUILD_FLAGS)

# Build Swift bridge (release)
bridge-release:
	cd $(SWIFT_BRIDGE_DIR) && swift build -c release $(SWIFT_BUILD_FLAGS)

# Debug build. Embeds an rpath to the debug dylib so the binary runs without
# DYLD_LIBRARY_PATH after codesigning.
build: bridge
	CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug) -lSiloBridge -Wl,-rpath,$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug)" \
	go build -o $(BIN_DEBUG) ./cmd/silo

# Release build. rpath is @executable_path-relative so the binary is
# relocatable: it resolves libSiloBridge.dylib from <prefix>/lib/silo
# regardless of where <prefix> lives. Works for `make install`
# (/usr/local/bin + /usr/local/lib/silo) and Homebrew
# (/opt/homebrew/Cellar/silo/<ver>/bin + .../lib/silo).
release: bridge-release
	CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/release) -lSiloBridge -Wl,-rpath,@executable_path/../lib/silo" \
	go build -ldflags="-s -w $(VERSION_LDFLAG)" -o $(BIN_RELEASE) ./cmd/silo

# Codesign debug build
sign-debug: build
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(BIN_DEBUG)
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(BRIDGE_LIB_DEBUG)

# Codesign release build
sign: release
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(BIN_RELEASE)
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(BRIDGE_LIB_RELEASE)

# Install to system (uses sudo).
install: release
	sudo install -d $(LIB_INSTALL_DIR)
	sudo install $(BRIDGE_LIB_RELEASE) $(LIB_INSTALL_DIR)/
	sudo install $(BIN_RELEASE) $(INSTALL_DIR)/silo
	sudo codesign --entitlements $(ENTITLEMENTS) --force --sign - $(INSTALL_DIR)/silo
	sudo codesign --entitlements $(ENTITLEMENTS) --force --sign - $(LIB_INSTALL_DIR)/libSiloBridge.dylib

# Homebrew-facing target. Lays the binary + dylib under $(PREFIX), ad-hoc signs
# both. Does not use sudo — Homebrew installs into its own prefix as the user.
# Usage:
#   make release-bundle PREFIX=/path/to/prefix [VERSION=0.4.0]
# Produces:
#   $(PREFIX)/bin/silo
#   $(PREFIX)/lib/silo/libSiloBridge.dylib
release-bundle:
	@if [ -z "$(PREFIX)" ]; then echo "error: PREFIX is required (usage: make release-bundle PREFIX=<dir>)"; exit 2; fi
	$(MAKE) release LIB_INSTALL_DIR=$(PREFIX)/lib/silo VERSION=$(VERSION)
	install -d $(PREFIX)/bin $(PREFIX)/lib/silo
	install $(BIN_RELEASE) $(PREFIX)/bin/silo
	install $(BRIDGE_LIB_RELEASE) $(PREFIX)/lib/silo/libSiloBridge.dylib
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(PREFIX)/bin/silo
	codesign --entitlements $(ENTITLEMENTS) --force --sign - $(PREFIX)/lib/silo/libSiloBridge.dylib

# CI-facing: package an already-built release-bundle into a tarball
# suitable for GitHub Release assets. Upstream caller must have run
# `make release-bundle PREFIX=$PREFIX VERSION=$VERSION` first.
# Usage:
#   make release-tarball PREFIX=<dir> VERSION=<tag> OUT_DIR=<dir>
# Produces:
#   $(OUT_DIR)/silo-$(VERSION)-macos-arm64.tar.gz
#   $(OUT_DIR)/silo-$(VERSION)-macos-arm64.tar.gz.sha256
release-tarball:
	@if [ -z "$(PREFIX)" ]; then echo "error: PREFIX is required"; exit 2; fi
	@if [ -z "$(VERSION)" ]; then echo "error: VERSION is required"; exit 2; fi
	@if [ -z "$(OUT_DIR)" ]; then echo "error: OUT_DIR is required"; exit 2; fi
	mkdir -p $(OUT_DIR)
	tar -czf $(OUT_DIR)/silo-$(VERSION)-macos-arm64.tar.gz -C $(PREFIX) bin lib
	cd $(OUT_DIR) && shasum -a 256 silo-$(VERSION)-macos-arm64.tar.gz \
	  > silo-$(VERSION)-macos-arm64.tar.gz.sha256

uninstall:
	sudo rm -f $(INSTALL_DIR)/silo
	sudo rm -rf $(LIB_INSTALL_DIR)

# Run all unit tests
test: bridge
	CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug) -lSiloBridge" \
	DYLD_LIBRARY_PATH=$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug) \
	go test ./...

# Run VM integration tests (requires signed binary + installed tools)
test-vm: sign-debug
	SILO_BIN=$(abspath $(BIN_DEBUG)) bash tests/integration/run-all.sh

clean:
	rm -rf bin
	cd $(SWIFT_BRIDGE_DIR) && swift package clean
	go clean -cache -testcache 2>/dev/null || true

# --- Lint / format / security ---------------------------------------------

# Install golangci-lint at the pinned version. Idempotent — re-downloads only
# if the binary is missing or its --version doesn't match $(GOLANGCI_VERSION).
$(GOLANGCI_BIN):
	@mkdir -p bin
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
	  | sh -s -- -b ./bin $(GOLANGCI_VERSION)

# One-shot developer bootstrap: linter binary + go tool deps (gosec, govulncheck).
# gosec needs a one-time: `go get -tool github.com/securego/gosec/v2/cmd/gosec@latest`
tools-install: $(GOLANGCI_BIN)
	go mod download

# Lint all packages. bridge target runs first so internal/bridge typechecks.
lint: bridge $(GOLANGCI_BIN)
	$(CGO_LINT_ENV) $(GOLANGCI_BIN) run ./...

# Apply safe auto-fixes from enabled linters.
lint-fix: bridge $(GOLANGCI_BIN)
	$(CGO_LINT_ENV) $(GOLANGCI_BIN) run --fix ./...

# Format in place using the formatters declared in .golangci.yml (gofumpt/gci/goimports).
fmt: $(GOLANGCI_BIN)
	$(GOLANGCI_BIN) fmt ./...

# Vulnerability scan against current go.sum + call graph.
vulncheck: bridge
	$(CGO_LINT_ENV) go tool govulncheck ./...

# Security scan. Advisory by intent — gosec is noisier than govulncheck.
security:
	go tool gosec -quiet -exclude-dir=swift-bridge ./...
