# Silo — Homebrew Distribution

## How it works for the user

```bash
brew install rchekalov/silo/silo
```

The fully-qualified name is required because homebrew-cask already has a `silo` cask (an unrelated macOS app). Using just `brew install silo` resolves to that cask. The three-part form `rchekalov/silo/silo` (user/tap/formula) forces Homebrew to pick our formula.

Homebrew downloads a prebuilt tarball attached to the tagged GitHub Release, extracts the signed `silo` binary and `libSiloBridge.dylib` into the Homebrew prefix, and is done. No Swift or Go toolchain is pulled in — the tarball is ad-hoc codesigned with the virtualization entitlement at build time on CI's `macos-latest` runner, and ad-hoc signatures survive tar/untar.

Install time on a recent M-series: a few seconds (~10 MB download + extract).

Source-build via `git clone && make install` still works for auditors who want to rebuild from source.

## What's in play

### Two repositories

- **`rchekalov/silo`** — main source repo. Tag pushes trigger `release.yml`.
- **`rchekalov/homebrew-silo`** — the tap repo. Just a `Formula/silo.rb` file.

The tap repo's `homebrew-` prefix is required by Homebrew convention — it lets users run `brew tap rchekalov/silo` instead of `brew tap rchekalov/homebrew-silo`.

### Prebuilt-binary formula

The canonical formula lives at `scripts/homebrew/silo.rb` in this repo (seed copy). On every tag the release workflow rewrites `version`, `url`, and `sha256` in the tap repo's `Formula/silo.rb`.

Key formula properties:

- `url` points at the prebuilt tarball attached to the GitHub Release: `.../releases/download/v<version>/silo-<version>-macos-arm64.tar.gz`.
- No build deps. No `depends_on "go"`, no `depends_on "swift"`. That drops ~3.5 GB of transitive deps — Swift 6 pulls in Python (via LLDB), `openssl@3`, `sqlite`, `readline`, `xz`, `zstd`, `ca-certificates`, and none of those run Silo.
- `depends_on :macos`, `depends_on arch: :arm64`, `depends_on macos: :tahoe` — macOS 26 on Apple Silicon only. `libSiloBridge.dylib` links only against the system Swift runtime bundled in macOS 26 (`/usr/lib/swift/...`), not Homebrew Swift.
- `install` is two lines: `bin.install "bin/silo"` and `(lib/"silo").install Dir["lib/silo/*"]`. The binary has an `@executable_path/../lib/silo` rpath baked in at build time, so it resolves the dylib under any prefix without further relinking.
- `caveats` tells the user about `PATH` and the one-time runtime bootstrap.

### The `release-bundle` + `release-tarball` Makefile targets

```
make release-bundle PREFIX=<dir> VERSION=<tag>
make release-tarball PREFIX=<dir> VERSION=<tag> OUT_DIR=<dir>
```

- `release-bundle` lays out `$PREFIX/bin/silo` + `$PREFIX/lib/silo/libSiloBridge.dylib`, bakes `VERSION` into the binary via `-ldflags -X ...version.Version=...`, and ad-hoc codesigns both files with `silo.entitlements`.
- `release-tarball` tars `$PREFIX/{bin,lib}` into `$OUT_DIR/silo-<VERSION>-macos-arm64.tar.gz` and writes an `.sha256` sidecar next to it.
- The release build rpath is `@executable_path/../lib/silo` (relative), so the tarball is reusable under any install prefix.

## Release workflow

`.github/workflows/release.yml` fires on tag push `v*`:

1. **verify** (macos-latest, arm64) — builds + tests, runs `make release-bundle PREFIX=$STAGE VERSION=<tag>`, confirms `$STAGE/bin/silo --version` matches the tag, then runs `make release-tarball` to produce `silo-<version>-macos-arm64.tar.gz` + sidecar, and also builds the `silo-runtime-arm64.tar.gz` runtime bundle (vmlinux + initfs.ext4). Both artifacts are uploaded via `actions/upload-artifact`.
2. **publish-release** (ubuntu-latest) — downloads both artifacts, reads the prebuilt-tarball sha256 from its sidecar file (no re-hash, no network fetch), and creates the GitHub Release with all four files attached (prebuilt tarball + sidecar, runtime bundle + sidecar).
3. **update-formula** (ubuntu-latest) — clones the tap repo using `TAP_GITHUB_TOKEN`, rewrites `version` / `url` / `sha256` in `Formula/silo.rb` to point at the prebuilt-tarball release-asset URL, commits, pushes. Skipped for pre-release tags that contain a dash (e.g., `v0.4.0-rc1`).

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
- Homebrew-core prefers source-build formulas over prebuilt-binary formulas; the tap formula's current prebuilt-download shape would need to be reworked for core (either as a `bottle` produced by Homebrew's build farm, or a source-build formula using Homebrew-supplied `go` + `swift`).

This is a post-1.0 goal. For the 0.x series, the personal tap is the right choice: faster iteration, no review process, full control.

## Future improvements

- Add a `silo setup` command that appends the `export PATH="$HOME/.silo/bin:$PATH"` line automatically so users don't have to edit their shell profile.
- Ship shell completions (bash, zsh, fish) and install them via the formula.
- Move into homebrew-core once the project is past 1.0 and the formula is stable.
