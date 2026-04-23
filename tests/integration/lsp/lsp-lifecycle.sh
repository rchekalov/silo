#!/bin/bash
# Tests the full LSP lifecycle: container boot → LSP server start → JSON-RPC proxying → shutdown.
# Sends raw Content-Length framed LSP messages to `silo lsp python` and validates responses.
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"
TIMEOUT=120

# Ensure python is installed AND the LSP rootfs is baked (pyright in
# ~/.silo/builds/python/rootfs.ext4). An install that predates the
# lsp.install wiring registers the tool without baking pyright, so
# `silo lsp` still fails — force a reinstall in that case.
if ! "$SILO_BIN" list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx python ||
   [ ! -f "$HOME/.silo/builds/python/rootfs.ext4" ]; then
    echo "Installing python (baking LSP)..."
    "$SILO_BIN" install python --force
fi

# Create temp workspace with a Python file
WORKDIR=$(mktemp -d)
cleanup() {
    # Kill background processes
    [ -n "${LSP_PID:-}" ] && kill "$LSP_PID" 2>/dev/null || true
    rm -rf "$WORKDIR"
}
trap cleanup EXIT

cat > "$WORKDIR/main.py" << 'PYEOF'
x: int = "not_an_int"
PYEOF

# Helper: send a Content-Length framed LSP message
send_lsp() {
    local body="$1"
    local len=${#body}
    printf "Content-Length: %d\r\n\r\n%s" "$len" "$body"
}

# Helper: read one LSP response body (returns via stdout)
read_lsp_response() {
    local fd="$1"
    local timeout_secs="${2:-30}"

    # Read headers line by line until empty line
    local content_length=""
    while true; do
        if ! IFS= read -r -t "$timeout_secs" line <&"$fd"; then
            echo "ERROR: timeout or EOF reading LSP header" >&2
            return 1
        fi
        line="${line%$'\r'}"
        if [ -z "$line" ]; then
            break
        fi
        if echo "$line" | grep -qi "^content-length:"; then
            content_length=$(echo "$line" | sed 's/[^0-9]//g')
        fi
    done

    if [ -z "$content_length" ]; then
        echo "ERROR: no Content-Length header found" >&2
        return 1
    fi

    # Read exactly content_length bytes. Must be `dd bs=1` (one byte at a
    # time) — `head -c` uses buffered I/O and drains the fifo past the body,
    # silently discarding later frames.
    dd bs=1 count="$content_length" <&"$fd" 2>/dev/null
}

echo "Starting silo lsp python..."

# Create named pipes for communication
LSP_IN="$WORKDIR/.lsp_in"
LSP_OUT="$WORKDIR/.lsp_out"
mkfifo "$LSP_IN" "$LSP_OUT"

# Start the LSP process with named pipes
cd "$WORKDIR"
"$SILO_BIN" lsp python < "$LSP_IN" > "$LSP_OUT" 2>/dev/null &
LSP_PID=$!

# Open FDs for writing to and reading from the LSP
exec 5>"$LSP_IN"
exec 6<"$LSP_OUT"

# Give the LSP server time to start
sleep 5

# --- Test 1: Initialize ---
echo "Testing: LSP initialize request"
INIT_REQ=$(cat <<'JSON'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file:///tmp/test-workspace","capabilities":{}}}
JSON
)
send_lsp "$INIT_REQ" >&5

# Skip server-initiated notifications (window/logMessage, progress, etc.)
# until we get the response to our initialize request (matched by "id":1).
INIT_RESP=""
for _ in 1 2 3 4 5; do
    frame=$(read_lsp_response 6 60)
    if echo "$frame" | grep -q '"id":1'; then
        INIT_RESP="$frame"
        break
    fi
done
if [ -z "$INIT_RESP" ]; then
    echo "FAIL: no frame with id:1 received after initialize"
    exit 1
fi
if echo "$INIT_RESP" | grep -q '"capabilities"'; then
    echo "PASS: initialize response contains capabilities"
else
    echo "FAIL: initialize response missing capabilities"
    echo "Response: $INIT_RESP"
    exit 1
fi
echo "PASS: initialize response has correct id"

# --- Test 2: Initialized notification ---
echo "Testing: LSP initialized notification"
INITIALIZED='{"jsonrpc":"2.0","method":"initialized","params":{}}'
send_lsp "$INITIALIZED" >&5
echo "PASS: initialized notification sent"

# --- Test 3: Shutdown ---
echo "Testing: LSP shutdown request"
SHUTDOWN='{"jsonrpc":"2.0","id":2,"method":"shutdown","params":null}'
send_lsp "$SHUTDOWN" >&5

# Same skip-notifications loop as initialize — pyright can send diagnostics
# or progress frames between shutdown and its response.
SHUTDOWN_RESP=""
for _ in 1 2 3 4 5; do
    frame=$(read_lsp_response 6 30)
    if echo "$frame" | grep -q '"id":2'; then
        SHUTDOWN_RESP="$frame"
        break
    fi
done
if [ -n "$SHUTDOWN_RESP" ]; then
    echo "PASS: shutdown response received"
else
    echo "FAIL: shutdown response missing or wrong"
    exit 1
fi

# --- Test 4: Exit ---
echo "Testing: LSP exit notification"
EXIT_MSG='{"jsonrpc":"2.0","method":"exit","params":null}'
send_lsp "$EXIT_MSG" >&5

# Close our write end
exec 5>&-

# Wait for LSP process to exit
set +e
wait "$LSP_PID"
EXIT_CODE=$?
set -e

if [ "$EXIT_CODE" -eq 0 ]; then
    echo "PASS: LSP server exited cleanly"
else
    echo "FAIL: LSP server exited with code $EXIT_CODE"
    exit 1
fi

echo "All LSP lifecycle tests passed"
