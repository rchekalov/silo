# Silo Features

Complete list of what silo can do today (v0.4.0).

## Core: Sandboxed Tool Execution

Every tool runs in its own Apple Container micro-VM. No access to SSH keys, cloud credentials, or other host data.

- `silo run python script.py` — run a command in an ephemeral VM (silo flags before the tool, command after; legacy `silo run python -- script.py` still works)
- `silo run node npm run dev` — when the first arg is a sibling shim of the tool (e.g. `npm`/`npx` for `node`, `pip` for `python`), it's auto-promoted to the entrypoint, so this execs `npm run dev`, not `node npm run dev`. Cross-tool shims emit a stderr hint instead of rewriting.
- `silo shell python` — interactive shell inside the container
- `silo python script.py` — shorthand (auto-expands to `silo run python script.py`)
- `--timing` flag on run — shows config/runtime/VM timing breakdown

## Tool Management

### Install / Uninstall

- `silo install python` — install from built-in registry
- `silo install python@3.12` — install a specific version (tool@version syntax; matches `sdk`, `npm`, `volta`, `uv tool install`)
- `silo install my-tool --image docker.io/foo/bar:latest --shim foo,bar` — custom image
- `silo install node --force` — force reinstall globally (new version, re-pull, refresh cache)
- `silo uninstall python` — remove tool and its shims

If the tool is already installed, `silo install` refuses (unlike prior behaviour). Use `--force` for a true reinstall, or `silo use` to pin a different version for the current project.

Registry entries may declare `postInstall:` — a list of shell commands run right after the image is pulled, baked into a persistent rootfs at `~/.silo/builds/<tool>/rootfs.ext4`. Subsequent `silo run` invocations boot from the baked rootfs, so apt packages, globally-installed npm/pip packages, etc. are always available without re-fetching. Used by `claude-code` (node:22-slim + `npm i -g @anthropic-ai/claude-code` + apt git/python3/etc.). The build step drops the tool's proxy allowlist so upstream package managers work regardless of what the tool is permitted to reach at runtime.

### Project Version Pinning (pyenv/asdf-style)

- `silo use python@3.12` — record a project pin in `silo.toml` (writes `tools` + `[overrides]` as needed). Does **not** install — run `silo sync` after.
- `silo use node` — pin the default version of a tool
- `silo use --global python@3.12` — same, but write to `~/.silo/silo.toml`
- `silo unuse python` — remove the pin from `silo.toml`
- Inside a project with `silo.toml`, all shims (`node`, `npm`, etc.) automatically use the project-pinned version
- Outside the project — global version is used

### Listing

- `silo list` — show installed tools
- `silo list --available` — show all registry tools

## Built-in Tool Registry

11 tools with version selection:

| Tool | Default | Versions | Shims |
|------|---------|----------|-------|
| python | 3.12-slim | 3.12, 3.11, 3.10, 3.9 | python, python3, pip, pip3 |
| node | 22-slim | 22, 20, 18, 16 | node, npm, npx |
| rust | 1.87-slim | 1.87, 1.86, 1.85 | rustc, cargo, rustup |
| go | 1.24 | 1.24, 1.23, 1.22 | go |
| deno | 2.4.0 | 2.4.0, 1.46.0 | deno |
| playwright | latest | - | npx |
| cypress | included | - | npx |
| psql | 16-alpine | 16, 15, 14 | psql |
| jupyter | (python) | - | jupyter |
| aws-cli | latest | - | aws |
| claude-code | (node) | - | claude |

User registry override: `~/.silo/registry.yaml` extends/replaces built-in entries.

## Shim System

- Shim scripts in `~/.silo/bin/` — add to PATH, then `python`, `npm`, etc. invoke silo transparently
- `silo shim add python my-script` — add custom shim
- `silo shim remove python my-script` — remove custom shim
- `silo shim list` — list all shims
- Conflict detection — warns when installing overlapping shims from different tools
- Custom aliases — `npm2:npm` maps host command `npm2` to container command `npm`

## Configuration

### Global Config (`~/.silo/config.toml`)

Stores installed tools with image, shims, cache mounts, resource limits, env, network, ports, LSP config. Stored as TOML.

### Project Config (`silo.toml`)

Found by walking up from current directory. Merged with global silo.toml (`~/.silo/silo.toml`).

