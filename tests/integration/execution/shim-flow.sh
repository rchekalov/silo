#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"
SHIM_DIR="$HOME/.silo/bin"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

echo "Testing: python shim exists and works"
if [ ! -x "$SHIM_DIR/python" ]; then
  echo "FAIL: $SHIM_DIR/python not found or not executable"
  exit 1
fi

(cd "$WORKDIR" && "$SHIM_DIR/python" -c "
import sys
with open('/workspace/.output', 'w') as f:
    f.write(sys.version.split()[0])
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ -z "$OUTPUT" ]; then
  echo "FAIL: python shim produced no output"
  exit 1
fi
echo "PASS: python shim works (Python $OUTPUT)"

echo "Testing: pip shim exists and works"
if [ ! -x "$SHIM_DIR/pip" ]; then
  echo "FAIL: $SHIM_DIR/pip not found or not executable"
  exit 1
fi

rm -f "$WORKDIR/.output"
(cd "$WORKDIR" && "$SHIM_DIR/pip" --version) >/dev/null 2>&1
# pip --version just needs to exit 0
echo "PASS: pip shim works"
