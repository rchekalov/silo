# Silo — Homebrew Distribution

## How it works for the user

```bash
brew install rchekalov/silo/silo
```

The fully-qualified name is required because homebrew-cask already has a `silo` cask (an unrelated macOS app). Using just `brew install silo` resolves to that cask. The three-part form `rchekalov/silo/silo` (user/tap/formula) forces Homebrew to pick our formula.

Homebrew pulls the source tarball for the tagged release, compiles silo on the user's machine (`make release-bundle`), and ad-hoc codesigns the resulting binary with the virtualization entitlement. No notarization, no Developer ID, no Gatekeeper quarantine prompts.

Compile time on a recent M-series: ~60 seconds (Go build + Swift bridge).

## What's in play

### Two repositories

- **`rchekalov/silo`** — main source repo. Tag pushes trigger `release.yml`.
- **`rchekalov/homebrew-silo`** — the tap repo. Just a `Formula/silo.rb` file.

The tap repo's `homebrew-` prefix is required by Homebrew convention — it lets users run `brew tap rchekalov/silo` instead of `brew tap rchekalov/homebrew-silo`.

### Source-build formula

The canonical formula lives at `scripts/homebrew/silo.rb` in this repo (seed copy). On every tag the release workflow rewrites `version`, `url`, and `sha256` in the tap repo's `Formula/silo.rb`.

Key formula properties:

- `url` points at the auto-generated GitHub source tarball (`.../archive/refs/tags/v<version>.tar.gz`). No asset upload required.
- `depends_on "go" => :build` and `depends_on "swift" => :build` — Homebrew resolves the toolchains.
- `depends_on :macos`, `depends_on arch: :arm64`, `depends_on macos: :tahoe` — macOS 26 on Apple Silicon only.
- `install` is one line: `make release-bundle PREFIX=#{prefix} VERSION=#{version}`. That target is defined in the main repo's `Makefile` and is the contract between silo and Homebrew.
- `caveats` tells the user about `PATH`, `brew install container`, and the one-time runtime bootstrap.
- The Apple Container CLI is intentionally **not** a formula dependency — silo surfaces a clear error at runtime if it's missing.

### The `release-bundle` Makefile target

```
make release-bundle PREFIX=<dir> [VERSION=<tag>]
```

- Builds release with rpath pointing at `$PREFIX/lib/silo` (so the installed binary resolves the dylib from within the Homebrew prefix).
- Bakes `VERSION` into the binary via `-ldflags "-X .../version.Version=..."` so `silo --version` matches the tag.
- Ad-hoc codesigns both the binary and the dylib with `silo.entitlements`.
- Lays out `$PREFIX/bin/silo` and `$PREFIX/lib/silo/libSiloBridge.dylib`.

## Release workflow

`.github/workflows/release.yml` fires on tag push `v*`:

1. **verify** (macos-latest, arm64) — checks out the tag, runs `make bridge && make test && make release VERSION=<tag>`, and confirms `./bin/silo-release --version` matches the tag.
2. **publish-release** (ubuntu-latest) — creates the GitHub Release with auto-generated notes, downloads the source tarball, computes its SHA-256.
3. **update-formula** (ubuntu-latest) — clones the tap repo using `TAP_GITHUB_TOKEN`, rewrites `version` / `url` / `sha256` in `Formula/silo.rb`, commits, pushes. Skipped for pre-release tags that contain a dash (e.g., `v0.4.0-rc1`).

After the workflow finishes, users running `brew update && brew upgrade` pull the new version. Fresh installs of `brew install silo` use it immediately.

## One-time setup

The steps to bootstrap this pipeline from scratch:

1. Create the `rchekalov/homebrew-silo` repo on GitHub (empty, public).
2. Copy `scripts/homebrew/silo.rb` from this repo into the tap repo at `Formula/silo.rb`, and push.
3. Generate a fine-grained GitHub token with `contents: write` scope on the tap repo. Store it as `TAP_GITHUB_TOKEN` on this repo under **Settings → Secrets and variables → Actions**.
4. Push a tag: `git tag v0.4.0 && git push origin v0.4.0`.
5. Watch `.github/workflows/release.yml` run. When it finishes, verify the tap repo's `Formula/silo.rb` has the correct version / url / sha256.
6. On a clean macOS 26 machine: `brew install rchekalov/silo/silo && silo --version`.

## Caveats for `silo setup`

Silo creates shims in `~/.silo/bin/`. The user must add that directory to their `PATH` — the formula's `caveats` block reminds them. A future `silo setup` command could detect the user's shell and append the `export PATH=...` line automatically; for now, caveats are the simplest correct approach.

## homebrew-core (later)

A personal tap works immediately but requires users to tap first. Getting into `homebrew-core` means users can just `brew install silo` with no tap step. The core requirements:

- Project must be notable (active users, repo visibility).
- Stable release — no `0.0.x`.
- Must pass `brew audit --new silo`.
- Homebrew-core formulas that invoke `make` + sign are acceptable, but source-build formulas are generally preferred over pre-built binaries.

This is a post-1.0 goal. For the 0.x series, the personal tap is the right choice: faster iteration, no review process, full control.

## Future improvements

- Add a `silo setup` command that appends the `export PATH="$HOME/.silo/bin:$PATH"` line automatically so users don't have to edit their shell profile.
- Ship shell completions (bash, zsh, fish) and install them via the formula.
- Move into homebrew-core once the project is past 1.0 and the formula is stable.
