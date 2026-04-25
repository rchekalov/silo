#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: running a non-existent tool gives clear error"

set +e
OUTPUT=$("$SILO_BIN" run nonexistent-tool-xyz --version 2>&1)
EXIT_CODE=$?
set -e

if [ "$EXIT_CODE" -eq 0 ]; then
  echo "FAIL: expected non-zero exit code for missing tool"
  exit 1
fi

if echo "$OUTPUT" | grep -qi "not installed\|not found\|unknown tool\|no tool"; then
  echo "PASS: clear error message for missing tool"
else
  echo "FAIL: error message not helpful: $OUTPUT"
  exit 1
fi
