#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: host environment variables do not leak into container"

export SILO_TEST_SECRET="should-not-leak-12345"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
import os
val = os.environ.get('SILO_TEST_SECRET', 'not-found')
with open('/workspace/.output', 'w') as f:
    f.write(val)
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "not-found" ]; then
  echo "FAIL: SILO_TEST_SECRET leaked into container (got: $OUTPUT)"
  exit 1
fi

echo "PASS: host env vars do not leak"

echo "Testing: passEnv forwards specified variables"

rm -f "$WORKDIR/.output"
cat > "$WORKDIR/.siloconf" <<'EOF'
passEnv:
  - SILO_TEST_SECRET
EOF

(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
import os
val = os.environ.get('SILO_TEST_SECRET', 'not-found')
with open('/workspace/.output', 'w') as f:
    f.write(val)
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "should-not-leak-12345" ]; then
  echo "FAIL: passEnv did not forward SILO_TEST_SECRET (got: $OUTPUT)"
  exit 1
fi

echo "PASS: passEnv correctly forwards specified variables"
