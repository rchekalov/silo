# CI Pipeline: Prebuilt Rootfs Images

## Problem

The first time a user runs a tool (e.g., `python --version`), Silo must unpack OCI image layers into an ext4 filesystem. This takes ~25 seconds. The rootfs cache (APFS clonefile) eliminates this cost on subsequent runs, but the first run is still slow.

Additionally, the first-ever `silo install` must bootstrap the entire runtime: download the kernel, install swiftly + Swift toolchain + Static Linux SDK, cross-compile vminitd, and build cctl. This takes ~5 minutes.

## Solution

Use GitHub Actions to prebuild:
1. **Rootfs ext4 images** for every supported tool — users download the cached rootfs instead of unpacking OCI layers
2. **Runtime bundle** — kernel + initfs.ext4 as a single downloadable archive

This reduces first-run time from ~30 seconds to ~2 seconds (download cached rootfs + APFS clone).

## Architecture

```
GitHub Actions (scheduled + on release)
  │
  ├── Build runtime bundle
  │   ├── Download kernel from Kata Containers
  │   ├── Cross-compile vminitd (using swiftly + Static Linux SDK)
  │   ├── Build cctl, create initfs.ext4
  │   └── Package: silo-runtime-arm64.tar.gz
  │       ├── vmlinux
  │       └── initfs.ext4
  │
  ├── Build rootfs images (one per tool)
  │   ├── Pull OCI image (e.g., python:3.12-slim)
  │   ├── Unpack into ext4 with correct rootfs size
  │   ├── Record image digest in manifest
  │   └── Package: silo-rootfs-python-3.12-slim.tar.gz
  │       ├── <digest>_<size>.ext4
  │       └── manifest.json
  │
  └── Upload as GitHub Release artifacts
      ├── silo-runtime-arm64.tar.gz
      ├── silo-rootfs-python-3.12-slim.tar.gz
      ├── silo-rootfs-node-22-slim.tar.gz
      ├── silo-rootfs-rust-1.83-slim.tar.gz
      ├── silo-rootfs-golang-1.23-alpine.tar.gz
      ├── silo-rootfs-deno-alpine.tar.gz
      └── manifest.json (all digests + checksums)
```

## GitHub Actions Workflow

### Triggers
- **On release tag** (`v*`) — build for the release
- **Weekly schedule** — rebuild to pick up base image updates (security patches)
- **Manual dispatch** — for ad-hoc rebuilds

### Runner requirements
- **macOS runner** (Apple Silicon) — required for Apple Containers / Virtualization.framework
- GitHub-hosted `macos-15` or self-hosted M-series runner
- Note: GitHub-hosted macOS runners may not support Virtualization.framework. If so, use a self-hosted runner on an M-series Mac with macOS 26.

### Workflow outline

```yaml
name: Build Prebuilt Images

on:
  push:
    tags: ['v*']
  schedule:
    - cron: '0 6 * * 1'  # Weekly Monday 6am UTC
  workflow_dispatch:
    inputs:
      tools:
        description: 'Comma-separated tools to rebuild (or "all")'
        default: 'all'

jobs:
  build-runtime:
    runs-on: macos-15  # or self-hosted Apple Silicon
    steps:
      - uses: actions/checkout@v4

      - name: Build silo
        run: make release

      - name: Bootstrap runtime
        run: |
          ./bin/silo install python  # triggers full bootstrap
          # Runtime artifacts are now at ~/.silo/vmlinux and ~/.silo/initfs.ext4

      - name: Package runtime bundle
        run: |
          mkdir -p dist
          tar czf dist/silo-runtime-arm64.tar.gz \
            -C ~/.silo vmlinux initfs.ext4

      - name: Upload runtime artifact
        uses: actions/upload-artifact@v4
        with:
          name: silo-runtime-arm64
          path: dist/silo-runtime-arm64.tar.gz

  build-rootfs:
    needs: build-runtime
    runs-on: macos-15
    strategy:
      matrix:
        tool:
          - { name: python, image: "docker.io/library/python:3.12-slim" }
          - { name: node, image: "docker.io/library/node:22-slim" }
          - { name: rust, image: "docker.io/library/rust:1.83-slim" }
          - { name: go, image: "docker.io/library/golang:1.23-alpine" }
          - { name: deno, image: "docker.io/denoland/deno:alpine" }
    steps:
      - uses: actions/checkout@v4

      - name: Download runtime
        uses: actions/download-artifact@v4
        with:
          name: silo-runtime-arm64

      - name: Install runtime
        run: |
          mkdir -p ~/.silo
          tar xzf silo-runtime-arm64.tar.gz -C ~/.silo/

      - name: Build silo
        run: make release

      - name: Pull and cache rootfs
        run: |
          ./bin/silo install ${{ matrix.tool.name }}
          # Rootfs is now cached at ~/.silo/rootfs-cache/<digest>_<size>.ext4

      - name: Package rootfs
        run: |
          mkdir -p dist
          # Find the cached ext4 and create manifest
          CACHE_FILE=$(ls ~/.silo/rootfs-cache/*.ext4 | head -1)
          DIGEST=$(basename "$CACHE_FILE" .ext4 | cut -d_ -f1)
          SIZE=$(basename "$CACHE_FILE" .ext4 | cut -d_ -f2)
          echo "{\"tool\": \"${{ matrix.tool.name }}\", \"image\": \"${{ matrix.tool.image }}\", \"digest\": \"sha256:${DIGEST}\", \"rootfsSizeInBytes\": ${SIZE}}" > manifest.json
          tar czf "dist/silo-rootfs-${{ matrix.tool.name }}.tar.gz" \
            -C ~/.silo/rootfs-cache "$(basename $CACHE_FILE)" \
            -C "$(pwd)" manifest.json

      - name: Upload rootfs artifact
        uses: actions/upload-artifact@v4
        with:
          name: silo-rootfs-${{ matrix.tool.name }}
          path: dist/silo-rootfs-${{ matrix.tool.name }}.tar.gz

  release:
    needs: [build-runtime, build-rootfs]
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: dist

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          files: dist/**/*.tar.gz
          generate_release_notes: true
```

