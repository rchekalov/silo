---
name: silo
description: Run dev tools (python, node, rust, go, deno, psql, claude-code, ...) inside Apple Container micro-VMs so package managers and AI agents can't read ~/.ssh, ~/.aws, keychain, or other projects. Manages isolated tool installs, per-project network/port config, disk-efficient rootfs caching with LRU + zstd compression, and project-scoped cleanup.
version: 0.4.0
---

# Silo

Silo is a macOS CLI that runs dev tools inside ephemeral Apple Container micro-VMs. Each invocation sees only the project directory and explicitly-passed env/files — package managers, interpreters, compilers, and AI coding agents can't touch `~/.ssh`, `~/.aws`, `~/.gnupg`, keychain, or other projects.

Use this skill when the user wants to:

- Install or run a dev tool in isolation (python, node, rust, go, deno, psql, jupyter, playwright, cypress, aws-cli, claude-code).
- Sandbox an AI agent (Claude Code, Cursor-style workflows) so it can't read host secrets.
- Configure a project's tool network access, port forwarding, or env-var passthrough.
- Diagnose or reclaim disk used by silo's caches.
- Pin a different tool version for a specific project.

**Don't** use this skill for: Linux containers on non-Apple-Silicon machines, Docker/Podman workflows, or any scenario where the host tools are already trusted.

## Mental model

- `silo install <tool>` puts a shim in `~/.silo/bin/`; afterwards `python script.py` transparently runs `silo run python script.py` inside a VM.
- `.siloconf` (YAML, walks up from cwd; global fallback at `~/.silo/siloconf`) controls what the sandbox can see: `passEnv`, `passFiles`, `overrides` (per-tool `network`, `ports`, `env`, `image`).
- Each run is a fresh VM. Packages installed with `pip install foo` inside a run vanish when it exits — use `silo setup` or per-tool cache mounts to persist state.
- Rootfs is cached per OCI digest at `~/.silo/rootfs-cache/` (APFS clonefile for sub-second starts). Cold entries can be zstd-compressed to save ~4× disk.

## Command map

### Install and run

| Intent | Command |
|---|---|
| Install a tool (default version) | `silo install <tool>` |
| Install pinned version | `silo install python@3.11-slim` |
| Install from custom image | `silo install mydb --image postgres:15 --shim psql,pg_dump --network` |
| List registry / installed | `silo list --available` / `silo list` |
| Uninstall (also frees rootfs + image) | `silo uninstall <tool>` *(add `--keep-image` to retain OCI blobs)* |
| Run a command | `silo run python script.py` or just `python script.py` (via shim) |
| Interactive shell | `silo shell python` |
| Run with timing breakdown | `silo run --timing python --version` |

### Project configuration

| Intent | Command |
|---|---|
| Auto-detect tools + generate `.siloconf` | `silo init` |
| Non-interactive init | `silo init --tool node --tool python --port 3000 --pass-env GITHUB_TOKEN --no-interactive` |
| Show merged config | `silo config show` |
| Add port forwarding | `silo config add-port node 3000:3000` |
| Allow a domain through the proxy | `silo config network allow node '*.npmjs.org'` |
| Reconcile environment to `.siloconf` | `silo pull` *(installs missing tools, warms cache; safe to re-run)* |

### Persisting installs into the rootfs

Each `silo run` is a fresh VM — `pip install`/`npm install` inside a run disappear when it exits. To bake deps into the image:

| Intent | Command |
|---|---|
| Install project deps once | `silo build node npm install` |
| Global (all projects) | `silo build --global python pip install poetry` |
| Re-run stored setup | `silo build <tool> --rerun` |
| Reset a customised env | `silo build node --remove` |

### Disk usage and cache management

| Intent | Command |
|---|---|
| Total ~/.silo footprint by bucket | `silo cache report` |
| Per-entry view (raw, zstd, both) | `silo cache list` |
| GC rootfs cache (LRU + age) | `silo cache gc` |
| GC + preview | `silo cache gc --dry-run` |
| Also GC orphan OCI blobs | `silo cache gc --images` |
| Also trim pip/npm/cargo caches | `silo cache gc --tool-caches` |
| Override size cap for one run | `silo cache gc --max-size 2048` |
| Compress cold entries (4× smaller) | `silo cache compress` *(decompresses on next hit; ~1–3s one-off)* |
| Compress everything | `silo cache compress --all` |
| Project-scoped reclamation | `silo clean` |
| Hard reset a bucket | `silo cache clean --rootfs` / `--containers` / `--safe` |
| Full reset | `silo reset` |

GC also runs automatically once per `silo run` — users rarely need to invoke it manually unless disk is already tight.

### Shims and LSP

| Intent | Command |
|---|---|
| Add a shim to an installed tool | `silo shim python add ipython` |
| Custom host→container mapping | `silo shim python add ipython:python -m IPython` |
| Remove a shim | `silo shim python remove ipython` |
| Run a language server in the sandbox | `silo lsp python` (pyright) / `lsp node` (tsserver) / `lsp rust` / `lsp go` |
| Generate IDE config | `silo ide vscode` / `silo ide zed` / `silo ide neovim` |

## Authoring `.siloconf`

Writing a `.siloconf` is the most common non-trivial task. Default template:

