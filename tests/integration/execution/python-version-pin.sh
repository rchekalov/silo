#!/bin/bash
set -euo pipefail

# Project-level `[overrides.python] image = "..."` should change the actual
# running interpreter, not just registry display. Validates two pinned
# minor versions back-to-back from the same workdir, swapping silo.toml
# in between.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"

write_silo_toml() {
  local image="$1"
  cat > silo.toml <<EOF
tools = ["python"]

[overrides.python]
image = "${image}"
EOF
}

assert_python_minor() {
  local expect="$1"
  local got
  got=$("$SILO_BIN" run python -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')" 2>&1 | tail -1)
  if [ "$got" != "$expect" ]; then
    echo "FAIL: expected python $expect, got: $got"
    return 1
  fi
}

echo "Testing: pinning python:3.11-slim"
write_silo_toml "docker.io/library/python:3.11-slim"
if ! assert_python_minor "3.11"; then exit 1; fi
echo "PASS: 3.11-slim pin honored"

echo "Testing: switching to python:3.12-slim in same workdir"
write_silo_toml "docker.io/library/python:3.12-slim"
if ! assert_python_minor "3.12"; then exit 1; fi
echo "PASS: 3.12-slim pin honored after rewrite"
