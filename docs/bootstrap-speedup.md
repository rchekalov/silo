# Bootstrap Speedup Plan

## Problem

First `silo install` takes ~5 minutes to bootstrap the runtime:
1. Download Linux kernel from Kata Containers (~280MB tarball)
2. Install swiftly + Swift 6.3 snapshot toolchain
3. Download Static Linux SDK (~290MB)
4. Cross-compile vminitd + vmexec from containerization source
5. Build cctl (macOS native)
6. Create initfs.ext4

This is a major onboarding barrier. Users expect `silo install python` to take seconds, not minutes.

## Current State

- All bootstrap artifacts are cached at `~/.silo/` after first run
- Subsequent `silo install` for other tools only pulls OCI images (seconds)
- The heavy cost is cross-compiling vminitd, which requires an entire Swift toolchain + Static Linux SDK
- `docs/ci-prebuilt-images.md` has a CI pipeline plan for prebuilt rootfs images, but doesn't fully address the runtime bootstrap

## Analysis: Where Time Goes

| Step | Time | Size | Can Prebuilt? |
|------|------|------|---------------|
| Download Kata kernel tarball | ~30s | ~280MB | Yes — ship vmlinux directly |
| Install swiftly | ~5s | small | Eliminate — not needed if initfs is prebuilt |
| Install Swift 6.3 snapshot | ~60s | ~1GB | Eliminate |
| Download Static Linux SDK | ~30s | ~290MB | Eliminate |
| Cross-compile vminitd | ~90s | — | Yes — ship prebuilt initfs.ext4 |
| Build cctl | ~30s | — | Eliminate — not used by silo at runtime |
| Create initfs.ext4 | ~10s | ~5MB | Yes — ship prebuilt |
| **Total** | **~5 min** | | |

**Key insight:** If we ship prebuilt `vmlinux` + `initfs.ext4`, the entire Swift toolchain / SDK / cross-compilation chain is eliminated. That's ~4 minutes of the ~5 minute bootstrap.

## Action Items

### Strategy 1: Prebuilt Runtime Bundle (effort: medium, impact: high) — **SHIPPED**

Ship `vmlinux` + `initfs.ext4` as a GitHub Release asset. On first `silo install`:

1. Download `silo-runtime-arm64.tar.gz` from latest release (~285MB compressed)
2. Extract to `~/.silo/vmlinux` and `~/.silo/initfs.ext4`
3. Done — skip the entire Swift toolchain + cross-compilation chain

**Reduces bootstrap from ~5 minutes to ~30 seconds** (download time).

#### Implementation

**RuntimeManager.swift changes:**

Implemented in [internal/engine/runtime.go](../internal/engine/runtime.go):

- `EnsureRuntime()` tries `tryDownloadRuntimeBundle()` before falling back to the source-build path.
- Bundle URL is `https://github.com/rchekalov/silo/releases/download/v<version>/silo-runtime-arm64.tar.gz` + a `.sha256` sidecar.
- Failures (missing asset, checksum mismatch, network error) log a single warning and drop through to the source-build path — never a hard error.
- The source-build fallback auto-clones `apple/containerization@<pinned>` into `~/.silo/.local/containerization`, so it works from anywhere (e.g., a Homebrew-installed silo).
- The release workflow (`.github/workflows/release.yml`) runs the hidden `silo bootstrap-runtime` command on `macos-latest`, packages `vmlinux` + `initfs.ext4`, and attaches the tarball + checksum to each tagged release.

**Build pipeline (GitHub Actions):**

```yaml
# On every release tag, build runtime bundle on macOS Apple Silicon runner
- Build silo, run bootstrap, package vmlinux + initfs.ext4
- Upload as release asset with SHA256 in release notes
```

**Fallback:** If download fails (no network, GitHub down, rate limited), fall back to existing build-from-source path. User sees a message:

```
Downloading prebuilt runtime... failed (network error)
Falling back to building from source (this takes ~5 minutes)
```

### Strategy 2: Prebuilt Rootfs Images (effort: medium, impact: medium)

Already planned in `docs/ci-prebuilt-images.md`. Reduces first tool run from ~25s (OCI unpack) to ~2s (download cached rootfs + APFS clone).

This is additive to Strategy 1 — they address different bottlenecks:
- Strategy 1: runtime bootstrap (vmlinux + initfs)
- Strategy 2: first tool run (rootfs ext4)

### Strategy 3: Better Progress UX (effort: low, impact: low)

Doesn't reduce actual time but reduces perceived pain.

Current bootstrap output is minimal. Improve to show:

```
Bootstrapping silo runtime (one-time setup)...

  [1/3] Downloading Linux kernel...          [=====>    ] 180/280 MB
  [2/3] Installing build tools...            [======    ] Swift SDK
  [3/3] Building VM init system...           [========  ] Compiling vminitd

This takes ~5 minutes on first run. Subsequent installs are fast.
```

Key improvements:
- Download progress bars with MB counters
- Step numbers so users know how far along they are
- Explicit "one-time" messaging
- ETA or elapsed time display

### Strategy 4: Lazy Kernel Download (effort: low, impact: low)

Currently downloads the full Kata Containers tarball (~280MB), then extracts just `vmlinux`. The tarball contains many other files we don't need.

**Optimization:** Host just the extracted `vmlinux` binary as its own release asset. Download ~35MB instead of ~280MB.

This saves ~20 seconds and ~245MB of bandwidth.

## Expected Impact

| Scenario | Before | After (all strategies) |
|----------|--------|----------------------|
| First ever `silo install` | ~5 minutes | ~30 seconds |
| First run of a tool | ~25 seconds | ~2 seconds |
| Subsequent runs | ~1-2 seconds | ~1-2 seconds (no change) |
| Fallback (no network) | ~5 minutes | ~5 minutes (no change, but with better UX) |

## Dependencies

- **CI runner:** Need Apple Silicon macOS 26 runner for building runtime bundle. GitHub-hosted `macos-15` may not support Virtualization.framework — may need self-hosted.
- **GitHub Releases:** Hosting for prebuilt assets. Size limit: 2GB per file (runtime bundle is ~285MB compressed, well within limit).
- **Release build fix:** Ideally the CI pipeline builds release binaries. If release builds are still broken, CI can use debug builds for the bootstrap step (the artifacts are the same — vmlinux and initfs.ext4 don't depend on silo's build configuration).

## Success Criteria

- New users go from `git clone` to `python --version` in under 2 minutes
- Prebuilt download has SHA256 verification
- Fallback to build-from-source works when prebuilt isn't available
- Progress output tells users what's happening and how long it will take
