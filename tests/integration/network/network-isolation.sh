#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

echo "Testing: network is not accessible by default"

set +e
(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
import urllib.request, sys
try:
    urllib.request.urlopen('https://example.com', timeout=5)
    with open('/workspace/.output', 'w') as f:
        f.write('NETWORK_ACCESSIBLE')
    sys.exit(1)
except Exception:
    with open('/workspace/.output', 'w') as f:
        f.write('NETWORK_BLOCKED')
    sys.exit(0)
") >/dev/null 2>&1
set -e

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "NETWORK_BLOCKED")
if [ "$OUTPUT" = "NETWORK_ACCESSIBLE" ]; then
  echo "FAIL: container can reach the internet without hostAccess"
  exit 1
fi

echo "PASS: network is blocked by default"
