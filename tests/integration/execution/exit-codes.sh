#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: exit code 0 propagation"
"$SILO_BIN" run python -- -c "print('ok')" >/dev/null 2>&1
echo "PASS: exit 0"

echo "Testing: non-zero exit code propagation"
set +e
"$SILO_BIN" run python -- -c "import sys; sys.exit(42)" >/dev/null 2>&1
EXIT_CODE=$?
set -e

if [ "$EXIT_CODE" -ne 42 ]; then
  echo "FAIL: expected exit code 42, got $EXIT_CODE"
  exit 1
fi
echo "PASS: exit 42 propagated correctly"

echo "Testing: exit code 1 propagation"
set +e
"$SILO_BIN" run python -- -c "raise ValueError('boom')" >/dev/null 2>&1
EXIT_CODE=$?
set -e

if [ "$EXIT_CODE" -ne 1 ]; then
  echo "FAIL: expected exit code 1, got $EXIT_CODE"
  exit 1
fi
echo "PASS: exit 1 propagated correctly"