- `pass_env` — forward host env vars into sandbox (e.g., `GITHUB_TOKEN`)
- `pass_files` — mount host files read-only (e.g., `.npmrc`)
- `passSshAgent` — forward `$SSH_AUTH_SOCK` into the guest at `/run/silo/ssh-agent.sock` so `git clone git@...` and other SSH-based tools work inside the sandbox. Apple Containerization runs a vsock relay; **host private keys never enter the guest** — only the agent protocol is relayed. Prefer this over `passFiles: [.ssh/id_ed25519]`, which copies the actual key material into the guest filesystem and breaks isolation. Toggle ad-hoc with `silo run --ssh-agent <tool> ...`.
- `mount.mode` / `mount.exclude` — control workspace mount behavior
- `overrides.<tool>.image` — per-project image/version override
- `overrides.<tool>.env` — per-project environment variables
- `overrides.<tool>.network` — per-project network config
- `overrides.<tool>.ports` — per-project port mappings
- `overrides.<tool>.postInstall` — extra bake steps appended to the registry's
- `overrides.<tool>.cache` — extra cache mounts (deduped by guest path)
- `overrides.<tool>.cpus` / `memoryMB` / `rootfsSizeMB` — per-project resource bumps (e.g. a Vue/Vite build needs ≥ 6 GB RAM)
- `overrides.<tool>.workdir` — guest working directory (e.g. `/app` instead of `/workspace`)
- `overrides.<tool>.passEnv` — host env vars forwarded only for this tool (e.g. `ANTHROPIC_API_KEY` only for `claude-code`)
- `overrides.<tool>.passSshAgent` — enable SSH agent forwarding only for this tool (logical OR with the project-level / registry-level value)
- `overrides.<tool>.lsp` — pin a language-server install command, add LSP-only cache mounts, tweak LSP env

Not overridable (registry/engine concerns): `shims`, `requires`, `buildRootfs`, `buildScript`, `buildScope`, `buildProjectRoot`.

Merge order: tool defaults -> global silo.toml -> project silo.toml

### Init

- `silo init` — generate a `silo.toml` in current directory

### Config CLI

