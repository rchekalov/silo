#!/bin/bash
set -euo pipefail

# Modern Python project shape: pyproject.toml + src/ layout + editable install.
# Asserts that `pip install -e .` works inside silo and the resulting
# .pth/.dist-info entries persist on the host venv across separate `silo run`
# invocations (proves editable installs survive container teardown via the
# host-mounted venv, not the rootfs).

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
allow = ["pypi.org", "*.pythonhosted.org"]
EOF

cat > pyproject.toml <<'EOF'
[build-system]
requires = ["setuptools>=61"]
build-backend = "setuptools.build_meta"

[project]
name = "mypkg"
version = "0.1.0"
description = "silo editable-install regression fixture"
requires-python = ">=3.9"

[tool.setuptools.packages.find]
where = ["src"]
EOF

mkdir -p src/mypkg
cat > src/mypkg/__init__.py <<'EOF'
__name__ = "mypkg"
GREETING = "hello from silo editable install"
EOF

echo "Testing: silo run python -m venv .venv"
"$SILO_BIN" run python -m venv .venv >/dev/null 2>&1
if [ ! -x .venv/bin/pip ]; then
  echo "FAIL: .venv/bin/pip not created"
  exit 1
fi

echo "Testing: silo run python -m pip install -e . (editable install)"
install_out=$("$SILO_BIN" run python -m pip install --quiet --disable-pip-version-check -e . 2>&1)
if ! "$SILO_BIN" run python -c "import mypkg; print(mypkg.GREETING)" 2>&1 | grep -q "hello from silo editable install"; then
  echo "FAIL: editable install did not make mypkg importable"
  echo "--- pip output ---"
  echo "$install_out" | tail -20
  exit 1
fi
echo "PASS: mypkg importable after editable install"

echo "Testing: editable install survives a fresh silo run (host venv persistence)"
# Mutate the source on the host; an editable install should pick up the change
# without reinstall.
cat > src/mypkg/__init__.py <<'EOF'
__name__ = "mypkg"
GREETING = "hello after edit"
EOF
out=$("$SILO_BIN" run python -c "import mypkg; print(mypkg.GREETING)" 2>&1 | tail -1)
if [ "$out" != "hello after edit" ]; then
  echo "FAIL: edited source not reflected in second silo run; got: $out"
  exit 1
fi
echo "PASS: editable install reflects host source edits across silo runs"
