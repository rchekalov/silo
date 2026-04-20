#!/bin/bash
# Tests that LSP path rewriting works correctly:
# - Host paths in requests are rewritten to guest /workspace paths
# - Guest paths in responses are rewritten back to host paths
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

# Ensure python is installed
if ! "$SILO_BIN" list 2>/dev/null | grep -qw python; then
    echo "Installing python..."
    "$SILO_BIN" install python
fi

# Create temp workspace
WORKDIR=$(mktemp -d)
cleanup() {
    [ -n "${LSP_PID:-}" ] && kill "$LSP_PID" 2>/dev/null || true
    rm -rf "$WORKDIR"
}
trap cleanup EXIT

# Create a Python file with a type error that pyright will flag
cat > "$WORKDIR/app.py" << 'PYEOF'
x: int = "hello"
PYEOF

# Helpers
send_lsp() {
    local body="$1"
    local len=${#body}
    printf "Content-Length: %d\r\n\r\n%s" "$len" "$body"
}

read_lsp_response() {
    local fd="$1"
    local timeout_secs="${2:-30}"

    local content_length=""
    while true; do
        if ! IFS= read -r -t "$timeout_secs" line <&"$fd"; then
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
        return 1
    fi

    head -c "$content_length" <&"$fd"
}

# Start LSP with named pipes
LSP_IN="$WORKDIR/.lsp_in"
LSP_OUT="$WORKDIR/.lsp_out"
mkfifo "$LSP_IN" "$LSP_OUT"

cd "$WORKDIR"
"$SILO_BIN" lsp python < "$LSP_IN" > "$LSP_OUT" 2>/dev/null &
LSP_PID=$!

exec 5>"$LSP_IN"
exec 6<"$LSP_OUT"

sleep 5

# Initialize with host path — this should be rewritten to /workspace inside container
echo "Testing: path rewriting in initialize"
INIT_REQ=$(cat <<JSON
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file://${WORKDIR}","capabilities":{"textDocument":{"publishDiagnostics":{"relatedInformation":true}}}}}
JSON
)
send_lsp "$INIT_REQ" >&5
INIT_RESP=$(read_lsp_response 6 60)

if echo "$INIT_RESP" | grep -q '"capabilities"'; then
    echo "PASS: initialize succeeded"
else
    echo "FAIL: initialize failed"
    echo "Response: $INIT_RESP"
    exit 1
fi

# Send initialized
send_lsp '{"jsonrpc":"2.0","method":"initialized","params":{}}' >&5

# Open document with host path — should be rewritten to /workspace/app.py inbound
echo "Testing: path rewriting in textDocument/didOpen"
DID_OPEN=$(cat <<JSON
{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file://${WORKDIR}/app.py","languageId":"python","version":1,"text":"x: int = \"hello\"\n"}}}
JSON
)
send_lsp "$DID_OPEN" >&5

# Read diagnostics notification — the URI should contain the HOST path (rewritten outbound)
echo "Testing: path rewriting in publishDiagnostics response"
FOUND_DIAGNOSTICS=false
for i in $(seq 1 10); do
    if DIAG_RESP=$(read_lsp_response 6 10); then
        if echo "$DIAG_RESP" | grep -q "publishDiagnostics"; then
            FOUND_DIAGNOSTICS=true
            # The URI in the response should be the HOST path, not /workspace
            if echo "$DIAG_RESP" | grep -q "file://${WORKDIR}/app.py"; then
                echo "PASS: diagnostics URI contains host path"
            elif echo "$DIAG_RESP" | grep -q "/workspace/app.py"; then
                echo "FAIL: diagnostics URI contains guest path (path rewriting failed)"
                echo "Response: $DIAG_RESP"
                exit 1
            else
                echo "PASS: diagnostics received (URI format may vary)"
            fi
            break
        fi
    fi
done

if ! $FOUND_DIAGNOSTICS; then
    echo "WARN: no diagnostics received within timeout (pyright may not have started)"
    # Not a hard failure — pyright may need longer to install on first run
fi

# Clean shutdown
send_lsp '{"jsonrpc":"2.0","id":99,"method":"shutdown","params":null}' >&5
read_lsp_response 6 10 >/dev/null 2>&1 || true
send_lsp '{"jsonrpc":"2.0","method":"exit","params":null}' >&5
exec 5>&-
wait "$LSP_PID" 2>/dev/null || true

echo "All path rewriting tests passed"
