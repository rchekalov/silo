#!/bin/bash
set -euo pipefail

# Mirrors the silo-web "Node quickstart" demo end-to-end:
#   silo init --no-interactive   → writes silo.toml
#   silo build node npm install  → bakes node_modules (incl. chalk) into rootfs
#   silo npm start               → runs the app inside the saved rootfs
#
# Acts as a high-level regression guard for the things that broke on 0.5.2:
# build persistence (Bug A), shim dispatch, project mount layout. If `chalk`
# can be require()'d after the build, the saved rootfs is intact.

SILO_BIN="${SILO_BIN:-silo}"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

cd "$WORKDIR"
cat > package.json <<'EOF'
{
  "name": "silo-demo-node",
  "version": "1.0.0",
  "private": true,
  "scripts": { "start": "node app.js" },
  "dependencies": { "chalk": "4.1.2" }
}
EOF
cat > app.js <<'EOF'
const chalk = require('chalk');
console.log(chalk.green('OK ') + 'node ' + process.version);
console.log('  platform:', process.platform, process.arch);
console.log('  cwd:     ', process.cwd());
EOF

echo "Testing: silo init --no-interactive runs without prompts"
"$SILO_BIN" init --no-interactive </dev/null >/dev/null 2>&1
if [ ! -f silo.toml ]; then
  echo "FAIL: silo init did not create silo.toml"
  exit 1
fi
# silo init currently writes TOML literal strings ('node') rather than basic
# strings ("node"); both are spec-valid, accept either.
if ! grep -Eq "^tools = \\[(\"node\"|'node')\\]" silo.toml; then
  echo "FAIL: silo.toml missing tools = [\"node\"]"
  cat silo.toml
  exit 1
fi
if ! grep -Eq "(\"node_modules\"|'node_modules')" silo.toml; then
  echo "FAIL: silo.toml missing node_modules in mount.exclude"
  cat silo.toml
  exit 1
fi
echo "PASS: silo init wrote silo.toml with expected tools + exclude"

echo "Testing: silo build node npm install"
build_out=$("$SILO_BIN" build node npm install 2>&1)
if ! echo "$build_out" | grep -q "Setup complete. Rootfs saved to.*\.silo/node/rootfs.ext4"; then
  echo "FAIL: build did not report Setup complete with expected rootfs path"
  echo "$build_out" | tail -20
  exit 1
fi
if [ ! -s .silo/node/rootfs.ext4 ]; then
  echo "FAIL: .silo/node/rootfs.ext4 missing or empty"
  exit 1
fi
size=$(stat -f%z .silo/node/rootfs.ext4 2>/dev/null || stat -c%s .silo/node/rootfs.ext4)
if [ "$size" -lt 1048576 ]; then
  echo "FAIL: rootfs is $size bytes, expected > 1 MiB"
  exit 1
fi
echo "PASS: build wrote a non-empty rootfs ($size bytes)"

echo "Testing: silo npm start runs inside the saved rootfs and chalk loads"
run_out=$("$SILO_BIN" npm start 2>&1)
if ! echo "$run_out" | grep -q "platform: linux arm64"; then
  echo "FAIL: expected 'platform: linux arm64' (proves we ran inside the VM)"
  echo "$run_out"
  exit 1
fi
if ! echo "$run_out" | grep -q "cwd:.*/workspace"; then
  echo "FAIL: expected 'cwd: /workspace' (proves project mount worked)"
  echo "$run_out"
  exit 1
fi
if ! echo "$run_out" | grep -q "OK node v"; then
  echo "FAIL: chalk import failed — node could not load chalk from the saved rootfs"
  echo "$run_out"
  exit 1
fi
echo "PASS: chalk loaded from saved rootfs, ran inside Linux VM at /workspace"
