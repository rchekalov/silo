#!/bin/bash
set -euo pipefail

# Verifies that `silo add kotlin` in a Kotlin-marked project:
#   1. creates/updates .siloconf with overrides.claude-code.postInstall
#   2. bakes a project-scoped rootfs at .silo/claude-code/rootfs.ext4
#   3. the baked rootfs actually contains the kotlin binary
#
# Skipped by run-all.sh unless claude-code is globally installed.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > build.gradle.kts <<'EOF'
plugins {
    kotlin("jvm") version "1.9.0"
}
EOF

echo "Testing: silo add kotlin records postInstall in .siloconf"
"$SILO_BIN" add kotlin --no-sync >/dev/null 2>&1

if [ ! -f .siloconf ]; then
  echo "FAIL: .siloconf was not created"
  exit 1
fi
if ! grep -q "postInstall" .siloconf; then
  echo "FAIL: .siloconf missing postInstall entry"
  cat .siloconf
  exit 1
fi
echo "PASS: .siloconf updated"

echo "Testing: silo sync produces project rootfs"
"$SILO_BIN" sync >/dev/null 2>&1

if [ ! -f .silo/claude-code/rootfs.ext4 ]; then
  echo "FAIL: project rootfs not produced at .silo/claude-code/rootfs.ext4"
  exit 1
fi
echo "PASS: project rootfs present"

echo "Testing: baked rootfs has kotlin binary"
if ! "$SILO_BIN" run --shim sh claude-code -c 'command -v kotlin >/dev/null && kotlin -version 2>&1 | head -1' | grep -qi kotlin; then
  echo "FAIL: kotlin not reachable inside claude-code"
  exit 1
fi
echo "PASS: kotlin in baked rootfs"

echo "Testing: second silo sync is idempotent"
out=$("$SILO_BIN" sync 2>&1)
if ! echo "$out" | grep -q "up-to-date"; then
  echo "FAIL: expected 'up-to-date' on second sync, got:"
  echo "$out"
  exit 1
fi
echo "PASS: idempotent bake"
