#!/bin/bash
set -euo pipefail

# Regression test for the rootfs corruption that hit `silo build python pip
# install …` before the `&& sync` wrap was added to runBuildOnce.
#
# Symptom: pip install reported success and the build wrote
# <project>/.silo/python/rootfs.ext4, but on the next `silo run python` the
# top-level package directories under site-packages were 0-byte regular files
# (mode 0o100644, nlink=1, size=0) instead of real directories. The fix wraps
# the build command in `sh -c "<cmd> && sync"` so the guest flushes its page
# cache before RunSetup snapshots the ext4.

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

echo "Testing: silo build python pip install -r requirements.txt"
"$SILO_BIN" build python pip install -r requirements.txt >/dev/null 2>&1
echo "PASS: build completed"

echo "Testing: saved rootfs has real package directories (not 0-byte files)"
out=$("$SILO_BIN" run python -c '
import os, sys
candidates = [p for p in sys.path if p.endswith("site-packages")]
for site in candidates:
    for name in ("requests", "urllib3", "idna", "certifi", "charset_normalizer"):
        full = os.path.join(site, name)
        if not os.path.isdir(full):
            print(f"BROKEN {full} -> {os.lstat(full) if os.path.exists(full) else None}")
            sys.exit(1)
print("OK")
' 2>&1)
if ! echo "$out" | grep -q "^OK$"; then
  echo "FAIL: build saved a corrupted rootfs"
  echo "$out"
  exit 1
fi
echo "PASS: rootfs directories intact"

echo "Testing: imported package actually works inside the saved rootfs"
ver=$("$SILO_BIN" run python -c "import requests; print(requests.__version__)" 2>&1)
if [ "$ver" != "2.31.0" ]; then
  echo "FAIL: expected requests 2.31.0, got: $ver"
  exit 1
fi
echo "PASS: requests 2.31.0 importable"
