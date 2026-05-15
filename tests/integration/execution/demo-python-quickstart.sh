#!/bin/bash
set -euo pipefail

# Mirrors the silo-web "Python quickstart" demo end-to-end:
#   silo init --no-interactive            → writes silo.toml
#   silo build python pip install -r ...  → bakes requests into rootfs
#   silo python app.py                    → runs the app inside the saved rootfs
#
# The `requests: 2.31.0` line is the regression guard for the 0.5.2 build-rootfs
# corruption bug fixed in 0.5.3 — without the `&& sync` wrap, requests/ in the
# saved rootfs was a 0-byte file and `import requests` failed.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > requirements.txt <<'EOF'
requests==2.31.0
EOF
cat > app.py <<'EOF'
import os
import platform
import sys

import requests

print(f"OK python {sys.version.split()[0]}")
print(f"  kernel:   {platform.system()} {platform.machine()}")
print(f"  cwd:      {os.getcwd()}")
print(f"  requests: {requests.__version__}")
EOF

echo "Testing: silo init --no-interactive runs without prompts"
"$SILO_BIN" init --no-interactive </dev/null >/dev/null 2>&1
if [ ! -f silo.toml ]; then
  echo "FAIL: silo init did not create silo.toml"
  exit 1
fi
# silo init currently writes TOML literal strings ('python') rather than basic
# strings ("python"); both are spec-valid, accept either.
if ! grep -Eq "^tools = \\[(\"python\"|'python')\\]" silo.toml; then
  echo "FAIL: silo.toml missing tools = [\"python\"]"
  cat silo.toml
  exit 1
fi
if ! grep -Eq "(\"\\.venv\"|'\\.venv')" silo.toml || ! grep -Eq "(\"__pycache__\"|'__pycache__')" silo.toml; then
  echo "FAIL: silo.toml missing .venv / __pycache__ in mount.exclude"
  cat silo.toml
  exit 1
fi
echo "PASS: silo init wrote silo.toml with expected tools + exclude"

# silo init for a python-only project doesn't add a network override, so pip
# can't reach pypi out of the box. Append the override the demo flow needs.
cat >> silo.toml <<'EOF'

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
allow = ["pypi.org", "*.pythonhosted.org"]
EOF

echo "Testing: silo build python pip install -r requirements.txt"
build_out=$("$SILO_BIN" build python pip install -r requirements.txt 2>&1)
if ! echo "$build_out" | grep -q "Successfully installed.*requests-2\.31\.0"; then
  echo "FAIL: build did not report 'Successfully installed … requests-2.31.0'"
  echo "$build_out" | tail -20
  exit 1
fi
if ! echo "$build_out" | grep -q "Setup complete. Rootfs saved to.*\.silo/python/rootfs.ext4"; then
  echo "FAIL: build did not report Setup complete with expected rootfs path"
  echo "$build_out" | tail -20
  exit 1
fi
if [ ! -s .silo/python/rootfs.ext4 ]; then
  echo "FAIL: .silo/python/rootfs.ext4 missing or empty"
  exit 1
fi
size=$(stat -f%z .silo/python/rootfs.ext4 2>/dev/null || stat -c%s .silo/python/rootfs.ext4)
if [ "$size" -lt 1048576 ]; then
  echo "FAIL: rootfs is $size bytes, expected > 1 MiB"
  exit 1
fi
echo "PASS: build wrote a non-empty rootfs ($size bytes)"

echo "Testing: silo python app.py runs inside the saved rootfs and requests loads"
run_out=$("$SILO_BIN" python app.py 2>&1)
if ! echo "$run_out" | grep -q "kernel:.*Linux aarch64"; then
  echo "FAIL: expected 'kernel: Linux aarch64' (proves we ran inside the VM)"
  echo "$run_out"
  exit 1
fi
if ! echo "$run_out" | grep -q "cwd:.*/workspace"; then
  echo "FAIL: expected 'cwd: /workspace' (proves project mount worked)"
  echo "$run_out"
  exit 1
fi
if ! echo "$run_out" | grep -q "requests: 2\.31\.0"; then
  echo "FAIL: requests 2.31.0 not loaded — the saved rootfs is the 0.5.2 corruption shape"
  echo "$run_out"
  exit 1
fi
echo "PASS: requests 2.31.0 loaded from saved rootfs, ran inside Linux VM at /workspace"
