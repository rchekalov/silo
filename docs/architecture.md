# Silo Architecture (v0.4.0 — Go)

## Overview

Silo runs development tools inside isolated Apple Container micro-VMs. The main binary is Go; a Swift dynamic library bridges to Apple's Containerization framework via cgo + C FFI.

```
User → silo CLI (Go)
         → internal/commands (cobra)
              → internal/engine (VM orchestration)
                   → internal/bridge (cgo)
                        → libSiloBridge.dylib (Swift)
                             → Apple Containerization framework
                                  → Lightweight Linux VM
```

## Package Architecture

### cmd/silo (binary)

Thin CLI layer using cobra. Responsibilities:
- argv[0] shim detection: when invoked as `python`, transforms to `silo run --shim python python <args...>`
- Tool shorthand: `silo python foo.py` → `silo run python foo.py`
- Shim shorthand: `silo npx foo` → `silo run --shim npx node foo`
- Docker-style positional split for `silo run` / `silo build`: silo flags before the tool name; everything after the tool is the inner command, forwarded via `_SILO_PASSTHROUGH` (`\x1F`-delimited). Known silo flags appearing after the tool are hoisted to the front so `silo build node --remove` keeps working. The legacy `--` separator is still accepted: if `--` appears anywhere in argv, the older strip-after-`--` path takes over.
- Terminal restoration on startup (cooked mode)
- Delegates all logic to `internal/` packages

### internal/ (library)

All business logic, independently testable:

| Package | Purpose |
|---------|---------|
| `config/global.go` | `~/.silo/config.yaml` — installed tools |
| `config/project.go` | `.siloconf` walk-up + global siloconf merge |
| `config/tool.go` | Tool spec: image, shims, cache, network, LSP |
| `config/cache_policy.go` | Configurable rootfs + tools cache policy |
| `cache/rootfs.go` | APFS clonefile-based rootfs cache |
| `cache/compress.go` | zstd hot/cold tiers |
| `cache/gc.go` | LRU + age-based eviction |
| `cache/pkg_cache.go` | Per-tool package cache policy application |
| `engine/engine.go` | `ContainerEngine` orchestrator: `EnsureRuntime`, `RunEphemeral`, `RunLSP`, `RunSetup`, `PullImage` |
| `engine/ephemeral.go` | Fresh VM per invocation with rootfs cache fast path |
| `engine/lsp.go` | LSP server in VM with pipe-based stdio proxy |
| `engine/runtime.go` | First-run bootstrap (kernel, Swift toolchain, vminitd) |
| `engine/reap.go` | Reap stale `silo-*` container dirs |
| `engine/signals.go` | SIGINT/SIGTERM forwarding into the VM |
| `runtime/paths.go` | Path helpers for `~/.silo/` layout |
| `shim/shim.go` | Shim manager: create/remove/conflict-check shim scripts |
| `tools/registry.go` + `registry.yaml` | Embedded tool registry (15+ tools) |
| `tools/detector.go` | Marker-file-based tool auto-detection (for `silo init`) |
| `tools/discovery.go` | Shimless executable discovery via image exec |
| `tools/installer.go` | Unified install pipeline (config + shims + image + post-install) |
| `lsp/framing.go` + `lsp/proxy.go` | JSON-RPC framing + path-rewriting proxy |
| `lsp/ide_config.go` | IDE config generation (VS Code, Zed, Neovim) |
| `network/port_forwarder.go` | TCP relay host→vm |
| `network/proxy.go` | HTTP proxy allowlist |
| `prompter/prompter.go` | Interactive yes/no + tool-list prompts |
| `errs/errs.go` | Typed error constructors |
| `version/version.go` | const Version string |

### internal/bridge (cgo wrapper)

Converts C callback-based FFI into synchronous-looking Go using channels:

- `Manager` — create/delete containers, image operations
- `Container` — create/start/stop/wait/exec/resize
- `Image` — digest query, orphan blob cleanup
- `Process` — start/wait/resize
- `ContainerConfig`, `ExecConfig`, `MountSpec` — builders marshalled via `marshal.go`

The C header `silo_bridge.h` mirrors the Swift `@_cdecl` signatures. `callbacks.go` marshalls C callbacks into Go channels via `//export` functions.

### swift-bridge (dynamic library)

