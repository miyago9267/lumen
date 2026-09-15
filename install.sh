#!/usr/bin/env bash
set -euo pipefail

case "$(uname -s)" in
  Darwin|Linux) ;;
  *)
    printf '%s\n' "error: lumen supports macOS and Linux" >&2
    exit 1
    ;;
esac

source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bin_dir="${LUMEN_BIN_DIR:-${HOME}/.local/bin}"
target="${bin_dir}/lumen"
tmp_target="${bin_dir}/.lumen.tmp.$$"

if ! command -v go >/dev/null 2>&1; then
  printf '%s\n' 'error: Go is required to build lumen' >&2
  exit 1
fi

if ! command -v codex >/dev/null 2>&1 && [ -z "${LUMEN_CODEX_BIN:-}" ]; then
  printf '%s\n' 'error: official codex is not in PATH; set LUMEN_CODEX_BIN if needed' >&2
  exit 1
fi

if [ -e "$target" ] || [ -L "$target" ]; then
  if ! "$target" --version 2>/dev/null | grep -q '^lumen '; then
    printf '%s\n' "error: refusing to overwrite non-lumen path: $target" >&2
    exit 1
  fi
fi

cleanup() {
  rm -f "$tmp_target"
}
trap cleanup EXIT

mkdir -p "$bin_dir"
(cd "$source_dir" && go test ./... && go build -trimpath -ldflags='-s -w' -o "$tmp_target" .)
chmod 755 "$tmp_target"
mv -f "$tmp_target" "$target"
trap - EXIT

printf '%s\n' "installed lumen -> $target"
"$target" --version
