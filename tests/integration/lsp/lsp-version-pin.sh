#!/bin/bash
# Regression guard: a project that pins a different python version than the
# globally installed one triggers a project-scoped bake during `silo sync`.
# The resulting project rootfs carries the pinned python AND the LSP, so the
# language server matches `silo run`'s toolchain.
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

# 1. Global install at 3.14-slim (registry default at the time of writing).
#    Tags must match the registry exactly; the pin below must differ for the
#    image-mismatch branch of `silo sync` to fire.
"$SILO_BIN" install python@3.14-slim --force >/dev/null

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

# 2. Pin the project to a different python version via an image override.
#    `tools:` only takes bare names; the canonical pin syntax (matching
#    `silo use python@3.12-slim`) records the tag under overrides.<tool>.image.
cat > "$WORKDIR/.siloconf" <<'YAML'
tools: [python]
overrides:
  python:
    image: docker.io/library/python:3.12-slim
YAML

# 3. Sync must run cleanly. The auto-bake now lives content-addressed under
#    ~/.silo/baked/<recipeHash>/ rather than <projectRoot>/.silo/<tool>/, so
#    the proof a bake actually happened is the probe in step 4 — pyright +
#    Python 3.12 are only present in the baked rootfs, not the global one.
sync_output=""
if ! sync_output=$(cd "$WORKDIR" && "$SILO_BIN" sync 2>&1); then
    echo "FAIL: silo sync exited non-zero" >&2
    echo "$sync_output" >&2
    exit 1
fi

# 4. The baked rootfs must carry both the pinned python and pyright.
probe=$(cd "$WORKDIR" && "$SILO_BIN" run --shim sh python -c \
    'command -v pyright-langserver && python3 --version' 2>&1)
if ! echo "$probe" | grep -q pyright-langserver; then
    echo "FAIL: pyright-langserver missing from project rootfs" >&2
    echo "probe output: $probe" >&2
    exit 1
fi
if ! echo "$probe" | grep -q "Python 3.12"; then
    echo "FAIL: project rootfs does not report Python 3.12" >&2
    echo "probe output: $probe" >&2
    exit 1
fi

echo "PASS: project rootfs has pyright and matches pinned python 3.12"
