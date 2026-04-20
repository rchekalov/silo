#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"
PORT=3456
MAX_WAIT=30

WORKDIR=$(mktemp -d)
cleanup() {
  if [ -n "${SILO_PID:-}" ]; then
    kill "$SILO_PID" 2>/dev/null || true
    wait "$SILO_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

cat > "$WORKDIR/.siloconf" <<'EOF'
overrides:
  python:
    network:
      host_access: true
    ports:
      - host: 3456
        guest: 3456
EOF

echo "Testing: port forwarding from host to VM"

cd "$WORKDIR"
"$SILO_BIN" run python -- -m http.server "$PORT" &
SILO_PID=$!

# Wait for server to become ready
elapsed=0
while [ $elapsed -lt $MAX_WAIT ]; do
  if curl -s -o /dev/null -w '' "http://localhost:${PORT}/" 2>/dev/null; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ $elapsed -ge $MAX_WAIT ]; then
  echo "FAIL: Server did not become ready within ${MAX_WAIT}s"
  exit 1
fi

# Verify HTTP response
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${PORT}/")
if [ "$STATUS" != "200" ]; then
  echo "FAIL: Expected HTTP 200 but got $STATUS"
  exit 1
fi

echo "PASS: Port forwarding works (localhost:${PORT} -> VM:${PORT})"

kill "$SILO_PID" 2>/dev/null || true
wait "$SILO_PID" 2>/dev/null || true
unset SILO_PID
