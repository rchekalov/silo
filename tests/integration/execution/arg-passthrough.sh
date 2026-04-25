#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

echo "Testing: multiple arguments pass through correctly"
(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import sys
with open('/workspace/.output', 'w') as f:
    f.write(str(len(sys.argv)))
" arg1 arg2) >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "3" ]; then
  echo "FAIL: expected 3 args (script + arg1 + arg2), got $OUTPUT"
  exit 1
fi
echo "PASS: argument count correct"

echo "Testing: arguments with spaces"
rm -f "$WORKDIR/.output"
(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import sys
with open('/workspace/.output', 'w') as f:
    f.write(sys.argv[1])
" "hello world") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "hello world" ]; then
  echo "FAIL: expected 'hello world', got '$OUTPUT'"
  exit 1
fi
echo "PASS: spaced arguments preserved"

echo "Testing: arguments with special characters"
rm -f "$WORKDIR/.output"
(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import sys
with open('/workspace/.output', 'w') as f:
    f.write(sys.argv[1])
" 'key=value&foo=bar') >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "key=value&foo=bar" ]; then
  echo "FAIL: expected 'key=value&foo=bar', got '$OUTPUT'"
  exit 1
fi
echo "PASS: special character arguments preserved"
