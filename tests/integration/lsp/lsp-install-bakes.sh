#!/bin/bash
# Regression guard: `silo install python` bakes pyright into the tool's
# global rootfs, so the LSP is usable without any extra setup step.
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

# Force-reinstall to guarantee the bake runs, even if a prior test left
# python registered without an LSP bake.
"$SILO_BIN" install python --force >/dev/null

# 1. The baked rootfs must exist where runPostInstall targets it.
if [ ! -f "$HOME/.silo/builds/python/rootfs.ext4" ]; then
    echo "FAIL: expected ~/.silo/builds/python/rootfs.ext4 after install" >&2
    exit 1
fi

# 2. Running the tool against the baked rootfs must resolve pyright-langserver.
output=$("$SILO_BIN" run --shim sh python -c 'command -v pyright-langserver' 2>&1)
if ! echo "$output" | grep -q pyright-langserver; then
    echo "FAIL: pyright-langserver not on PATH in baked rootfs" >&2
    echo "output: $output" >&2
    exit 1
fi

echo "PASS: pyright-langserver baked and discoverable"
