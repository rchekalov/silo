#!/bin/bash
# Backward-compatibility canary for the legacy `--` pass-through delimiter.
# All other integration tests exercise the new positional form (silo flags
# before the tool, command after); this one explicitly uses `--` so the
# legacy strip-after-`--` path can't silently regress.
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

echo "Testing: legacy 'silo run python -- --version' still works"
"$SILO_BIN" run python -- --version >/dev/null 2>&1
echo "PASS: legacy run -- form"

echo "Testing: legacy 'silo run python -- -c <code>' still works"
(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
with open('/workspace/.output', 'w') as f:
    f.write('legacy-ok')
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "legacy-ok" ]; then
  echo "FAIL: legacy form did not pass -c through (got '$OUTPUT')"
  exit 1
fi
echo "PASS: legacy run -- -c form"

echo "Testing: legacy 'silo run python --timing -- --version' still works"
"$SILO_BIN" run python --timing -- --version >/dev/null 2>&1
echo "PASS: legacy form with silo flag before --"
