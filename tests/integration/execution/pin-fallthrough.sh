#!/bin/bash
# Pyenv-style shim fall-through:
#   - Branch [A]: Inside a project that claims a tool via .siloconf, the shim
#     dispatches into silo regardless of the global pinnedGlobally flag.
#   - Branch [B]: Outside any project, a globally pinned tool dispatches into silo.
#   - Branch [C]: Outside any project, an unpinned tool falls through to the
#     next instance on PATH (e.g. host Python).
#
# Requires `python` to be installed in silo and at least one host python on
# PATH that is NOT the silo shim itself (homebrew, pyenv, /usr/bin, etc.).
set -euo pipefail

SILO_BIN="${SILO_BIN:-silo}"
SHIM_DIR="$HOME/.silo/bin"

WORKDIR=$(mktemp -d)
PROJECT_DIR="$WORKDIR/proj"
NONPROJECT_DIR="$WORKDIR/elsewhere"
mkdir -p "$PROJECT_DIR" "$NONPROJECT_DIR"
echo 'tools: [python]' > "$PROJECT_DIR/.siloconf"

# Capture original pinned state so we can restore it whatever happens.
ORIG_PIN=$("$SILO_BIN" current python 2>&1 | head -1 | grep -o 'globally pinned\|claimed\|fall-through' || echo "unknown")

cleanup() {
  rm -rf "$WORKDIR"
  case "$ORIG_PIN" in
    "globally pinned") "$SILO_BIN" pin python >/dev/null 2>&1 || true ;;
    "fall-through")    "$SILO_BIN" unpin python >/dev/null 2>&1 || true ;;
  esac
}
trap cleanup EXIT

if [ ! -x "$SHIM_DIR/python" ]; then
  echo "FAIL: $SHIM_DIR/python missing — run `silo install python` first"
  exit 1
fi

run_python() {
  # Run python via the silo shim from $1 cwd. Print sys.platform so we can
  # tell linux (silo VM) from darwin (host fall-through).
  (cd "$1" && PATH="$SHIM_DIR:$PATH" python -c "import sys; print(sys.platform)" 2>&1)
}

echo "Test 1/3: branch [A] — project claim wins over unpinned global"
"$SILO_BIN" unpin python >/dev/null
out=$(run_python "$PROJECT_DIR")
if [ "$out" != "linux" ]; then
  echo "FAIL: expected silo VM dispatch (linux) inside claiming project, got: $out"
  exit 1
fi
echo "PASS: silo VM dispatched ($out)"

echo "Test 2/3: branch [C] — unpinned, no project claim → fall-through"
out=$(run_python "$NONPROJECT_DIR")
if [ "$out" = "linux" ]; then
  echo "FAIL: expected host fall-through (darwin/etc.) outside project, got: $out"
  exit 1
fi
echo "PASS: fell through to host ($out)"

echo "Test 3/3: branch [B] — globally pinned dispatches everywhere"
"$SILO_BIN" pin python >/dev/null
out=$(run_python "$NONPROJECT_DIR")
if [ "$out" != "linux" ]; then
  echo "FAIL: expected silo VM dispatch when globally pinned, got: $out"
  exit 1
fi
echo "PASS: silo VM dispatched even outside project ($out)"
