# Docker Backend Support for Silo

## Context

Silo currently only runs tools inside Apple Container micro-VMs via the `apple/containerization` framework. This limits usage to macOS 26+ with Apple Silicon. Adding Docker as an alternative backend would:
- Allow usage on any system with Docker Desktop (including Intel Macs, older macOS)
- Provide a fallback when Apple Containerization isn't available
- Make Silo useful for teams with mixed hardware

## Architecture: Backend Protocol

Introduce a `ContainerBackend` protocol at the **runner level** (not VM level). The Apple Containerization API is too low-level (Kernel, initfs, ext4 blocks) to abstract cleanly — instead we abstract at the "run this tool in a container" level, which maps to what `ContainerEngine` already calls.

```swift
public protocol ContainerBackend: Sendable {
    var name: String { get }
    func isAvailable() async throws -> Bool
    func ensureRuntime() async throws
    func pullImage(_ reference: String, cacheForTool: ToolDefinition?) async throws
    func runEphemeral(toolName:tool:command:arguments:projectDir:projectRoot:projectConfig:interactive:) async throws -> Int32
    func runLsp(toolName:tool:projectDir:projectRoot:projectConfig:) async throws -> Int32
}
```

## Module Structure

**New module: `SiloBackend`** — depends only on `SiloConfig` + Foundation (no Containerization dependency).

```
Sources/SiloBackend/
    ContainerBackend.swift         -- protocol definition
    BackendKind.swift              -- enum BackendKind { case apple, docker }
    BackendFactory.swift           -- resolves config → BackendKind, creates backend
    Docker/
        DockerBackend.swift        -- ContainerBackend via `docker` CLI
        DockerCLI.swift            -- Foundation.Process wrapper for docker commands
```

**Apple backend stays in SiloCore** (already depends on Containerization):
```
Sources/SiloCore/Backend/
    AppleVMBackend.swift           -- wraps EphemeralRunner, LspRunner, RuntimeManager
```

### Package.swift Changes
- Add `SiloBackend` target depending on `SiloConfig`
- `SiloCore` gains dependency on `SiloBackend`
- `SiloBackend` does NOT depend on `Containerization`

## Config Changes

### Backend field (stored as raw `String?` to keep SiloConfig backend-agnostic)

**`GlobalConfig`** — add `defaultBackend: String?` (nil = "apple")
**`ToolDefinition`** — add `backend: String?` (nil = use global default)
**`ToolOverride`** — add `backend: String?` (nil = no override)

All optional with nil defaults → existing YAML configs decode without changes.

**Resolution order:** project override → tool definition → global default → `.apple`

### Files to modify:
- `Sources/SiloConfig/Config/GlobalConfig.swift` — add `defaultBackend: String?`
- `Sources/SiloConfig/Config/ToolDefinition.swift` — add `backend: String?`
- `Sources/SiloConfig/Config/ProjectConfig.swift` — add `backend: String?` to `ToolOverride`

## Docker Backend Implementation

**`DockerCLI.swift`** — shell-out wrapper using `Foundation.Process` (same pattern as `RuntimeManager.run()`):
- `run(_ args:)` — runs `docker <args>`, throws on non-zero exit
- `runCapture(_ args:)` — captures stdout
- `isAvailable()` — runs `docker info`

**`DockerBackend.swift`** — maps ToolDefinition → `docker run` flags:

| Silo Concept | Docker Flag |
|---|---|
| `tool.image` | image argument |
| `tool.workdir` | `-w` |
| `tool.env` | `-e KEY=VALUE` |
| `tool.cache` | `-v host:guest` |
| `tool.cpus` | `--cpus` |
| `tool.memoryMB` | `-m <N>m` |
| `tool.ports` | `-p host:guest` |
| `tool.network.hostAccess` | `--add-host host.silo.internal:host-gateway` |
| `projectDir` | `-v projectDir:workdir` |
| `passEnv` | `-e` (from host env) |
| `passFiles` | `-v file:path:ro` |
| interactive | `-it` (when isatty) |

**Skipped/N/A for Docker:**
- `rootfsSizeMB` — Docker manages its own storage
- `RootfsCache` / APFS clonefile — Docker has layer caching
- `buildRootfs` — defer to v2 (use `docker build` / `docker commit`)
- Kernel/initfs/vminitd — not needed
- `VmnetNetwork` — Docker Desktop handles networking
- `mount.mode` / `mount.exclude` — defer, document as Apple-only for now

**LSP support:** `docker run --rm -i` (no `-t`) with `Process.standardInput`/`standardOutput` piped through existing `LspProxy` (already backend-agnostic).

## ContainerEngine Refactoring

`ContainerEngine` becomes a dispatcher:

```swift
public final class ContainerEngine: Sendable {
    private let globalConfig: GlobalConfig

    public func runEphemeral(...) async throws -> Int32 {
        let backend = BackendFactory.resolve(toolName:tool:projectConfig:globalConfig:)
        try await backend.ensureRuntime()
        return try await backend.runEphemeral(...)
    }
}
```

Existing `EphemeralRunner`, `LspRunner`, `RuntimeManager` are untouched internally — `AppleVMBackend` wraps them.

**File:** `Sources/SiloCore/Engine/ContainerEngine.swift`

## CLI Changes

Add `--backend apple|docker` flag to:
- `InstallCommand` — controls which backend pulls the image
- `RunCommand` — overrides resolved backend for this invocation
- `ShellCommand`, `LspCommand`, `SetupCommand` — same

**File:** `Sources/silo/Commands/RunCommand.swift` (and siblings)

## Implementation Phases

### Phase 1: Protocol & Module Setup
1. Create `Sources/SiloBackend/` with `ContainerBackend.swift`, `BackendKind.swift`, `BackendFactory.swift`
2. Update `Package.swift` — add `SiloBackend` target
3. Add `backend: String?` to `GlobalConfig`, `ToolDefinition`, `ToolOverride`

### Phase 2: Apple VM Wrapper
4. Create `Sources/SiloCore/Backend/AppleVMBackend.swift` — thin adapter over existing runners
5. Refactor `ContainerEngine` to use `BackendFactory` + protocol dispatch
6. Verify all existing tests pass (no behavior change)

### Phase 3: Docker Backend
7. Create `DockerCLI.swift` and `DockerBackend.swift`
8. Wire `BackendFactory.create(.docker)` → `DockerBackend`

### Phase 4: CLI Integration
9. Add `--backend` flag to commands
10. `silo install python --backend docker` pulls via Docker, saves backend in config

### Phase 5: Testing
11. Unit tests for Docker argument construction (no Docker needed)
12. Unit tests for `BackendFactory` resolution logic
13. Integration tests guarded by `docker info` availability

## What to Defer (v2)
- Warm mode for Docker (`docker exec` on persistent container)
- `silo setup` / `silo rebuild` for Docker (use `docker build` / `docker commit`)
- `ExecutableDiscovery` via Docker
- `mount.mode` / `mount.exclude` for Docker
- Docker Compose integration
- Linux host support

## Verification
1. `swift test` — existing tests pass unchanged
2. `make sign-debug` — builds successfully with new module
3. `silo install python --backend docker` — pulls image via Docker
4. `silo run python -- python3 -c "print('hello')"` with `backend: docker` in config — runs in Docker container
5. `silo run python --timing -- --version` — works with both backends
6. Verify backend resolution: global default → per-tool → per-project override
