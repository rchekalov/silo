#!/bin/bash
set -euo pipefail

# Regression test for the host-mount writeback bug: `silo run python venv/bin/pip
# install …` reported success, but the new packages never appeared in the host
# venv (or in subsequent fresh `silo run` mounts of the same project dir).
# Root cause was the same shape as the build-persistence bug — virtio-fs writes
# back to the host weren't flushed before container teardown. Fix is the
# flushGuestSync exec between Wait and Stop in ephemeralRunner.Run.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > requirements.txt <<'EOF'
requests==2.31.0
EOF
cat > silo.toml <<'EOF'
tools = ["python"]

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
allow = ["pypi.org", "*.pythonhosted.org"]
EOF

echo "Testing: silo run python -m venv venv (creates venv on host)"
"$SILO_BIN" run python -m venv venv >/dev/null 2>&1
if [ ! -x venv/bin/pip ]; then
  echo "FAIL: venv/bin/pip not created on host"
  exit 1
fi
echo "PASS: venv created"

echo "Testing: pip install via venv persists to host filesystem"
"$SILO_BIN" run python venv/bin/pip install -r requirements.txt >/dev/null 2>&1

# The package must exist on the host after the run exits.
sitepkgs=$(ls -d venv/lib/python*/site-packages 2>/dev/null | head -1)
if [ -z "$sitepkgs" ]; then
  echo "FAIL: could not locate site-packages under venv/lib"
  exit 1
fi
if [ ! -f "$sitepkgs/requests/__init__.py" ]; then
  echo "FAIL: requests/__init__.py not on host after pip install"
  ls -la "$sitepkgs"
  exit 1
fi
echo "PASS: requests installed to host venv"

echo "Testing: package importable from a fresh silo run"
ver=$("$SILO_BIN" run python -c "
import sys
sys.path.insert(0, '$sitepkgs')
import requests; print(requests.__version__)
" 2>&1 | tail -1)
if [ "$ver" != "2.31.0" ]; then
  echo "FAIL: expected requests 2.31.0, got: $ver"
  exit 1
fi
echo "PASS: requests 2.31.0 importable from host venv"
