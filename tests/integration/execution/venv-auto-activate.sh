#!/bin/bash
set -euo pipefail

# Asserts the auto-activation path implemented by applyVenvAutoActivate
# (internal/engine/ephemeral.go). Three claims under test:
#
#   1. With ./.venv at the project root, `silo run python` enters the venv
#      (sys.prefix == /workspace/.venv) without the user sourcing anything.
#   2. The same activation fires when pip is invoked through its host shim
#      (~/.silo/bin/pip), which wraps to `silo run python --shim pip ...` —
#      so `pip install <pkg>` lands in the project venv, not the rootfs.
#   3. The plain `venv` directory name (no leading dot) works the same way,
#      and activation also fires when silo is invoked from a subdirectory
#      of the project root (cwd != project root, but venv is found by walk-up).

SILO_BIN="${SILO_BIN:-silo}"
SHIM_DIR="$HOME/.silo/bin"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > silo.toml <<'EOF'
tools = ["python"]

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
allow = ["pypi.org", "*.pythonhosted.org"]
EOF

# ---- Pass 1: .venv (dotfile, the modern default) ----

echo "Testing: silo run python -m venv .venv creates .venv on host"
"$SILO_BIN" run python -m venv .venv >/dev/null 2>&1
# bin/python is a symlink into the guest rootfs; check the real script bin/pip
# instead so we don't fail on host-side symlink resolution.
if [ ! -x .venv/bin/pip ]; then
  echo "FAIL: .venv/bin/pip not created on host"
  exit 1
fi
echo "PASS: .venv created"

echo "Testing: sys.prefix points at /workspace/.venv (auto-activation fired)"
prefix=$("$SILO_BIN" run python -c "import sys; print(sys.prefix)" 2>&1 | tail -1)
if [ "$prefix" != "/workspace/.venv" ]; then
  echo "FAIL: expected sys.prefix=/workspace/.venv, got: $prefix"
  exit 1
fi
echo "PASS: auto-activation set sys.prefix correctly"

echo "Testing: pip via host shim installs into .venv (not rootfs site-packages)"
if [ ! -x "$SHIM_DIR/pip" ]; then
  echo "FAIL: $SHIM_DIR/pip shim missing — `silo install python` first"
  exit 1
fi
"$SHIM_DIR/pip" install --quiet --disable-pip-version-check certifi >/dev/null 2>&1
sitepkgs=$(ls -d .venv/lib/python*/site-packages 2>/dev/null | head -1)
if [ -z "$sitepkgs" ] || [ ! -f "$sitepkgs/certifi/__init__.py" ]; then
  echo "FAIL: certifi/__init__.py not in $sitepkgs after pip-via-shim install"
  ls -la "$sitepkgs" 2>/dev/null || echo "(no site-packages dir)"
  exit 1
fi
echo "PASS: pip-via-shim installed into project venv"

# ---- Pass 2: subdirectory invocation, plain `venv` name ----

echo "Testing: rename .venv → venv and invoke from a subdirectory"
mv .venv venv
mkdir -p sub/nested
cd sub/nested
prefix=$("$SILO_BIN" run python -c "import sys; print(sys.prefix)" 2>&1 | tail -1)
if [ "$prefix" != "/workspace/venv" ]; then
  echo "FAIL: from subdir, expected sys.prefix=/workspace/venv, got: $prefix"
  exit 1
fi
echo "PASS: project-root venv resolves from subdirectory"

# Verify the package installed in pass 1 is still importable through the
# renamed venv — auto-activation works regardless of cwd.
import_out=$("$SILO_BIN" run python -c "import certifi; print(certifi.__name__)" 2>&1 | tail -1)
if [ "$import_out" != "certifi" ]; then
  echo "FAIL: certifi not importable from venv at /workspace/venv (got: $import_out)"
  exit 1
fi
echo "PASS: certifi importable from auto-activated venv across rename + subdir"