```yaml
# Environment variables forwarded from the host (only these are visible inside the VM)
passEnv:
  - GITHUB_TOKEN
  - DATABASE_URL

# Host files mounted read-only into /workspace/
passFiles:
  - .npmrc
  - .pypirc

# Directories excluded from the project mount (saves mount time + memory)
mount:
  exclude:
    - node_modules
    - .venv
    - __pycache__

# Tools required by this project — `silo pull` installs any that are missing
tools: [node, python]

overrides:
  node:
    network:
      hostAccess: true            # access host DB/APIs via host.silo.internal
      proxy:
        allow:                    # HTTPS/HTTPS allowlist (deny everything else)
          - registry.npmjs.org
          - "*.github.com"
    ports:
      - host: 3000
        guest: 3000
  python:
    image: docker.io/library/python:3.11-slim   # pin a version for this project
    env:
      PYTHONPATH: /workspace/src

# Optional: cache size/age caps. Omit for defaults (8 GiB rootfs / 4 GiB per tool / 60d age).
cache:
  rootfs:
    maxSizeMB: 8192
    maxAgeDays: 60
  tools:
    maxSizeMB: 4096
    perMount:
      rust/cargo: 8192
```

Decision rules when authoring:

- **`passEnv`**: forward the minimum set needed. Never blanket-include `HOME`, `PATH`, `USER`.
- **`network.hostAccess`**: only enable if the tool needs to reach the host (DB on localhost, local API). Combine with `proxy.allow` to restrict outbound destinations.
- **`proxy.allow`**: leading wildcards work (`*.npmjs.org`). The proxy is an HTTP/HTTPS forward proxy; it blocks everything not on the allowlist.
- **`ports`**: only forward ports you need. `host: 3000 guest: 3000` exposes the VM's `:3000` to the host.
- **`pass_files`**: for credentials files (`.npmrc`, `.pypirc`). Mounted read-only.
- **`image` override**: pin a specific Docker tag for this project. Other projects get their defaults.
- **`tools:` list**: everything the project depends on; `silo pull` uses this set to install missing tools and warm the cache.

## Common workflows

### Running Claude Code in isolation

```bash
silo install claude-code
cat > .siloconf << 'EOF'
passEnv: [ANTHROPIC_API_KEY, GITHUB_TOKEN]
EOF
silo claude                         # fully isolated from ~/.ssh, ~/.aws, keychain
```

Inside the sandbox Claude Code sees `/workspace/` (the project) and `/root/.claude/` (its own config, persisted across runs) — nothing else.

### Python project with pip + dev server

```bash
silo install python
cat > .siloconf << 'EOF'
overrides:
  python:
    network:
      hostAccess: true
      proxy:
        allow: [pypi.org, "*.pythonhosted.org"]
    ports: [{host: 8000, guest: 8000}]
EOF
silo build python pip install -r requirements.txt        # bakes deps into rootfs
python manage.py runserver                                # run normally via shim
```

### Pinning a tool version

```yaml
# .siloconf — e.g., keep this project on python 3.11 while global default is 3.12
overrides:
  python:
    image: docker.io/library/python:3.11-slim
```

### Reclaiming disk

```bash
silo cache report                   # see where disk is going
silo cache compress                 # 4× reduction on cold entries
silo cache gc --images              # drop orphan OCI blobs
silo clean                          # project-scoped: nuke rootfs + tool caches for current project
```

### Fixing a slow start

First `silo install` downloads a kernel + Swift toolchain + cross-compiles vminitd (~5 min, one-time). After that, runs with a warm cache take ~600ms. Run `silo run --timing python --version` to see the breakdown.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Binary exits with code 137 (SIGKILL) | Not codesigned. Use `make install` or `make sign-debug` — never run `go build` output directly. |
| "couldn't be saved … file with same name already exists" | Stale container dir from crashed run. `silo cache clean --containers` or let the auto-reaper handle it (runs at next `silo run`). |
| `pip install` / `npm install` fails inside sandbox | Default config blocks network. Add `network.hostAccess: true` + `proxy.allow` to `.siloconf` for that tool, or use `silo setup` which has networking on. |
| "No such file or directory: /workspace/…" | That path is outside the project root. Only the directory containing `.siloconf` (or cwd if absent) is mounted. |
| Rootfs cache growing unbounded | Shouldn't happen (auto-GC runs). If it did, `silo cache gc --dry-run` + `silo cache compress`. |
| Can't reach host `localhost:5432` from sandbox | Use `host.silo.internal:5432`. `localhost` inside the VM is the guest, not the host. |
| Shim name conflicts with another tool | `silo install` warns; rename via `silo shim <tool> add altname:origname` or change host command. |

## Environment layout (for read-only reference)

```
~/.silo/
  config.yaml          # installed tools (GlobalConfig)
  siloconf             # global fallback .siloconf
  bin/                 # shims (must be on PATH)
  vmlinux, initfs.ext4 # bootstrap artifacts (~5 min first install)
  images/              # OCI content store (deduped by layer digest)
  rootfs-cache/        # unpacked ext4 per image digest — {digest}.ext4 (hot) or {digest}.ext4.zst (cold)
  cache/{tool}/        # per-tool package caches (pip, npm, cargo, ...)
  containers/          # transient — auto-reaped if older than 30 min
  builds/              # global setup rootfs (from `silo setup --global`)
```

## Invariants

- Shims are in `~/.silo/bin/`. The user's shell must have this on PATH for transparent use.
- `silo init` writes `.silo/` to `.gitignore`. Never commit anything under `.silo/` — it's transient cache.
- `.siloconf` **should** be committed — it's the project's authoritative sandbox declaration.
- macOS 26+ / Apple Silicon only. Don't suggest Silo for Linux or Intel Macs.
- Every silo command is safe to run from any directory. Commands that need project context walk up for `.siloconf`; if missing, they either act globally or bail with a clear message.