## Client-Side Integration

### RuntimeManager changes

During `ensureRuntime()`, before falling back to the build-from-source path:

```swift
// Try to download prebuilt runtime bundle
func downloadPrebuiltRuntime() async throws -> Bool {
    let releaseURL = "https://github.com/rchekalov/silo/releases/latest/download/silo-runtime-arm64.tar.gz"
    // Download, extract to ~/.silo/vmlinux and ~/.silo/initfs.ext4
    // Return true if successful
}
```

### ToolInstaller changes

During `install()`, after pulling the OCI image:

```swift
// Try to download prebuilt rootfs cache
func downloadPrebuiltRootfs(tool: String) async throws -> Bool {
    let releaseURL = "https://github.com/rchekalov/silo/releases/latest/download/silo-rootfs-\(tool).tar.gz"
    // Download, extract manifest.json, verify digest matches pulled image
    // Place ext4 in ~/.silo/rootfs-cache/
    // Return true if successful (digest matched)
}
```

### Digest verification

The prebuilt rootfs is keyed by image digest. If the upstream image has been updated since the CI build, the digest won't match and Silo falls back to local unpacking. This ensures:
- No stale/outdated rootfs images are used
- Security patches in base images are always picked up
- Weekly CI rebuilds keep prebuilts fresh

## Manifest Format

Each rootfs tarball includes a `manifest.json`:

```json
{
  "tool": "python",
  "image": "docker.io/library/python:3.12-slim",
  "digest": "sha256:abc123...",
  "rootfsSizeInBytes": 2147483648,
  "builtAt": "2026-04-04T12:00:00Z",
  "siloVersion": "0.2.0"
}
```

A top-level release manifest lists all available prebuilts:

```json
{
  "version": "0.2.0",
  "runtime": {
    "url": "silo-runtime-arm64.tar.gz",
    "sha256": "...",
    "kernel": "kata-3.17.0",
    "initfs": "containerization-0.26.5"
  },
  "tools": [
    {
      "name": "python",
      "url": "silo-rootfs-python.tar.gz",
      "sha256": "...",
      "imageDigest": "sha256:abc123..."
    }
  ]
}
```

## Expected Impact

| Operation | Before | After |
|---|---|---|
| First `silo install` (full bootstrap) | ~5 minutes | ~30 seconds (download runtime bundle) |
| First tool run (OCI unpack) | ~25 seconds | ~2 seconds (download + APFS clone) |
| Subsequent tool runs (cache hit) | ~2 seconds | ~2 seconds (no change, already fast) |

## Considerations

- **Runner availability:** Virtualization.framework support on GitHub-hosted macOS runners is verified before each build via a pre-flight smoke check. A self-hosted Apple Silicon runner is a fallback if the hosted runner ever drops that capability.
- **Image size:** Prebuilt rootfs ext4 files are ~2 GB uncompressed per tool, ~500 MB to 1 GB gzipped. GitHub Releases has a 2 GB per-file limit; tools exceeding that are split or hosted via GitHub Packages.
- **Multi-version support:** The prebuilt rootfs is keyed on image digest. When a user installs an uncommon version tag and no prebuilt matches, silo falls back to local unpacking transparently.
- **Build cost:** Weekly rebuilds trigger only when base image digests actually change, keeping CI minute spend proportional to upstream activity.
