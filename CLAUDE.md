# Silo — Claude Code Guide

## Table of Contents

- [Project Overview](#project-overview)
- [Quick Reference](#quick-reference)
- [Build & Test](#build--test)
- [Project Structure](#project-structure)
- [Architecture](#architecture)
  - [Package Architecture](#package-architecture)
  - [Swift FFI Bridge](#swift-ffi-bridge)
  - [Execution Flow](#execution-flow)
  - [Rootfs Caching](#rootfs-caching)
- [Runtime Directory](#runtime-directory)
- [Configuration](#configuration)
- [Key Patterns & Conventions](#key-patterns--conventions)
- [Troubleshooting](#troubleshooting)
- [Bootstrap Dependency Chain](#bootstrap-dependency-chain)
- [Known Issues](#known-issues)
- [Roadmap](#roadmap)

## Project Overview

Silo is a CLI tool that runs development tools (Python, Node.js, Rust, Go, Deno, and more) inside isolated Apple Container micro-VMs. Each invocation is sandboxed with no access to SSH keys, cloud credentials, or other sensitive host data.

Built on [Apple Containerization](https://github.com/apple/containerization) (lightweight VMs with their own Linux kernel, not Docker-style process namespaces). Requires Apple Silicon, macOS 26+.

The main binary is Go. A Swift dynamic library (`libSiloBridge.dylib`) bridges Go to Apple's Containerization framework via cgo + C FFI.

Current version: **0.4.0**

## Quick Reference

```bash
# Build
make bridge                          # build Swift bridge dylib (debug)
make build                           # build Go binary (debug), links bridge
make sign-debug                      # build + codesign with entitlements
make install                         # release build + sign + install to /usr/local/bin

# Test
make test                            # go test ./... with CGO_LDFLAGS set

# Run (after sign-debug or install)
silo list                            # show installed tools
silo list --available                # show all registry tools
silo install python@3.12             # install a specific version (globally)
silo run python --timing -- -c "print('hello')"
silo config ports add node 3000:3000 # add port forwarding to .siloconf
silo config network allow node '*.npmjs.org'  # allow a domain
silo config show                      # show merged project config

# Project-local pinning (asdf/pyenv style — edits .siloconf; does not install)
silo use python@3.12                 # pin this project to python 3.12
silo use node                        # pin default version of node for this project
silo use --global python@3.12        # pin in ~/.silo/siloconf instead
silo unuse python                    # unpin from .siloconf

# Project lifecycle (per-project reconcile + disk reclamation)
silo sync                             # reconcile env to .siloconf (install missing tools, warm rootfs cache)
                                      # `silo pull` / `silo apply` remain as deprecated aliases (removed in 0.6.0)
silo clean                            # free rootfs cache + per-tool caches + stale VMs for this project
silo clean --rootfs-only              # narrow to rootfs cache only
silo clean --force                    # non-interactively remove shared artifacts too

# Persistent customization (was `setup` + `rebuild`)
silo build node -- npm i -g typescript  # bake a persistent rootfs on top of the tool's image
silo build node --rerun                 # re-run the stored build script
silo build --all --rerun                # refresh every tool with a stored script
silo build node --remove                # delete the stored rootfs
                                        # `silo setup` / `silo rebuild` remain as deprecated aliases

# Diagnostics (was `status` — now split)
silo doctor                           # runtime readiness: kernel, initfs
silo current                          # installed tools + active project overrides
silo current python                   # effective tool definition (merged with .siloconf overrides)

# Shell integration
silo shellenv                         # print the shell init for ~/.silo/bin; `eval "$(silo shellenv)"`
silo shellenv fish                    # force fish syntax (default: detect from $SHELL)

# Global cleanup
silo prune                            # one-shot: rootfs GC + per-tool caches + orphan OCI blobs
silo cache report                     # summarise ~/.silo disk usage by bucket
silo cache list                       # list rootfs cache entries (raw/zstd, last used)
silo cache gc                         # LRU-evict rootfs entries over policy cap
silo cache gc --images                # also GC orphan OCI layer blobs
silo cache gc --tool-caches           # also apply policy to pip/npm/cargo caches
silo cache gc --dry-run               # preview eviction without acting
silo cache gc --max-size 2048         # override policy: cap at 2 GiB for this run
silo cache compress                   # zstd-compress cold rootfs entries (~4× smaller; ~2s decompress cost)
silo cache compress --all             # compress every entry regardless of age
silo cache clean --safe --dry-run     # preview orphans not referenced by any installed tool
silo cache clean --safe               # remove them (prompts; --force to skip)
silo cache clean                      # nuke all rootfs cache + container state (original behavior)
```

Cache policy (configurable in `.siloconf` or `~/.silo/siloconf`; defaults apply if absent):

```yaml
cache:
  rootfs:
    maxSizeMB: 8192        # LRU eviction above this cap
    maxAgeDays: 60         # entries untouched beyond this are evicted
  tools:
    maxSizeMB: 4096        # per-tool package cache cap (per mount)
    maxAgeDays: 30         # file-level eviction by atime inside each mount
    perMount:
      rust/cargo: 8192     # override for specific mounts
```

Auto-GC runs once per process at the top of `silo run` — users passively reclaim disk just by using silo. `silo uninstall <tool>` also frees the rootfs cache entry and (if not shared) deletes the OCI image + orphan blobs.

The binary MUST be codesigned with `silo.entitlements` (`com.apple.security.virtualization`) or macOS will SIGKILL it. Always use `make sign-debug` or `make install`, never raw `go build` output directly.

## Build & Test

The build has two stages: Swift bridge first, then Go binary (linked against the dylib via cgo).

| Command | What it does |
|---|---|
| `make bridge` | Build Swift bridge dylib (debug) |
| `make bridge-release` | Build Swift bridge dylib (release) |
| `make build` | Build Go binary (debug), `CGO_LDFLAGS` points at debug dylib |
| `make release` | Build Go binary (release) |
| `make sign-debug` | Debug build + codesign **(use this for development)** |
| `make sign` | Release build + codesign |
| `make install` | Release build + sign + install to /usr/local/bin + /usr/local/lib/silo |
| `make release-bundle PREFIX=<dir> [VERSION=<tag>]` | Build + sign into `$PREFIX/{bin,lib/silo}` (Homebrew-facing) |
| `make test` | Run `go test ./...` with `CGO_LDFLAGS` and `DYLD_LIBRARY_PATH` |
| `make test-vm` | Run end-to-end VM integration tests (`tests/integration/run-all.sh`) |
| `make clean` | Clean bin/, swift-bridge build, go cache |

**Dependencies:**
- Go toolchain (≥ 1.25; cgo required)
- Swift 6.2+ (for building the bridge dylib only)
- Apple Containerization framework (pulled by SPM for the bridge)

**Environment variables / Make variables:**
- `CGO_LDFLAGS` — `-L<bridge-build-dir> -lSiloBridge -Wl,-rpath,<dir>` — embeds the rpath at link time so the binary resolves the dylib without `DYLD_LIBRARY_PATH`.
- `DYLD_LIBRARY_PATH` — set only for `go test` so unit tests can dlopen the bridge.
- `LIB_INSTALL_DIR` — overridable install location for `libSiloBridge.dylib`. Defaults to `/usr/local/lib/silo`. Homebrew passes `LIB_INSTALL_DIR=#{libexec}/silo` via the `release-bundle` target.
- `VERSION` — optional. When set, baked into the binary via `-ldflags "-X github.com/rchekalov/silo/internal/version.Version=..."` so `silo --version` matches the release tag.

**Release & distribution:**
- `.github/workflows/ci.yml` — PR + push CI: `make bridge && make test && make sign-debug` on `macos-latest` (arm64). Uploads the signed debug binary as an artifact.
- `.github/workflows/release.yml` — on tag `v*`: verifies build, creates a GitHub Release, computes the source-tarball SHA-256, bumps `Formula/silo.rb` in `rchekalov/homebrew-silo` via the `TAP_GITHUB_TOKEN` secret.
- `scripts/homebrew/silo.rb` — seed formula for the tap repo. Homebrew installs from source via `make release-bundle PREFIX=#{prefix} VERSION=#{version}`.
- `docs/homebrew-distribution.md` — end-to-end setup steps (create tap repo, add secret, tag, validate).

## Project Structure

```
cmd/silo/
  main.go                            # Entry point, argv[0] shim detection, arg transforms

internal/
  bridge/                            # cgo wrapper around libSiloBridge.dylib
    manager.go                       # Manager handle, image ops, container factories
    container.go                     # Container lifecycle, exec, wait, stop
    image.go                         # Image pull, lookup, orphan cleanup
    process.go                       # Process handle, stdio, resize
    terminal.go                      # TTY size queries
    callbacks.go                     # C→Go callback bridge (channels)
    cgo.go                           # build constraints + CGO includes
    silo_bridge.h                    # C header mirrored from swift-bridge
    marshal.go                       # C struct marshalling helpers
    types.go                         # opaque handle types

  commands/                          # cobra subcommands
    root.go                          # root command + addCommand registry
    run.go                           # silo run <tool> -- <args>
    shell.go                         # silo shell <tool>
    install.go                       # silo install <tool>[@<version>]
    uninstall.go                     # silo uninstall <tool>
    use.go                           # silo use <tool>[@<version>] / silo unuse (edit .siloconf)
    list.go                          # silo list [--available]
    current.go                       # silo current [tool] (effective config after overrides)
    doctor.go                        # silo doctor (runtime readiness)
    status.go                        # silo status (deprecated; forwards to doctor + current)
    build.go                         # silo build <tool> [-- cmd] [--rerun] [--remove] [--all]
                                     #   (absorbs the old `setup`/`rebuild` commands as aliases)
    cache.go                         # silo cache list|report|gc|compress|clean
    prune.go                         # silo prune (global GC facade)
    config.go                        # silo config ports|network|show (+ deprecated add-port/remove-port)
    reset.go                         # silo reset
    init.go                          # silo init (generate .siloconf)
    shim.go                          # silo shim add|remove|list
    lsp.go                           # silo lsp <tool>
    ide.go                           # silo ide <vscode|zed|neovim>
    pull.go                          # silo sync (with pull/apply aliases — reconcile env to .siloconf)
    clean.go                         # silo clean (project-scoped reclaim)

  config/
    global.go                        # ~/.silo/config.yaml (installed tools)
    project.go                       # .siloconf walk-up + merge + overrides
    tool.go                          # ToolDefinition struct + defaults
    cache_policy.go                  # CachePolicy from .siloconf

  engine/
    engine.go                        # ContainerEngine (orchestrator)
    ephemeral.go                     # Fresh VM per invocation, rootfs cache fast path
    lsp.go                           # LSP server in VM with pipe-based stdio proxy
    runtime.go                       # First-run bootstrap (kernel, Swift toolchain, vminitd, initfs)
    reap.go                          # Reap stale silo-* container dirs
    signals.go                       # SIGINT/SIGTERM forwarding into the VM

  cache/
    rootfs.go                        # Cached rootfs ext4 (APFS clonefile)
    compress.go                      # zstd compress/decompress (hot/cold tiers)
    gc.go                            # LRU + age-based eviction
    pkg_cache.go                     # Per-tool package cache GC

  lsp/
    framing.go                       # JSON-RPC framing reader/writer
    proxy.go                         # Bidirectional stdio proxy with path rewriting
    ide_config.go                    # IDE config generator (VS Code, Zed, Neovim)

  network/
    port_forwarder.go                # TCP relay host→vm
    proxy.go                         # HTTP proxy allowlist

  runtime/
    paths.go                         # SiloPaths (static accessors for ~/.silo/ layout)

  shim/
    shim.go                          # Manager (create/remove/conflict-check shims)

  tools/
    registry.go                      # Built-in tool registry loader
    registry.yaml                    # Embedded tool specs (15+ tools)
    detector.go                      # Auto-detect tools from marker files
    discovery.go                     # Shimless executable discovery via image exec
    installer.go                     # Unified install pipeline (config + shims + image + post-install)

  prompter/prompter.go               # Interactive yes/no + tool-list prompts
  errs/errs.go                       # Typed errors (ToolNotInstalled, etc.)
  version/version.go                 # const Version string

swift-bridge/                        # Swift dynamic library (libSiloBridge.dylib)
  Sources/SiloBridge/
    Bridge.swift                     # @_cdecl exports wrapping Apple Containerization APIs
    Config.swift                     # C struct → Swift type converters
    Boxes.swift                      # ARC reference wrappers for opaque handles
  Package.swift                      # SPM manifest (depends on apple/containerization)

silo.entitlements                    # com.apple.security.virtualization (required for VM ops)
Makefile                             # Orchestrates Swift bridge + Go builds + codesigning
```

## Architecture

### Package Architecture

```
cmd/silo (binary)
  └─ internal/commands  (cobra subcommands; thin, delegates to engine/tools/cache)
       └─ internal/engine   (VM orchestration)
            └─ internal/bridge  (cgo wrappers over libSiloBridge.dylib)
                 └─ libSiloBridge.dylib (Swift, loaded at runtime via rpath)
                      └─ Apple Containerization framework
```

- **cmd/silo**: entry point. Detects argv[0] shim dispatch (`python` symlink → `silo run python --shim python`), rewrites tool-shorthand argv (`silo python foo.py` → `silo run python -- foo.py`), and parses passthrough via `_SILO_PASSTHROUGH` env var (\x1F-delimited).
- **internal/commands**: cobra commands. Thin glue — load config, call engine/tools/cache, format output.
- **internal/engine**: `ContainerEngine` orchestrator. `EnsureRuntime` (bootstrap), `RunEphemeral`, `RunLSP`, `RunSetup`, `PullImage`. Wraps bridge calls with config-aware setup (mounts, env, ports, network).
- **internal/bridge**: cgo surface. C callbacks from Swift → Go channels → synchronous-looking Go API. Opaque handle types (`Manager`, `Container`, `Image`, `Process`).

### Swift FFI Bridge

The Swift bridge (`swift-bridge/`) is a dynamic library that wraps Apple's Containerization framework:

- **Bridge.swift** (~600 lines): `@_cdecl` exported functions for manager creation, container lifecycle, image operations, process exec, terminal sizing.
- **Config.swift**: Converts C structs to Swift types (mounts, container config, exec config).
- **Boxes.swift**: Reference-counted wrappers (`ManagerBox`, `ContainerBox`, `ImageBox`, `ProcessBox`) for passing Swift objects through opaque C handles.

All VM operations flow: Go → cgo → Swift bridge → Apple Containerization.

### Execution Flow

**Ephemeral (only mode):** Fresh VM per invocation. Full isolation, ~600ms with rootfs cache.

```
shim (or silo run) → cmd/silo/main.go
  → commands.runRun → engine.ContainerEngine.RunEphemeral
    → internal/engine/ephemeral.go
      → clone cached rootfs (APFS clonefile, instant)
      → bridge.Manager.CreateContainerFromImage (skips OCI unpack)
      → Container.Create → Start → Wait
      → Container.Stop → Manager.Delete
```

**Shim detection (argv[0]):** [cmd/silo/main.go:77-102](cmd/silo/main.go:77) — when invoked as `python` via a shim symlink, resolves the shim to its tool and transforms args to `silo run <tool> --shim <shim> -- <args>`.

**Tool shorthand:** [cmd/silo/main.go:109-151](cmd/silo/main.go:109) — `silo python script.py` is rewritten to `silo run python -- script.py` before cobra parses.

### Rootfs Caching

Without caching, every invocation unpacks OCI layers to ext4 (~25s). With caching, a pre-unpacked rootfs is cloned via APFS copy-on-write (~1ms).

**Cache location:**
- Hot (raw): `~/.silo/rootfs-cache/{digestHex}.ext4`
- Cold (zstd): `~/.silo/rootfs-cache/{digestHex}.ext4.zst`
- LRU sidecar: `~/.silo/rootfs-cache/{digestHex}.lastused`

**Cache key:** Image digest alone (not digest+size, so `rootfsSizeMB` tweaks don't duplicate entries). Tag updates produce a new digest, naturally invalidating stale entries. Legacy `{digestHex}_{size}.ext4` entries are migrated on first touch.

**Hot/cold tiers.** `silo cache compress` demotes old entries to zstd (~4× smaller). On the next cache hit, zstd is decompressed back to raw (promotion) and the fast clonefile path resumes. `cache.Entry` transparently reports both forms; `Has`, `CloneTo`, `RemoveByDigest`, and GC all accept either.

**Flow in ephemeral.Run:**
1. `maintenanceBeforeRun()` runs once per process: migrate legacy cache entries, reap stale `silo-*` container dirs, auto-GC rootfs if over the cap.
2. Get image metadata via `Manager.ImageGet(reference, pull=false)`.
3. If cache hit: `Rootfs.CloneTo()` (APFS clonefile; decompresses zstd tier first if needed) → `Manager.CreateContainerFromImage()`.
4. If cache miss: `Manager.CreateContainerFromRef()` (slow unpack) then `Rootfs.Store()`.

**Key files:** [internal/cache/rootfs.go](internal/cache/rootfs.go), [internal/cache/gc.go](internal/cache/gc.go), [internal/cache/compress.go](internal/cache/compress.go), [internal/cache/pkg_cache.go](internal/cache/pkg_cache.go), [internal/engine/ephemeral.go](internal/engine/ephemeral.go), [internal/engine/reap.go](internal/engine/reap.go).

## Runtime Directory

```
~/.silo/
  config.yaml          # Installed tools (GlobalConfig)
  siloconf             # Global .siloconf (fallback for all projects)
  vmlinux              # Linux kernel
  initfs.ext4          # vminitd init filesystem
  bin/                 # Shim scripts (must be on PATH)
  images/              # OCI image content store
  containers/          # Container rootfs (managed by bridge, transient)
  rootfs-cache/        # Cached unpacked rootfs ext4 files
  builds/              # Global build artifacts (setup rootfs)
  cache/               # Tool caches (pip, npm, cargo, go mod, deno)
  logs/                # Reserved
```

## Configuration

### Global (~/.silo/config.yaml)

```yaml
version: 1
tools:
  python:
    image: docker.io/library/python:3.12-slim
    shims:
      - python
      - python3
      - pip
      - pip3
    cache:
      - guest: /root/.cache/pip
        host: ~/.silo/cache/python/pip
    workdir: /workspace
    env:
      PYTHONDONTWRITEBYTECODE: "1"
    cpus: 2
    memory_mb: 512
    rootfs_size_mb: 2048
```

### Project (.siloconf)

Searched by walking up from cwd. Merged with global siloconf (`~/.silo/siloconf`) — project overrides global.

```yaml
tools: [python, node]      # declares the project's required tools (silo sync uses this)
pass_env: [GITHUB_TOKEN]
pass_files: [.npmrc]
overrides:
  python:
    env:
      PYTHONPATH: /workspace/src
```

The project's tool set is the union of `tools:` and the keys of `overrides:`. `silo sync` uses this set to decide what to install/pull; `silo clean` uses it to decide what artifacts to reclaim.

### Global siloconf (~/.silo/siloconf)

Same format as project `.siloconf`. Applied as fallback when no project-level config exists, or merged under project config.

### ToolDefinition fields

| Field | Go type | Default | Notes |
|---|---|---|---|
| `image` | string | -- | OCI image reference |
| `shims` | []ShimMapping | nil | Entries in ~/.silo/bin/ |
| `cache` | []CacheMount | nil | Persistent host<->guest mounts |
| `workdir` | string | /workspace | Container working directory |
| `env` | map[string]string | nil | Default env vars |
| `cpus` | int | 2 | VM CPU count |
| `memory_mb` | uint64 | 512 | VM memory |
| `rootfs_size_mb` | uint64 | 2048 | Root filesystem size |
| `network` | *NetworkConfig | nil | Host access, proxy allowlist |
| `requires` | []string | nil | Tool dependencies |
| `ports` | []PortMapping | nil | Port forwarding |
| `build_rootfs` | string | "" | Path to built rootfs |
| `build_script` | string | "" | Setup script reference |
| `postInstall` | []string | nil | Registry-level shell commands baked into a persistent `build_rootfs` during `silo install` (e.g. `apt-get install git && npm i -g @anthropic-ai/claude-code`). Executed with `HostAccess` + proxy allowlist dropped so upstream package managers work; runtime keeps the original allowlist. |
| `lsp` | *LspConfig | nil | LSP server config |

## Key Patterns & Conventions

- **License header:** Every source file starts with `// SPDX-License-Identifier: Apache-2.0`
- **CLI layer is thin:** `cmd/silo/main.go` handles shim dispatch + arg rewriting; `internal/commands/` holds cobra commands that only parse args and call into engine/tools/cache.
- **internal/* is the library:** All logic lives under `internal/`, testable independently.
- **Config is yaml.v3:** `GlobalConfig`, `ProjectConfig`, `ToolDefinition` use `yaml:"..."` tags. Same spelling across global + project files.
- **Error handling:** `internal/errs` exports typed constructors (`ToolNotInstalledError`, `Configf`, etc.). Commands return errors; cobra prints them.
- **Concurrency:** goroutines + channels. Bridge callbacks land on a C thread, marshalled into Go channels in `internal/bridge/callbacks.go`.
- **FFI pattern:** cgo callbacks → channel send → synchronous-looking Go API. Handles are `unsafe.Pointer` wrapped in typed structs.
- **Tests:** `*_test.go` next to the file under test. Integration tests live in `tests/integration/*.sh` and are implementation-agnostic (`$SILO_BIN` driven).

## Troubleshooting

### Binary gets SIGKILL (exit code 137)
Only `com.apple.security.virtualization` works with ad-hoc signing. Adding `com.apple.security.hypervisor` or `com.apple.vm.networking` causes macOS to kill the process. Always use the entitlements in `silo.entitlements` as-is.

### "couldn't be saved in the folder containers because a file with the same name already exists"
Stale container directory from a crashed or interrupted run:
```bash
rm -rf ~/.silo/containers/silo-*
```

### Performance debugging
Use `--timing` on the run command:
```bash
silo run python --timing -- --version
```

### Full reset
```bash
rm -rf ~/.silo
```

## Bootstrap Dependency Chain

First `silo install` triggers this chain (one-time, ~5 minutes, all cached at `~/.silo/`):

```
Kata Containers 3.17.0 release
  └─> vmlinux kernel binary

swiftly (Swift version manager)
  └─> Swift 6.3.0
      └─> Static Linux SDK
          └─> vminitd + vmexec (cross-compiled from containerization source)
              └─> cctl (macOS native)
                  └─> initfs.ext4
```

## Known Issues

1. **First `silo install` downloads a ~285 MB prebuilt runtime bundle (~30 s).** The bundle (vmlinux + initfs.ext4) is published as a GitHub Release asset alongside each tagged version. Without network access, `silo` falls back to building from source (~5 min: kernel download + Swift toolchain install + vminitd cross-compile), which auto-clones `apple/containerization` into `~/.silo/.local/containerization`. Both paths cache their output at `~/.silo/`.

2. **Entitlements require ad-hoc signing.** Binary must be codesigned after every build.

## Roadmap

**Next:** Distribution (bootstrap speedup, Homebrew)

**Future:** Docker backend

See `docs/` for detailed design docs.
