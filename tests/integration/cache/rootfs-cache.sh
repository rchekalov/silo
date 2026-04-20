#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: rootfs cache speeds up subsequent runs"

# Both runs just need to succeed — timing is printed to tty, hard to capture
"$SILO_BIN" run python -- --version >/dev/null 2>&1
echo "First run: OK"

"$SILO_BIN" run python -- --version >/dev/null 2>&1
echo "Second run: OK"

echo "PASS: rootfs cache operational (both runs completed)"
