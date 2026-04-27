#!/bin/bash
set -euo pipefail

# Asserts the deny-by-default network contract for `silo run`:
#
#   1. With no silo.toml (registry-only): the python tool can reach pypi
#      (registry allowlist), but NOT example.com (not in any allowlist).
#   2. With silo.toml `allow = ["*"]`: example.com is reachable (explicit
#      open-internet opt-in).
#
# Runs `silo run python` directly so we exercise the runtime path (Run),
# not the build path (RunSetup). The build path is covered by the existing
# demo-python-quickstart.sh.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"

# Project with no network override at all.
cat > silo.toml <<'EOF'
tools = ["python"]
EOF

echo "Testing: registry-only allowlist permits pypi.org"
# `pip download` is the lightest pypi reach available without writing files.
# We capture stderr because pip writes its progress + errors there.
if ! "$SILO_BIN" run python -m pip download --no-deps --dest /tmp requests==2.31.0 >/tmp/silo-net.allow 2>&1; then
  echo "FAIL: pip download from pypi failed despite registry allowlist"
  tail -30 /tmp/silo-net.allow
  exit 1
fi
echo "PASS: pip reached pypi.org via registry allowlist"

echo "Testing: arbitrary host is denied by default"
# urllib gives a clean traceback on proxy 403/connect-refused that's easy to
# match on. We expect the run to fail (exit nonzero).
set +e
"$SILO_BIN" run python -c "import urllib.request, sys; urllib.request.urlopen('https://example.com', timeout=5); print('REACHED'); sys.exit(0)" >/tmp/silo-net.deny 2>&1
exit_code=$?
set -e

if [ "$exit_code" -eq 0 ] && grep -q "REACHED" /tmp/silo-net.deny; then
  echo "FAIL: example.com was reachable — deny-by-default not enforced"
  tail -30 /tmp/silo-net.deny
  exit 1
fi
# Either the proxy logged a denial, or urllib raised an HTTP/connection error.
if ! grep -qE "proxy: denied example.com|HTTPError|URLError|Forbidden|Connection" /tmp/silo-net.deny; then
  echo "FAIL: expected proxy denial / urllib error for example.com, got:"
  tail -30 /tmp/silo-net.deny
  exit 1
fi
echo "PASS: example.com denied with expected proxy/error signature"

echo 'Testing: allow = ["*"] opens internet for that tool'
cat > silo.toml <<'EOF'
tools = ["python"]

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
allow = ["*"]
EOF

if ! "$SILO_BIN" run python -c "import urllib.request; r = urllib.request.urlopen('https://example.com', timeout=10); print('REACHED', r.status)" >/tmp/silo-net.star 2>&1; then
  echo "FAIL: allow=[*] should permit example.com but the run failed"
  tail -30 /tmp/silo-net.star
  exit 1
fi
if ! grep -q "REACHED 200" /tmp/silo-net.star; then
  echo "FAIL: example.com response missing under allow=[*]"
  tail -30 /tmp/silo-net.star
  exit 1
fi
echo 'PASS: allow = ["*"] reached example.com (200)'
