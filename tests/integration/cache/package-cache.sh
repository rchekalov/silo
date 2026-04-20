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

echo "Testing: pip package persists across invocations via workspace mount"

# Install a small package (needs network, install to workspace)
(cd "$WORKDIR" && "$SILO_BIN" run python --shim pip -- install --target /workspace/.pkg requests) >/dev/null 2>&1

# Verify it's importable in the same workspace context
rm -f "$WORKDIR/.output"
(cd "$WORKDIR" && "$SILO_BIN" run python -- -c "
import sys; sys.path.insert(0, '/workspace/.pkg')
import requests
with open('/workspace/.output', 'w') as f:
    f.write(requests.__version__)
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [ -z "$OUTPUT" ]; then
  echo "FAIL: requests not importable via workspace target"
  exit 1
fi
echo "PASS: requests $OUTPUT available via workspace --target"
