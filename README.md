# Silo

**Isolated dev tool runner on Apple Containers.**

> **v0.4.0 — alpha.** macOS 26+ / Apple Silicon only. Expect rough edges.

Silo wraps [Apple Containers](https://github.com/apple/containerization) to run development tools inside ephemeral lightweight VMs. Package managers, interpreters, and compilers work as expected — but each invocation runs isolated with no access to your SSH keys, cloud credentials, or other sensitive data.

## Why

Two classes of tools routinely execute untrusted code with full user permissions:

**Package managers** run arbitrary code during installation (`postinstall` hooks, `setup.py`, build scripts). Recent supply chain attacks demonstrate this is actively exploited:

- **Axios npm compromise (March 2026):** Backdoored versions of a 100M weekly download package deployed a cross-platform RAT via a `postinstall` hook.
- **Shai-Hulud worm (September 2025):** Self-replicating worm compromised 500+ npm packages, harvesting GitHub PATs and cloud credentials.

**AI coding agents** (Claude Code, Cursor, Copilot) read your codebase, edit files, and run shell commands. When something goes wrong — a prompt injection from a malicious file in the repo, a hallucinated destructive command — the agent has access to everything you do: `~/.ssh`, `~/.aws`, API keys, and unrestricted network.

Silo fixes both by running tools in a micro-VM that only sees the project directory and explicitly allowed environment variables.

## What's protected

| Without Silo | With Silo |
|---|---|
| `~/.ssh/`, `~/.aws/`, `~/.gnupg/` — full access | Blocked |
| All environment variables visible | Only explicitly passed vars |
| RAT persists in user space | Dies with ephemeral VM |
| Lateral movement to other projects | Only current project mounted |
| macOS keychain, browser data accessible | Blocked (separate Linux kernel) |

## Requirements

- Apple Silicon Mac (M1+)
- macOS 26 (Tahoe) or later

## Install

```bash
brew install rchekalov/apps/silo

# Add silo shims to your PATH (run once for current shell + append to profile)
eval "$(silo shellenv)"
echo 'eval "$(silo shellenv)"' >> ~/.zshrc
```

The three-part `rchekalov/apps/silo` is required because homebrew-cask already has an unrelated `silo` cask — the fully-qualified form picks our formula.

Homebrew compiles silo from source on your machine (~1–2 min) and ad-hoc codesigns the binary with the virtualization entitlement it needs to boot VMs. No notarization, no Gatekeeper quarantine prompts.

## Install from source

Skip Homebrew and build manually:

```bash
git clone https://github.com/rchekalov/silo.git
cd silo

# Build, sign, and install to /usr/local/bin
make install

# Add silo shims to your PATH
eval "$(silo shellenv)"
echo 'eval "$(silo shellenv)"' >> ~/.zshrc
```

### Build prerequisites

- Xcode Command Line Tools (`xcode-select --install`)
- Go 1.25+ (`brew install go`)
- Swift 6.2+ (usually provided by Xcode CLT)

### Codesigning is required

The binary **must** be codesigned with virtualization entitlements or macOS will kill it (SIGKILL). Always use `make install` (release) or `make sign-debug` (development) — never run raw `go build` output directly.

| Command | Use for |
|---|---|
| `make install` | Production — release build + sign + install to `/usr/local/bin` (prompts for sudo) |
| `make sign-debug` | Development — debug build + sign (faster iteration) |
| `go build` | Compilation only — **unsigned, will crash on any VM operation** |

## Quick start

```bash
# Install a sandboxed tool (first run bootstraps the runtime — takes a few minutes)
silo install python

# Use it normally — shims make it transparent
python --version
python script.py

# Or run explicitly
silo run python -- script.py

# Interactive shell inside the sandbox
silo shell python

# See what's installed
silo list

# See all available tools with versions
silo list --available

# Remove a tool
silo uninstall python
```

Silo also supports shorthand syntax — `silo python script.py` expands to `silo run python -- script.py`, and `silo npm test` resolves the shim and expands to `silo run node --shim npm -- test`.

## Running Claude Code in isolation

Claude Code is an AI agent that reads your codebase, edits files, and runs arbitrary shell commands. When it works correctly, it's exceptional. When something goes wrong — a prompt injection from a malicious file, a hallucinated destructive command — it has the same access as you: `~/.ssh`, `~/.aws`, `~/.gnupg`, API keys, and unrestricted network.

Silo eliminates this by running Claude Code inside a VM where the host filesystem doesn't exist. There's nothing to block because there's nothing to access.

### Setup

```bash
# Install the isolated Claude Code tool
silo install claude-code

# Configure your project
cat > .siloconf << 'EOF'
pass_env:
  - ANTHROPIC_API_KEY
  - GITHUB_TOKEN
EOF

# Run Claude Code — fully isolated
silo claude
```

### What Claude Code sees inside Silo

- `/workspace/` — your project directory (mounted read-write)
- `/root/.claude/` — Claude Code config (persisted between runs)
- Network access to: `api.anthropic.com`, `github.com`, `registry.npmjs.org`, `pypi.org` (and other allowlisted domains)

### What it cannot access

- `~/.ssh/`, `~/.aws/`, `~/.gnupg/` — do not exist
- `~/.config/`, `~/Library/` — do not exist
- Other projects — do not exist
- Arbitrary network destinations — blocked by proxy

### What works the same

- Reading and editing project files
- Running tests (`npm test`, `pytest`, `cargo test`)
- Installing project dependencies
- Git operations (`git add`, `git commit`, `git log`, `git diff`)
- Building the project

### What changes

| Operation | Without Silo | Inside Silo |
|---|---|---|
| Read `~/.ssh/id_rsa` | Accessible | Does not exist |
| Read `~/.aws/credentials` | Accessible | Does not exist |
| Read other projects | Accessible | Does not exist |
| Network: `api.anthropic.com` | Unrestricted | Allowed via proxy |
| Network: arbitrary domain | Unrestricted | Blocked |
| Permission prompts | Frequent | None needed (VM is the boundary) |

### Extending the network allowlist

If Claude Code needs access to your project's API or a custom registry:

```yaml
# .siloconf
overrides:
  claude-code:
    network:
      hostAccess: true
      proxy:
        allow:
          - api.anthropic.com
          - "*.github.com"
          - registry.npmjs.org
          - api.mycompany.com
          - internal-registry.mycompany.com
        deny:
          - "*"
```

## Available tools

| Tool | Image | Shims | Versions |
|---|---|---|---|
| `python` | `python:3.12-slim` | `python`, `python3`, `pip`, `pip3` | 3.12, 3.11, 3.10, 3.9 |
| `node` | `node:22-slim` | `node`, `npm`, `npx` | 23, 22 (LTS), 20, 18 |
| `rust` | `rust:1.83-slim` | `cargo`, `rustc`, `rustup` | 1.83, 1.82, 1.81 |
| `go` | `golang:1.23-alpine` | `go` | 1.23, 1.22, 1.21 |
| `deno` | `denoland/deno:alpine` | `deno` | latest, 2.1.4, 2.0.6 |
| `playwright` | `node:22-slim` | `playwright` | — (requires `node`) |
| `cypress` | `cypress/included:14.0.0` | `cypress` | — (requires `node`) |
| `psql` | `postgres:16-alpine` | `psql`, `pg_dump`, `pg_restore`, `createdb`, `dropdb` | 16, 15, 14 |
| `jupyter` | `jupyter/scipy-notebook:latest` | `jupyter` | — (requires `python`) |
| `aws-cli` | `amazon/aws-cli:latest` | `aws` | — |
| `claude-code` | `silotools/claude-code:latest` | `claude` | — |

Install with a specific version:

```bash
silo install python@3.11-slim
silo install node@20-slim
```

If you don't specify a version, silo shows an interactive picker:

```bash
$ silo install python
Available versions for python:
  1) 3.12-slim [default]
  2) 3.11-slim
  3) 3.10-slim
  4) 3.9-slim
Select version [1]:
```

### Per-project version pinning

Different projects can use different tool versions via `.siloconf` overrides:

```bash
# Project A — uses default Python 3.12
cd ~/projects/web-app
python --version  # Python 3.12.x (from silo)

# Project B — pins Python 3.11 in its .siloconf
cd ~/projects/legacy-api
python --version  # Python 3.11.x (from silo, using project override)
```

The `.siloconf` in `legacy-api/` would contain:

```yaml
overrides:
  python:
    image: docker.io/library/python:3.11-slim
```

This also works for Node.js, Go, Rust, and any other tool — just override the `image` field with the desired version tag.

### Custom tool installation

Install any OCI image as a silo tool:

```bash
# Basic custom image
silo install mydb --image postgres:15 --shim psql,pg_dump --network

# With a setup script and resource tuning
silo install webdriver --image selenium/node:4.0 \
  --setup ./install.sh --cpus 4 --memory 2048

# Custom shim mapping (host command:container command)
silo install mytool --image myimage:latest --shim npm2:npm
```

## Project configuration

### Initialize with `silo init`

The easiest way to set up a project is with `silo init`. It auto-detects tools by scanning for marker files (`package.json`, `requirements.txt`, `Cargo.toml`, `go.mod`, `deno.json`) and walks you through configuration:

```bash
$ cd ~/projects/my-web-app
$ silo init
Detected: node (package.json), python (requirements.txt)

Configure node:
  Network access (host DB, APIs)? [y/N] y
  Forward ports? (comma-separated, e.g., 3000) 3000,5173

Configure python:
  Network access? [y/N] n
  Forward ports?

Pass env vars from host? (comma-separated) GITHUB_TOKEN,DATABASE_URL
Pass host files? (e.g., .npmrc) .npmrc
Exclude from mount? [node_modules,.venv]

Created .siloconf
Added .silo/ to .gitignore

Not installed: python
  Run: silo install python
```

This generates a `.siloconf` and suggests installing any missing tools. After that, just use tools normally — shims handle everything transparently:

```bash
silo install python         # install the missing tool
npm install                  # runs inside sandbox (node already installed)
python manage.py runserver   # runs inside sandbox
```

Non-interactive mode for CI/scripting:

```bash
silo init --tool node --tool python --port 3000 --pass-env GITHUB_TOKEN --no-interactive
```

### Manual `.siloconf`

Create a `.siloconf` in your project root to control what the sandbox can access:

```yaml
# Environment variables to pass into the sandbox
passEnv:
  - GITHUB_TOKEN
  - DATABASE_URL

# Host files to mount read-only
passFiles:
  - .npmrc
  - .pypirc

# Directories to exclude from the project mount
mount:
  exclude:
    - node_modules
    - .venv

# Per-tool overrides for this project
overrides:
  node:
    network:
      hostAccess: true
    ports:
      - host: 3000
        guest: 3000
  python:
    env:
      PYTHONPATH: /workspace/src
```

### Example configurations

**Web app (Node + Python backend):**

```yaml
# .siloconf
passEnv:
  - GITHUB_TOKEN
  - DATABASE_URL

passFiles:
  - .npmrc

mount:
  exclude:
    - node_modules
    - .venv
    - __pycache__

overrides:
  node:
    network:
      hostAccess: true    # access host DB, APIs via host.silo.internal
    ports:
      - host: 3000        # dev server
        guest: 3000
      - host: 5173        # Vite HMR
        guest: 5173
  python:
    env:
      PYTHONPATH: /workspace/src
```

**Data science project:**

```yaml
# .siloconf
overrides:
  python:
    image: docker.io/library/python:3.11-slim  # pin version
    memoryMB: 2048                              # more RAM for pandas/numpy
    rootfsSizeMB: 4096                          # space for large packages
    network:
      hostAccess: true
    ports:
      - host: 8888        # Jupyter notebook
        guest: 8888
```

**Full-stack monorepo:**

```yaml
# .siloconf
passEnv:
  - AWS_PROFILE
  - GITHUB_TOKEN

overrides:
  node:
    image: docker.io/library/node:20-slim  # LTS for production
    network:
      hostAccess: true
    ports:
      - host: 3000
        guest: 3000
  python:
    env:
      PYTHONDONTWRITEBYTECODE: "1"
  go:
    network:
      hostAccess: true
```

## Installing project dependencies with `silo setup`

Each `silo run` creates a fresh ephemeral VM — any packages installed during the run are lost when it exits. To persist installed dependencies, use `silo setup`. It runs a command inside the VM and saves the resulting filesystem, so future runs start with everything already installed.

### Example: Node.js web app

```bash
cd ~/projects/my-web-app

# 1. Create a .siloconf to configure networking and port forwarding
cat > .siloconf << 'EOF'
mount:
  exclude:
    - node_modules

overrides:
  node:
    network:
      hostAccess: true
      proxy:
        allow:
          - registry.npmjs.org
          - "*.github.com"
    ports:
      - host: 5173
        guest: 5173
EOF

# 2. Install dependencies and persist them
silo setup node -- npm install

# 3. Run the dev server — accessible at http://localhost:5173
npm run dev
```

Without the `.siloconf`, the VM has no network access (so `npm install` can't reach the registry) and no port forwarding (so the dev server isn't accessible from the host).

### Example: Python project

```bash
cd ~/projects/my-api

# 1. Configure network access for pip and port forwarding for the server
cat > .siloconf << 'EOF'
overrides:
  python:
    network:
      hostAccess: true
      proxy:
        allow:
          - pypi.org
          - "*.pythonhosted.org"
    ports:
      - host: 8000
        guest: 8000
EOF

# 2. Install from requirements.txt and persist
silo setup python -- pip install -r requirements.txt

# 3. Future runs have all packages available
python manage.py runserver
```

Or use `silo init` to auto-detect your project and generate a `.siloconf` interactively.

### Project-local vs global

By default, `silo setup` saves the image to `.silo/<tool>/rootfs.ext4` in the project directory (requires a `.siloconf` in the project root). Use `--global` for packages you want everywhere:

```bash
# Project-local (default) — only this project gets these deps
silo setup node -- npm install

# Global — available in all projects
silo setup python --global -- pip install poetry

# Project-local builds on top of global
# (global has poetry, project adds project-specific deps)
silo setup python -- poetry install
```

### Other uses

```bash
# Install system packages or browser binaries
silo setup node -- npx playwright install --with-deps

# Reset a customized environment
silo setup node --reset
silo setup python --reset --global
```

Rebuild later from stored scripts:

```bash
silo rebuild node                  # Re-run stored setup
silo rebuild --all                 # Rebuild all customized tools
silo rebuild python --setup "pip install -U pip"  # Override the script
```

## Shim management

Add or remove shims for installed tools at runtime, without reinstalling:

```bash
# Add a shim (creates ~/.silo/bin/ipython pointing to the python tool)
silo shim python add ipython

# Custom mapping — host command runs a different container command
silo shim python add ipython:python -m IPython

# Add multiple shims at once
silo shim python add ipython black mypy

# Remove a shim
silo shim python remove ipython

# List all shims for a tool
silo shim python list
```

Silo detects conflicts if a shim name is already claimed by another tool.

## IDE integration (experimental)

Silo can run language servers (LSP) inside the sandbox so your editor gets autocomplete, go-to-definition, and error checking — all while code analysis stays isolated. The LSP server runs in the same sandboxed environment as your tools, so it sees the exact same packages and dependencies.

> **Experimental:** LSP proxying works but has not been extensively tested with real editor workflows. Manual configuration may be required.

Supported language servers:

| Tool | Language Server | Install |
|---|---|---|
| `python` | Pyright | `pip install pyright` (auto) |
| `node` | TypeScript Language Server | `npm install -g typescript-language-server` (auto) |
| `rust` | rust-analyzer | `rustup component add rust-analyzer` (auto) |
| `go` | gopls | `go install golang.org/x/tools/gopls@latest` (auto) |

Start a language server (the LSP server is installed automatically on first use):

```bash
silo lsp python     # starts pyright, communicates via stdio
silo lsp node       # starts typescript-language-server
silo lsp rust       # starts rust-analyzer
silo lsp go         # starts gopls
```

Generate IDE configuration to point your editor at silo's LSP:

```bash
silo ide vscode     # creates .vscode/settings.json
silo ide zed        # creates .zed/settings.json
silo ide neovim     # generates LSP client config
```

Silo transparently rewrites file paths between host and container, so your editor works with real file paths while the language server operates inside the sandbox.

## Cache management and disk usage

Silo caches unpacked rootfs ext4 files so subsequent `silo run` invocations start in ~600ms instead of ~25s. On APFS the cache is sparse (a "2 GB" file uses ~200 MB on disk), but it still accumulates over time. The disk commands are:

### See what's using disk

```bash
silo cache report                 # total ~/.silo footprint by bucket
silo cache list                   # per-entry view (raw, zstd, or both) with last-used times
```

Example:

```
~/.silo disk usage (on-disk / apparent):
  rootfs cache          37.0 MiB /     36.2 MiB  (0 raw, 1 zstd, 0 both)
  per-tool caches       78.5 MiB
  containers (live)      0.0 MiB
  OCI image store      280.0 MiB
      orphan blobs: 45.0 MiB  (run `silo cache gc --images` to free)
  builds               202.4 MiB
  --
  total                597.9 MiB
```

### Garbage collect (LRU + age-based)

```bash
silo cache gc                     # evict oldest rootfs entries until under the size cap
silo cache gc --dry-run           # preview
silo cache gc --images            # also GC unreferenced OCI layer blobs
silo cache gc --tool-caches       # also evict old files from pip/npm/cargo caches
silo cache gc --max-size 2048     # override the policy for one run
silo cache gc --max-age 30        # only evict entries untouched for >30 days
```

GC runs automatically once per process at the top of `silo run` — you passively reclaim disk just by using silo. Cold entries stick around until the cache exceeds the policy cap (default 8 GiB) or the age cutoff (default 60 days).

### Compress cold entries (zstd)

```bash
silo cache compress               # compress entries last used >14 days ago (~4× smaller)
silo cache compress --all         # compress every entry regardless of age
silo cache compress --dry-run     # preview
```

A compressed entry is decompressed back to raw on the next cache hit (≈1–3s one-off cost for a 500 MB image), then cloned normally. My debian-slim image went from 155 MB → 37 MB with default zstd.

### Configure the policy

Put a `cache:` block in `.siloconf` or `~/.silo/siloconf`:

```yaml
cache:
  rootfs:
    maxSizeMB: 8192        # LRU eviction above this cap
    maxAgeDays: 60         # entries untouched beyond this are evicted
  tools:
    maxSizeMB: 4096        # per-mount cap for pip/npm/cargo
    maxAgeDays: 30         # file-level eviction by atime inside each mount
    perMount:
      rust/cargo: 8192     # override for specific mounts
```

### Full cleanup

```bash
# Remove specific buckets
silo cache clean --safe           # only orphans (not referenced by installed tools)
silo cache clean --rootfs         # nuke rootfs cache
silo cache clean --containers     # nuke stale container state
silo cache clean                  # wipe rootfs cache + container state

# Project-scoped reclamation (walks up for .siloconf)
silo clean                        # rootfs cache + per-tool caches + stale VMs for this project
silo clean --rootfs-only
silo clean --caches-only
silo clean --force                # also remove artifacts shared with other tools

# Uninstalling a tool also reclaims its rootfs cache + OCI image (if not shared)
silo uninstall python             # use --keep-image to opt out

# Full reset (next install re-bootstraps)
silo reset
```

## How it works

```
User runs: python script.py
         │
         ▼
Shim (~/.silo/bin/python)
         │  calls: silo run python --shim python -- script.py
         ▼
silo run
         │  1. Loads ~/.silo/config.yaml (tool definition)
         │  2. Finds .siloconf (walks up from cwd)
         │  3. Merges config (project overrides global)
         ▼
Apple Container (ephemeral micro-VM)
  ┌─────────────────────────────────────┐
  │  /workspace  ← project dir (rw)    │
  │  /root/.cache/pip  ← cache (rw)    │
  │                                     │
  │  $GITHUB_TOKEN ← if .siloconf      │
  │                   allows it         │
  │                                     │
  │  No access to: ~/.ssh, ~/.aws,      │
  │  ~/Library, keychain, other projects│
  └─────────────────────────────────────┘
         │
         ▼
VM destroyed after exit (ephemeral mode)
```

## Command reference

| Command | Description |
|---|---|
| `silo install <tool>` | Install a tool from the registry or custom image |
| `silo uninstall <tool>` | Remove an installed tool and its shims |
| `silo run <tool> -- <args>` | Run a command in an ephemeral sandbox |
| `silo shell <tool>` | Interactive shell inside a sandbox |
| `silo list [--available]` | Show installed or all available tools |
| `silo init` | Auto-detect project and generate `.siloconf` (experimental) |
| `silo setup <tool> -- <cmd>` | Customize a tool's VM and persist changes |
| `silo rebuild [<tool>]` | Re-run stored setup scripts |
| `silo shim <tool> <add\|remove\|list>` | Add, remove, or list shims for a tool |
| `silo lsp <tool>` | Start a sandboxed language server |
| `silo ide <ide>` | Generate IDE configuration for LSP |
| `silo status` | Show status of running VMs |
| `silo cache report` | Summarise disk usage by bucket |
| `silo cache list` | Per-entry view of the rootfs cache (raw / zstd / both) |
| `silo cache gc` | LRU + age-based eviction (also `--images`, `--tool-caches`) |
| `silo cache compress` | Compress cold rootfs entries with zstd (~4× smaller) |
| `silo cache clean` | Hard cleanup of rootfs cache / containers / orphans |
| `silo clean` | Project-scoped reclamation (respects `.siloconf`) |
| `silo pull` | Reconcile the environment to `.siloconf` |
| `silo reset` | Remove all silo data |

## First-run bootstrap

The first `silo install` bootstraps the container runtime (one-time, ~5 minutes):

1. **Linux kernel** — downloaded from [Kata Containers](https://github.com/kata-containers/kata-containers) release (~280MB)
2. **Swift toolchain** — installs [swiftly](https://www.swift.org/swiftly/) and Swift 6.3 snapshot for cross-compilation
3. **Static Linux SDK** — required to cross-compile vminitd for Linux (~290MB)
4. **vminitd** — cross-compiled from the [containerization](https://github.com/apple/containerization) source
5. **initfs** — ext4 filesystem image containing vminitd

All artifacts are cached at `~/.silo/`. Subsequent tool installs only pull the OCI image (seconds, not minutes).

Use `--timing` to see where time is spent:

```bash
$ silo run python --timing -- --version
[silo] config loaded: 5ms
[silo] runtime ready: 6ms
[silo] ephemeral completed: 1200ms
[silo] total: 1211ms
```

## Known limitations

- **macOS 26+ and Apple Silicon only** — requires Apple Containerization framework
- **First install takes ~5 minutes** — downloads kernel, Swift toolchain, cross-compiles vminitd (one-time)
- **No warm VM persistence** — each `silo run` is a fresh process; a background daemon is planned
- **LSP/IDE integration is experimental** — may need manual editor configuration

## Architecture

Silo is built on the [Apple Containerization](https://github.com/apple/containerization) Swift framework. Each container is a lightweight VM with its own Linux kernel — not a process namespace like Docker. This provides stronger isolation:

- Separate kernel (Linux guest on macOS host)
- No shared filesystem beyond explicit mounts
- No shared process namespace
- Sub-second startup time
- Near-zero idle overhead

## License

Apache License 2.0 — see [LICENSE](LICENSE).
