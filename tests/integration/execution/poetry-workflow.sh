#!/bin/bash
set -euo pipefail

# Poetry is one of the two dominant non-pip Python package managers. Silo
# doesn't ship it as a first-class tool — users bake it into the python
# rootfs via `silo build`. This test validates that recipe end-to-end:
# bake poetry, run `poetry install` against a real pyproject.toml with
# virtualenvs.in-project so the resulting .venv is auto-activatable.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > silo.toml <<'EOF'
tools = ["python"]

[overrides.python.network]
hostAccess = true

[overrides.python.network.proxy]
# Poetry hits PyPI for resolution + the pythonhosted CDN for wheels.
# install.python-poetry.org is *not* needed — we install poetry via pip,
# not the official installer.
allow = ["pypi.org", "*.pythonhosted.org"]
EOF

cat > pyproject.toml <<'EOF'
[tool.poetry]
name = "silo-poetry-test"
version = "0.1.0"
description = "silo poetry-workflow regression fixture"
authors = ["silo <silo@example.com>"]
package-mode = false

[tool.poetry.dependencies]
python = ">=3.10,<4.0"
certifi = "*"

[build-system]
requires = ["poetry-core"]
build-backend = "poetry.core.masonry.api"
EOF

echo "Testing: silo build python pip install poetry (bake poetry into rootfs)"
build_out=$("$SILO_BIN" build python pip install poetry 2>&1)
if ! echo "$build_out" | grep -qE "Successfully installed.*poetry-"; then
  echo "FAIL: build did not report poetry installation"
  echo "$build_out" | tail -20
  exit 1
fi
echo "PASS: poetry baked"

echo "Testing: poetry config virtualenvs.in-project = true (puts .venv in /workspace)"
"$SILO_BIN" run python -m poetry config virtualenvs.in-project true --local >/dev/null 2>&1
if [ ! -f poetry.toml ]; then
  echo "FAIL: poetry config --local did not write poetry.toml"
  exit 1
fi
echo "PASS: poetry.toml written with in-project venv config"

echo "Testing: poetry install resolves + installs certifi into ./.venv"
install_out=$("$SILO_BIN" run python -m poetry install --no-interaction 2>&1)
if [ ! -d .venv ]; then
  echo "FAIL: .venv not created on host"
  echo "$install_out" | tail -20
  exit 1
fi
sitepkgs=$(ls -d .venv/lib/python*/site-packages 2>/dev/null | head -1)
if [ -z "$sitepkgs" ] || [ ! -f "$sitepkgs/certifi/__init__.py" ]; then
  echo "FAIL: certifi/__init__.py not in $sitepkgs after poetry install"
  echo "$install_out" | tail -20
  exit 1
fi
echo "PASS: poetry installed certifi into .venv on host"

echo "Testing: certifi importable from auto-activated venv"
out=$("$SILO_BIN" run python -c "import certifi; print(certifi.__name__)" 2>&1 | tail -1)
if [ "$out" != "certifi" ]; then
  echo "FAIL: certifi not importable; got: $out"
  exit 1
fi
echo "PASS: certifi importable from auto-activated venv"
