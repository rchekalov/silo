# Contributing to Silo

Thanks for taking the time to contribute. Silo is an alpha-stage project — issues, bug reports, and pull requests are all welcome.

## Getting started

### Prerequisites

- Apple Silicon Mac (M1 or later)
- macOS 26 (Tahoe) or later
- Xcode Command Line Tools (`xcode-select --install`)
- Go 1.25 or later (`brew install go`)
- Swift 6.2 or later (usually provided by the Xcode CLT; `brew install swift` if needed)

### Clone and build

```bash
git clone https://github.com/rchekalov/silo.git
cd silo

# Build + ad-hoc sign the debug binary (required — unsigned binaries are SIGKILL'd by macOS)
make sign-debug

# Run the signed binary
./bin/silo --version
```

### Codesigning is required

Silo calls into Apple's Virtualization.framework, which requires the `com.apple.security.virtualization` entitlement. Every build must be codesigned with `silo.entitlements` or macOS will kill the process with SIGKILL the moment it tries to create a VM.

- Use `make sign-debug` for day-to-day development.
- Use `make install` for a release build installed into `/usr/local/bin`.
- Never run raw `go build` output directly — it will crash.

See `CLAUDE.md` → "Troubleshooting" for more on entitlements and signing.

## Dev loop

```bash
# Make your change, then:
make sign-debug              # rebuild + sign
./bin/silo <command>         # test manually

# Or use `make install` for an end-to-end smoke test with the on-PATH binary
make install
silo <command>
```

## Tests

```bash
make test          # Go unit tests
make test-vm       # VM integration tests (tests/integration/*.sh) — signed binary required
```

Unit tests live next to the code they test. Integration tests are shell scripts driven by `$SILO_BIN` and should stay implementation-agnostic.

When adding a feature that changes behavior visible to users or Claude (the agent reading this file), write an integration test or extend an existing one. See `docs/integration-testing.md`.

## Pull request guidelines

- Keep PRs focused. One PR per logical change.
- Run `make test` locally before pushing.
- Update `CLAUDE.md` and `docs/features.md` in the **same commit** as any behavior change. These two files are the source of truth for what silo can do; keeping them in sync prevents drift.
- Every source file starts with `// SPDX-License-Identifier: Apache-2.0` (Go/Swift) or the matching comment syntax for the file type.
- Commit messages follow the existing style (see `git log` — short imperative subject, optional body explaining the "why").

## Reporting bugs

Use the bug report issue template. Include:

- macOS version and architecture (`sw_vers`, `uname -m`)
- silo version (`silo --version`)
- The exact command you ran
- The output (redact anything sensitive)
- Output of `silo doctor` if the bug seems runtime-related

## Project layout

`CLAUDE.md` at the repo root documents the full project structure, architecture, build targets, and conventions. Read it first.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0 (see `LICENSE`).
