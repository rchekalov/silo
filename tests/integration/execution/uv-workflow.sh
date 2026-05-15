#!/bin/bash
set -euo pipefail

# uv is the other dominant non-pip Python package manager (Astral, 2024+).
# Like poetry, it's not a first-class silo tool; users bake it into the
# python rootfs. Validates the bake recipe + uv venv/install workflow end
# to end. Also asserts that the uv cache mount added in registry.yaml is
# actually populated on the host after install — proves the new mount works.
#
# Invocation pattern: `silo run python --shim uv -- <subcmd>` rather than
# `silo run python -m uv …`. The latter resolves python via PATH, which
# auto-activation points at /workspace/.venv/bin/python — and the venv has
# no uv module (uv lives in the rootfs's site-packages). Going through the
# uv binary directly sidesteps that asymmetry.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

UV_CACHE_DIR="$HOME/.silo/cache/python/uv"

cd "$WORKDIR"
cat > silo.toml <<'EOF'
tools = ["python"]

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
allow = ["pypi.org", "*.pythonhosted.org"]

[overrides.python.env]
# Don't let uv try to download a managed CPython — host network policy
# doesn't allowlist astral CDNs, and we have a perfectly good interpreter
# already in the rootfs. uv falls back to that.
UV_PYTHON_DOWNLOADS = "never"
EOF

echo "Testing: silo build python pip install uv (bake uv into rootfs)"
build_out=$("$SILO_BIN" build python pip install uv 2>&1)
if ! echo "$build_out" | grep -qE "Successfully installed.*uv-"; then
  echo "FAIL: build did not report uv installation"
  echo "$build_out" | tail -20
  exit 1
fi
echo "PASS: uv baked"

echo "Testing: uv venv creates .venv on host"
"$SILO_BIN" run python --shim uv -- venv >/dev/null 2>&1
if [ ! -d .venv/bin ]; then
  echo "FAIL: uv venv did not create .venv/bin on host"
  exit 1
fi
echo "PASS: uv venv created .venv"

echo "Testing: uv pip install certifi populates the venv"
install_out=$("$SILO_BIN" run python --shim uv -- pip install certifi 2>&1)
sitepkgs=$(ls -d .venv/lib/python*/site-packages 2>/dev/null | head -1)
if [ -z "$sitepkgs" ] || [ ! -f "$sitepkgs/certifi/__init__.py" ]; then
  echo "FAIL: certifi/__init__.py not in $sitepkgs after uv pip install"
  echo "$install_out" | tail -20
  exit 1
fi
echo "PASS: uv installed certifi into .venv"

echo "Testing: certifi importable from auto-activated venv"
out=$("$SILO_BIN" run python -c "import certifi; print(certifi.__name__)" 2>&1 | tail -1)
if [ "$out" != "certifi" ]; then
  echo "FAIL: certifi not importable; got: $out"
  exit 1
fi
echo "PASS: certifi importable from auto-activated venv"

echo "Testing: ~/.silo/cache/python/uv mount is effective (uv-marker files on host)"
# uv writes a CACHEDIR.TAG and an archive-v0/ subtree on first use. Their
# presence on the host proves the /root/.cache/uv guest mount is bound to
# this host directory — independent of warm-cache state from prior runs.
if [ ! -f "$UV_CACHE_DIR/CACHEDIR.TAG" ] || [ ! -d "$UV_CACHE_DIR/archive-v0" ]; then
  echo "FAIL: $UV_CACHE_DIR is missing uv's cache markers (CACHEDIR.TAG / archive-v0/)"
  echo "      The /root/.cache/uv mount in registry.yaml is not effective."
  ls -la "$UV_CACHE_DIR" 2>&1 | head -10
  exit 1
fi
echo "PASS: uv cache mount populated (CACHEDIR.TAG + archive-v0/ present on host)"
