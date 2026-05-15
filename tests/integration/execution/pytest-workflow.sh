#!/bin/bash
set -euo pipefail

# pytest is the de-facto test runner for Python. Validates that `silo build`
# can bake it into a project rootfs and that `silo run python -m pytest`
# exit codes propagate correctly (0 on pass, non-zero on fail) — without
# correct exit propagation, CI integrations against silo are broken.

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

mkdir -p tests
cat > tests/test_smoke.py <<'EOF'
def test_pass():
    assert 1 + 1 == 2

def test_fail():
    assert 1 + 1 == 3, "intentional failure for the silo pytest regression test"
EOF

echo "Testing: silo build python pip install pytest (bake pytest into rootfs)"
build_out=$("$SILO_BIN" build python pip install pytest 2>&1)
if ! echo "$build_out" | grep -qE "Successfully installed.*pytest-"; then
  echo "FAIL: build did not report pytest installation"
  echo "$build_out" | tail -20
  exit 1
fi
echo "PASS: pytest baked into project rootfs"

echo "Testing: pytest -m pass+fail exits non-zero with failure visible"
set +e
fail_out=$("$SILO_BIN" run python -m pytest tests/ 2>&1)
fail_exit=$?
set -e
if [ "$fail_exit" -eq 0 ]; then
  echo "FAIL: pytest exited 0 with a failing test present"
  echo "$fail_out"
  exit 1
fi
if ! echo "$fail_out" | grep -qE "1 failed.*1 passed"; then
  echo "FAIL: expected '1 failed, 1 passed' in pytest output"
  echo "$fail_out" | tail -20
  exit 1
fi
echo "PASS: pytest reported 1 failed + 1 passed and exited $fail_exit"

echo "Testing: pytest scoped to passing test exits 0"
"$SILO_BIN" run python -m pytest "tests/test_smoke.py::test_pass" >/dev/null 2>&1
echo "PASS: scoped pytest exited 0"
