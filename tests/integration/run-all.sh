#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SILO_BIN="${SILO_BIN:-silo}"

PASS=0
FAIL=0
SKIP=0
FAILURES=()

# Check which tools are installed
INSTALLED_TOOLS=$("$SILO_BIN" list 2>/dev/null | awk 'NR>1 {print $1}' || echo "")

tool_installed() {
  echo "$INSTALLED_TOOLS" | grep -qw "$1"
}

run_test() {
  local name="$1"
  local script="$2"
  shift 2
  local required_tools=("$@")

  # Check required tools
  for tool in "${required_tools[@]}"; do
    if [ -n "$tool" ] && ! tool_installed "$tool"; then
      printf "  %-40s SKIP (requires %s)\n" "$name" "$tool"
      ((SKIP++))
      return 0
    fi
  done

  printf "  %-40s " "$name"
  local output exit_code
  set +e
  output=$(SILO_BIN="$SILO_BIN" bash "$script" 2>&1)
  exit_code=$?
  set -e
  if [ "$exit_code" -eq 0 ]; then
    echo "PASS"
    ((PASS++))
  else
    echo "FAIL (exit $exit_code)"
    ((FAIL++))
    FAILURES+=("$name")
    # Show last 10 lines of output on failure
    echo "$output" | tail -10 | sed 's/^/    /'
  fi
  # Brief pause between tests to let VM resources release
  sleep 1
}

echo "=== Silo Integration Tests (VM) ==="
echo ""
echo "Binary: $SILO_BIN"
echo "Installed tools: ${INSTALLED_TOOLS:-none}"
echo ""

echo "--- Isolation ---"
run_test "file-isolation" "$SCRIPT_DIR/isolation/file-isolation.sh" "python"
run_test "env-isolation" "$SCRIPT_DIR/isolation/env-isolation.sh" "python"
run_test "ssh-agent-forwarding" "$SCRIPT_DIR/isolation/ssh-agent-forwarding.sh" "python"

echo ""
echo "--- Execution ---"
run_test "exit-codes" "$SCRIPT_DIR/execution/exit-codes.sh" "python"
run_test "multi-tool" "$SCRIPT_DIR/execution/multi-tool.sh" ""
run_test "arg-passthrough" "$SCRIPT_DIR/execution/arg-passthrough.sh" "python"
run_test "shim-flow" "$SCRIPT_DIR/execution/shim-flow.sh" "python"
run_test "pin-fallthrough" "$SCRIPT_DIR/execution/pin-fallthrough.sh" "python"
run_test "legacy-dashdash" "$SCRIPT_DIR/execution/legacy-dashdash.sh" "python"
run_test "add-kotlin" "$SCRIPT_DIR/execution/add-kotlin.sh" "claude-code"
run_test "build-persistence" "$SCRIPT_DIR/execution/build-persistence.sh" "python"
run_test "venv-host-mount" "$SCRIPT_DIR/execution/venv-host-mount.sh" "python"
run_test "demo-node-quickstart" "$SCRIPT_DIR/execution/demo-node-quickstart.sh" "node"
run_test "demo-python-quickstart" "$SCRIPT_DIR/execution/demo-python-quickstart.sh" "python"

echo ""
echo "--- Cache ---"
run_test "rootfs-cache" "$SCRIPT_DIR/cache/rootfs-cache.sh" "python"
run_test "package-cache" "$SCRIPT_DIR/cache/package-cache.sh" "python"

echo ""
echo "--- Network ---"
run_test "network-isolation" "$SCRIPT_DIR/network/network-isolation.sh" "python"
run_test "host-access" "$SCRIPT_DIR/network/host-access.sh" "python"
run_test "proxy-allowlist" "$SCRIPT_DIR/network/proxy-allowlist.sh" "python"
run_test "network-default-deny" "$SCRIPT_DIR/network/network-default-deny.sh" "python"

echo ""
echo "--- LSP ---"
# lsp-install-bakes runs first: it force-reinstalls python so any stale
# pre-existing bake (e.g. from an older registry without pyright[nodejs])
# is overwritten before the lifecycle/path-rewriting tests consume it.
run_test "lsp-install-bakes" "$SCRIPT_DIR/lsp/lsp-install-bakes.sh" ""
run_test "lsp-lifecycle" "$SCRIPT_DIR/lsp/lsp-lifecycle.sh" ""
run_test "lsp-path-rewriting" "$SCRIPT_DIR/lsp/path-rewriting.sh" ""
run_test "lsp-ide-config" "$SCRIPT_DIR/lsp/ide-config.sh" ""
run_test "lsp-version-pin" "$SCRIPT_DIR/lsp/lsp-version-pin.sh" ""

echo ""
echo "--- Errors ---"
run_test "not-installed" "$SCRIPT_DIR/errors/not-installed.sh" ""

echo ""
echo "=== Results: $PASS passed, $FAIL failed, $SKIP skipped ==="

if [ ${#FAILURES[@]} -gt 0 ]; then
  echo ""
  echo "Failed tests:"
  for f in "${FAILURES[@]}"; do
    echo "  - $f"
  done
  exit 1
fi