Swift library wrapping Apple's Containerization framework:
- `Bridge.swift` (~600 lines) — `@_cdecl` exports for all VM operations
- `Config.swift` — C struct → Swift type converters
- `Boxes.swift` — `ManagerBox`, `ContainerBox`, `ImageBox`, `ProcessBox` (ARC wrappers for opaque handles)
- Depends on: `apple/containerization >= 0.1.0`

## Execution Flow

### Ephemeral execution (silo run)

```
silo run python -c "print('hello')"

1. Load GlobalConfig → find python tool definition
2. Find project .siloconf (walk-up) + merge global siloconf
3. engine.RunEphemeral
   a. bridge.Manager.ImageGet(reference, pull=false)
   b. Check rootfs cache for digest match
   c. If hit: Rootfs.CloneTo() via APFS clonefile (~1ms)
      If miss: Manager.CreateContainerFromRef() (OCI unpack ~25s), then Rootfs.Store()
   d. Build container config (cpus, memory, mounts, env, network, DNS)
   e. Container.Create() → Start() → Wait()
   f. Container.Stop() → Manager.Delete()
4. Return exit code
```

### Rootfs caching

**Cache key:** image digest (hex). Tag updates produce a new digest → automatic invalidation.
**Location:** `~/.silo/rootfs-cache/{digestHex}.ext4` (raw) or `.ext4.zst` (cold tier).

Cache populated during `silo install` (eager) or first `silo run` (lazy fallback).

### Config resolution

```
Tool defaults (from registry.yaml)
  ↓ overridden by
Global siloconf (~/.silo/siloconf)
  ↓ overridden by
Project .siloconf (walk-up from cwd)
  ↓ merged into
Final ToolDefinition used for execution
```

## Built-in Tool Registry

Embedded `internal/tools/registry.yaml` with 15+ tools:

| Tool | Image | Shims | LSP |
|------|-------|-------|-----|
| python | python:3.12-slim | python, python3, pip, pip3 | pyright |
| node | node:22-slim | node, npm, npx | typescript-language-server |
| rust | rust:1.83-slim | cargo, rustc, rustup | rust-analyzer |
| go | golang:1.23 | go | gopls |
| deno | denoland/deno | deno | deno lsp |
| playwright | mcr.microsoft.com/playwright | npx | — |
| cypress | cypress/included | npx | — |
| psql | postgres:17 | psql, pg_dump, pg_restore | — |
| jupyter | jupyter/scipy-notebook | jupyter | — |
| aws-cli | amazon/aws-cli | aws | — |
| claude-code | node:22-slim | claude | — |

User overrides via `~/.silo/registry.yaml` (same format, takes precedence).

## Network Model

Default: no network access (full isolation).

Per-tool configuration:
- `hostAccess: true` — enables networking with `host.silo.internal` DNS pointing to host
- `proxy.allowlist` — restrict outbound to specific domains (wildcard support)
- Container gets DNS resolver at gateway IP
- Port forwarding via `ports` config

## Runtime Directory

```
~/.silo/
  config.yaml          # Installed tools
  siloconf             # Global .siloconf
  vmlinux              # Linux kernel (Kata Containers)
  initfs.ext4          # vminitd init filesystem
  bin/                 # Shim scripts
  images/              # OCI image store
  containers/          # Transient container rootfs
  rootfs-cache/        # Cached rootfs ext4 (our optimization)
  builds/              # Global setup rootfs
  cache/               # Tool caches (pip, npm, cargo, etc.)
  logs/                # Reserved
```

## Build System

Two-phase build orchestrated by Makefile:

1. **Swift bridge:** `swift build` in `swift-bridge/` → `libSiloBridge.dylib`
2. **Go binary:** `go build ./cmd/silo` with `CGO_LDFLAGS="-L<bridge-build-dir> -lSiloBridge -Wl,-rpath,<dir>"` → `bin/silo`
3. **Codesign:** `codesign --entitlements silo.entitlements` (required for VM ops)

The `CGO_LDFLAGS` the Makefile exports embeds an rpath at link time, so the binary resolves the Swift dylib without needing `DYLD_LIBRARY_PATH` at runtime. For tests, the Makefile also sets `DYLD_LIBRARY_PATH` so `go test` binaries can dlopen the bridge.
