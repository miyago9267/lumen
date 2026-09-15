#!/usr/bin/env bash
set -euo pipefail

target="${LUMEN_BIN_DIR:-${HOME}/.local/bin}/lumen"

if [ ! -e "$target" ] && [ ! -L "$target" ]; then
  printf '%s\n' "lumen is not installed at $target"
  exit 0
fi

if ! "$target" --version 2>/dev/null | grep -q '^lumen '; then
  printf '%s\n' "error: refusing to remove non-lumen path: $target" >&2
  exit 1
fi

rm -f "$target"
printf '%s\n' "removed lumen from $target"
