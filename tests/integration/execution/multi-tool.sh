#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

INSTALLED_TOOLS=$("$SILO_BIN" list 2>/dev/null | awk 'NR>1 {print $1}' || echo "")

TESTED=0

for tool in python node go; do
  if ! echo "$INSTALLED_TOOLS" | grep -qw "$tool"; then
    echo "SKIP: $tool not installed"
    continue
  fi

  echo "Testing: $tool --version"
  "$SILO_BIN" run "$tool" -- --version >/dev/null 2>&1
  echo "PASS: $tool runs successfully"
  ((TESTED++))
done

if [ "$TESTED" -eq 0 ]; then
  echo "FAIL: no tools installed to test"
  exit 1
fi

echo "PASS: $TESTED tools verified"
