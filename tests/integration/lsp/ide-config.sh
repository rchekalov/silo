#!/bin/bash
# Tests IDE config generation for VS Code, Zed, and Neovim.
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

# Ensure python is installed (need a real tool with LSP config)
if ! "$SILO_BIN" list 2>/dev/null | grep -qw python; then
    echo "Installing python..."
    "$SILO_BIN" install python
fi

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT
cd "$WORKDIR"

# --- Test 1: VS Code config ---
echo "Testing: silo ide vscode"
"$SILO_BIN" ide vscode 2>/dev/null

if [ ! -f ".vscode/settings.json" ]; then
    echo "FAIL: .vscode/settings.json not created"
    exit 1
fi

if grep -q "python.languageServer" .vscode/settings.json; then
    echo "PASS: VS Code settings contain python.languageServer"
else
    echo "FAIL: VS Code settings missing python.languageServer"
    cat .vscode/settings.json
    exit 1
fi

# --- Test 2: Neovim config ---
echo "Testing: silo ide neovim"
"$SILO_BIN" ide neovim 2>/dev/null

if [ ! -f ".nvim-silo.lua" ]; then
    echo "FAIL: .nvim-silo.lua not created"
    exit 1
fi

if grep -q "lspconfig.pyright.setup" .nvim-silo.lua; then
    echo "PASS: Neovim config contains pyright setup"
else
    echo "FAIL: Neovim config missing pyright setup"
    cat .nvim-silo.lua
    exit 1
fi

if grep -q "cmd = { 'silo', 'lsp', 'python' }" .nvim-silo.lua; then
    echo "PASS: Neovim config has correct silo lsp command"
else
    echo "FAIL: Neovim config missing silo lsp command"
    cat .nvim-silo.lua
    exit 1
fi

# --- Test 3: Zed config ---
echo "Testing: silo ide zed"
"$SILO_BIN" ide zed 2>/dev/null

if [ ! -f ".zed/settings.json" ]; then
    echo "FAIL: .zed/settings.json not created"
    exit 1
fi

if grep -q "pyright-langserver" .zed/settings.json; then
    echo "PASS: Zed settings contain pyright-langserver"
else
    echo "FAIL: Zed settings missing pyright-langserver"
    cat .zed/settings.json
    exit 1
fi

# --- Test 4: Unsupported IDE ---
echo "Testing: silo ide emacs (should fail)"
set +e
"$SILO_BIN" ide emacs 2>&1 | grep -qi "unsupported"
RESULT=$?
set -e

if [ "$RESULT" -eq 0 ]; then
    echo "PASS: unsupported IDE gives clear error"
else
    echo "FAIL: unsupported IDE did not give clear error"
    exit 1
fi

echo "All IDE config tests passed"
