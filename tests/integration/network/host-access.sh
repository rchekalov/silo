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
      host_access: true
EOF

echo "Testing: host.silo.internal resolves when hostAccess is enabled"

(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
import socket
ip = socket.gethostbyname('host.silo.internal')
with open('/workspace/.output', 'w') as f:
    f.write(ip)
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ -z "$OUTPUT" ]; then
  echo "FAIL: host.silo.internal did not resolve"
  exit 1
fi
echo "PASS: host.silo.internal resolves to $OUTPUT"

echo "Testing: DNS resolvers are configured with hostAccess"

rm -f "$WORKDIR/.output"
(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
with open('/etc/resolv.conf') as f:
    content = f.read()
has_dns = '1.1.1.1' in content or '8.8.8.8' in content
with open('/workspace/.output', 'w') as f:
    f.write('HAS_DNS' if has_dns else 'NO_DNS')
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ "$OUTPUT" != "HAS_DNS" ]; then
  echo "FAIL: DNS resolvers not configured"
  exit 1
fi
echo "PASS: DNS resolvers configured for hostAccess"
