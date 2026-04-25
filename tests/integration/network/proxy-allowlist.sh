#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cat > "$WORKDIR/.siloconf" <<'EOF'
overrides:
  python:
    network:
      hostAccess: true
      proxy:
        allow:
          - api.github.com
EOF

echo "Testing: allowlisted host is reachable through the proxy"

(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import urllib.request
urllib.request.urlopen('https://api.github.com', timeout=15)
with open('/workspace/.output', 'w') as f:
    f.write('OK')
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "OK" ]; then
  echo "FAIL: allowlisted host api.github.com was not reachable through the proxy"
  exit 1
fi
echo "PASS: allowlisted host reachable through the proxy"

echo "Testing: non-allowlisted host is blocked by the proxy"

rm -f "$WORKDIR/.output"
set +e
(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import urllib.request, sys
try:
    urllib.request.urlopen('https://example.com', timeout=15)
    with open('/workspace/.output', 'w') as f:
        f.write('REACHED')
    sys.exit(1)
except Exception:
    with open('/workspace/.output', 'w') as f:
        f.write('DENIED')
    sys.exit(0)
") >/dev/null 2>&1
set -e

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "DENIED" ]; then
  echo "FAIL: non-allowlisted host example.com was not blocked (got '$OUTPUT')"
  exit 1
fi
echo "PASS: non-allowlisted host blocked by the proxy"
