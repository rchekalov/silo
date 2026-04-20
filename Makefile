.PHONY: build release sign sign-debug install uninstall test test-vm clean bridge bridge-release release-bundle

SWIFT_BRIDGE_DIR = swift-bridge
BRIDGE_LIB_DEBUG = $(SWIFT_BRIDGE_DIR)/.build/debug/libSiloBridge.dylib
BRIDGE_LIB_RELEASE = $(SWIFT_BRIDGE_DIR)/.build/release/libSiloBridge.dylib
BIN_DEBUG = bin/silo
BIN_RELEASE = bin/silo-release
ENTITLEMENTS = silo.entitlements

# Overridable install paths — Homebrew passes its own PREFIX via release-bundle.
INSTALL_DIR ?= /usr/local/bin
LIB_INSTALL_DIR ?= /usr/local/lib/silo

# Optional version override — baked into the binary via ldflags when set.
# Release workflow passes VERSION=<tag>, Homebrew formula passes VERSION=#{version}.
VERSION ?=
VERSION_LDFLAG = $(if $(VERSION),-X github.com/rchekalov/silo/internal/version.Version=$(VERSION))

# Build Swift bridge (debug)
bridge:
	cd $(SWIFT_BRIDGE_DIR) && swift build

# Build Swift bridge (release)
bridge-release:
	cd $(SWIFT_BRIDGE_DIR) && swift build -c release

# Debug build. Embeds an rpath to the debug dylib so the binary runs without
# DYLD_LIBRARY_PATH after codesigning.
build: bridge
	CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug) -lSiloBridge -Wl,-rpath,$(abspath $(SWIFT_BRIDGE_DIR)/.build/debug)" \
	go build -o $(BIN_DEBUG) ./cmd/silo

# Release build. rpath targets $(LIB_INSTALL_DIR) so the produced binary
# resolves the dylib from wherever it's installed. Override LIB_INSTALL_DIR
# to retarget (e.g., Homebrew passes LIB_INSTALL_DIR=#{libexec}/silo).
release: bridge-release
	CGO_LDFLAGS="-L$(abspath $(SWIFT_BRIDGE_DIR)/.build/release) -lSiloBridge -Wl,-rpath,$(LIB_INSTALL_DIR)" \
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