Modify `silo.toml` from the command line (creates the file if it doesn't exist):

- `silo config ports add <tool> <host:guest>` — add port forwarding
- `silo config ports remove <tool> <host:guest>` — remove port forwarding
- `silo config network allow <tool> <domain>` — allow a domain for network proxy
- `silo config network deny <tool> <domain>` — deny a domain
- `silo config network remove <tool> <domain>` — remove a domain from allow/deny
- `silo config show` — display merged project config

The old flat `config add-port` / `config remove-port` forms remain as hidden aliases and will be removed in 0.6.0.

## Performance

### Progress Feedback

- Bootstrap downloads (kernel, Swift toolchain, SDK) show curl progress bars with size hints
- OCI image pulls show an animated spinner

### Rootfs Caching

First run unpacks OCI layers (~25s). Subsequent runs clone cached rootfs via APFS copy-on-write (~1ms).

- Cache key: image digest + rootfs size
- Cache location: `~/.silo/rootfs-cache/`
- `silo cache clean` — clear all cached rootfs

### Persistent Caches

Tool caches (pip, npm, cargo, go mod, deno) are mounted from host, persisted across runs at `~/.silo/cache/`.

For Python specifically, `~/.silo/cache/python/{pip,uv,poetry}` are all mounted by default. Users who bake `poetry` or `uv` into the python rootfs (`silo build python pip install poetry` or `… uv`) get the same warm-cache behavior pip already enjoys, without further configuration.

### Alternative Python Package Managers

`poetry` and `uv` are not first-class registry tools (no dedicated shims). The recommended pattern is to bake them into the `python` rootfs and invoke via `python -m`:

```bash
silo build python pip install poetry
silo run python -m poetry config virtualenvs.in-project true --local
silo run python -m poetry install        # auto-activated via ./.venv

silo build python pip install uv
silo run python -m uv venv
silo run python -m uv pip install <pkg>  # auto-activated via ./.venv
```

`virtualenvs.in-project = true` puts poetry's venv at `./.venv`, which silo's auto-activation logic already understands. uv's `uv venv` creates `./.venv` by default. Set `UV_PYTHON_DOWNLOADS=never` (via `[overrides.python.env]`) so uv reuses the rootfs interpreter instead of trying to download a managed CPython.

## Network

- Default: no network access (full isolation)
- Per-tool: `network.hostAccess: true` + optional proxy allowlist
- DNS: configurable nameservers (defaults to 1.1.1.1, 8.8.8.8)
- Proxy allowlists with wildcard domains (e.g., `*.github.com`)

## Port Forwarding

- `ports` in `silo.toml` overrides or tool config — maps host ports to guest ports
- Ports automatically enable networking (no separate `hostAccess` needed)
- Application-layer TCP relay, started after container boot
- Example `silo.toml`:
  ```toml
  [[overrides.python.ports]]
  host = 8080
  guest = 8080
  ```

## Project-Scoped Builds

- `silo build <tool> <cmd>` — run a setup command to build a customized rootfs (legacy `silo build <tool> -- <cmd>` still works)
  - Default: project-local (`.silo/<tool>/rootfs.ext4`)
  - `--global` flag: system-wide (`~/.silo/builds/<tool>/rootfs.ext4`)
- `silo build <tool> --rerun` — re-run the stored build script
  - `--script <cmd>` — override the stored script
  - `--all` — rerun for every tool with a stored script
  - `--global` — target the global build rootfs
- `silo build <tool> --remove` — delete the stored rootfs

The old `silo setup` / `silo rebuild` commands remain as deprecated aliases of `silo build` and will be removed in 0.6.0.

The user's command is wrapped as `sh -c '<cmd> && sync'` before the build VM runs it. Without the trailing `sync`, the rootfs would be snapshotted while the guest's page cache still held tail-end writes — visible as `pip install`-installed package directories appearing as 0-byte file inodes in the saved rootfs.

Lookup order: project rootfs -> global build rootfs -> rootfs cache -> OCI unpack

## Project Reconciliation

- `silo sync` — install any tools declared in `silo.toml` that aren't installed yet, and pull/warm their rootfs cache. Safe to re-run.
  - `--dry-run` — print the plan without acting
  - `--force` — re-pull even if the rootfs cache is warm
- `silo pull` / `silo apply` — deprecated aliases of `silo sync` (removed in 0.6.0).

## Status & Diagnostics

Previously bundled into `silo status`; now split into three focused commands:

- `silo doctor` — runtime readiness (kernel, initfs, bootstrap state). Also flags shims that are shadowed on the live `$PATH` — i.e. `~/.silo/bin/pip` exists but `which pip` resolves elsewhere because homebrew/conda/asdf shellenv was sourced after silo's. Fix is to move `eval "$(silo shellenv)"` last in your shell rc.
- `silo current` — installed tools plus any active project overrides
- `silo current <tool>` — effective tool definition after `silo.toml` overrides
- `silo cache report` — disk usage by bucket (rootfs cache, per-tool caches, images, builds)

`silo status` remains as a deprecated alias that prints `doctor` + `current` output.

## Python venv auto-activation

`silo run python …` injects `VIRTUAL_ENV` and prepends `bin/` to `PATH` when the project root contains `./venv/bin/python` or `./.venv/bin/python` — equivalent to `source venv/bin/activate`. Without this, `silo run python venv/bin/pip install …` would invoke the rootfs python (since `python venv/bin/pip` is just "run this script with the system interpreter"), and pip would install into the ephemeral `/usr/local/lib/python*/site-packages` instead of the host venv. Scoped to the python tool only — a stray `venv/` in a node project doesn't touch other tools' env.

## LSP Support

Run language servers inside sandboxed containers, with transparent stdio proxying and path rewriting between host and guest.

### LSP Server

- `silo lsp python` — start pyright in a container, proxy JSON-RPC over stdio
- `silo lsp node` — start typescript-language-server
- `silo lsp rust` — start rust-analyzer
- `silo lsp go` — start gopls
- Automatic path rewriting: host paths (`/Users/me/project/...`) ↔ guest paths (`/workspace/...`) in all LSP messages, including `file://` URIs
- LSP server is installed automatically inside the container on first use (via `lsp.install` config)
- LSP-specific cache mounts and environment variables from registry config
- Container stays alive until IDE disconnects (stdin EOF) or the LSP server exits
- stderr passes through directly for diagnostics

### IDE Config Generation

- `silo ide vscode` — generate `.vscode/settings.json` with language server config
- `silo ide zed` — generate `.zed/settings.json` with LSP binary entries
- `silo ide neovim` — generate `.nvim-silo.lua` with lspconfig setup
- `silo ide <ide> --tool <name>` — generate config for a specific tool only
- Merges with existing IDE config files (won't overwrite your settings)

### Supported LSP Servers (from registry)

| Tool | LSP Server | Install Command |
|------|-----------|----------------|
| python | pyright-langserver --stdio | pip install pyright |
| node | typescript-language-server --stdio | npm install -g typescript-language-server typescript |
| rust | rust-analyzer | rustup component add rust-analyzer |
| go | gopls serve | go install golang.org/x/tools/gopls@latest |

## Shell Integration

- `silo shellenv [bash|zsh|fish]` — print the PATH export for `~/.silo/bin`. Intended use: `eval "$(silo shellenv)"` in a shell profile so silo's shims (python, node, npm, ...) are found automatically after install. Without an argument, the shell is detected from `$SHELL`. Fish gets `set -gx PATH ...`; everything else gets POSIX `export PATH=...`.

## Other

- `silo reset` — remove `~/.silo/` (full reset)
- Reserved name protection — can't install tools named `run`, `setup`, etc.
- Passthrough args — `--help`, `--version` after `--` passed to the tool
- argv[0] shim detection — when invoked as `python` via symlink, auto-routes to the right tool
