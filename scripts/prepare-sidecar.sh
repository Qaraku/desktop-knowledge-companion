#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
case "${RUNNER_OS:-Linux}" in
  Windows) target="x86_64-pc-windows-msvc"; extension=".exe" ;;
  Linux) target="x86_64-unknown-linux-gnu"; extension="" ;;
  *) echo "unsupported release platform: ${RUNNER_OS:-unknown}" >&2; exit 1 ;;
esac

output_dir="$root_dir/src-tauri/binaries"
mkdir -p "$output_dir"
cd "$root_dir"
go build -o "$output_dir/knowledge-core-$target$extension" ./cmd/knowledge-core
