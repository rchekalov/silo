#!/bin/bash
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"

echo "Testing: sensitive host directories are not accessible in container"

WORKDIR=$(mktemp -d)
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

(cd "$WORKDIR" && "$SILO_BIN" run python -c "
import os, sys

sensitive = [
    os.path.expanduser('~') + '/.ssh',
    os.path.expanduser('~') + '/.aws',
    os.path.expanduser('~') + '/.gnupg',
    os.path.expanduser('~') + '/.config/gcloud',
    os.path.expanduser('~') + '/.azure',
    os.path.expanduser('~') + '/.kube',
]

leaked = [p for p in sensitive if os.path.exists(p)]
with open('/workspace/.output', 'w') as f:
    if leaked:
        f.write('FAIL: accessible: ' + ', '.join(leaked))
        sys.exit(1)
    f.write('PASS')
") >/dev/null 2>&1

OUTPUT=$(cat "$WORKDIR/.output" 2>/dev/null || echo "")
if [[ "$OUTPUT" != "PASS" ]]; then
  echo "FAIL: $OUTPUT"
  exit 1
fi

echo "PASS: sensitive directories not accessible in container"
