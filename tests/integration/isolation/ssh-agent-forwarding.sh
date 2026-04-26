#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: --ssh-agent forwards \$SSH_AUTH_SOCK and host keys never enter the guest"

# Skip if the host has no SSH agent — there's nothing to forward.
if [ -z "${SSH_AUTH_SOCK:-}" ] || [ ! -S "$SSH_AUTH_SOCK" ]; then
  echo "SKIP: host has no \$SSH_AUTH_SOCK"
  exit 0
fi

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

# Negative case first: no --ssh-agent → no socket, no env var.
NEG_OUTPUT=$(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import os, stat
sock_env = os.environ.get('SSH_AUTH_SOCK', '')
exists = os.path.exists('/run/silo/ssh-agent.sock')
print(f'env={sock_env}|exists={exists}')
" 2>&1 | tr -d '\r')
if [[ "$NEG_OUTPUT" != *"env=|exists=False"* ]]; then
  echo "FAIL: without --ssh-agent, expected socket absent + env unset, got: $NEG_OUTPUT"
  exit 1
fi
echo "PASS: socket relay off by default"

# Positive case: --ssh-agent on, env var set, socket reachable, no key files.
POS_OUTPUT=$(cd "$WORKDIR" && "$SILO_BIN" run --ssh-agent python -c "
import os, socket, stat
sock_path = os.environ.get('SSH_AUTH_SOCK', '')
exists = os.path.exists(sock_path) if sock_path else False
is_socket = stat.S_ISSOCK(os.stat(sock_path).st_mode) if exists else False

# Try to send SSH_AGENTC_REQUEST_IDENTITIES (msg type 11) and read the reply
# header. A failure here means the relay is broken — bytes don't make it
# through the vsock pump.
relay_works = False
try:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(3)
    s.connect(sock_path)
    s.sendall(b'\\x00\\x00\\x00\\x01\\x0b')
    data = s.recv(5)
    s.close()
    relay_works = len(data) == 5
except Exception as e:
    relay_works = f'error: {e}'

# No host keys in guest filesystem — the whole point of agent forwarding.
no_keys = not any(os.path.exists(p) for p in [
    '/root/.ssh/id_ed25519', '/root/.ssh/id_rsa',
    '/home/.ssh/id_ed25519', '/Users',
])

print(f'env={sock_path}|isSocket={is_socket}|relay={relay_works}|noKeys={no_keys}')
" 2>&1 | tr -d '\r')

if [[ "$POS_OUTPUT" != *"env=/run/silo/ssh-agent.sock"* ]]; then
  echo "FAIL: SSH_AUTH_SOCK not set to /run/silo/ssh-agent.sock: $POS_OUTPUT"
  exit 1
fi
if [[ "$POS_OUTPUT" != *"isSocket=True"* ]]; then
  echo "FAIL: /run/silo/ssh-agent.sock is not a real socket: $POS_OUTPUT"
  exit 1
fi
if [[ "$POS_OUTPUT" != *"relay=True"* ]]; then
  echo "FAIL: agent protocol bytes did not relay end-to-end: $POS_OUTPUT"
  exit 1
fi
if [[ "$POS_OUTPUT" != *"noKeys=True"* ]]; then
  echo "FAIL: host private keys leaked into the guest: $POS_OUTPUT"
  exit 1
fi

echo "PASS: --ssh-agent forwards agent socket without leaking key files"
