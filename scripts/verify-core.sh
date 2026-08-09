#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
data_dir=$(mktemp -d)
binary="$data_dir/knowledge-core"
cleanup() { rm -r -- "$data_dir"; }
trap cleanup EXIT INT TERM

cd "$root_dir"
go build -o "$binary" ./cmd/knowledge-core

"$binary" health --data-dir "$data_dir/state" --json | grep -q '"ready":true'
first_import=$("$binary" import --data-dir "$data_dir/state" --kind text --content 'Go agent evidence' --idempotency-key verify-import --json)
second_import=$("$binary" import --data-dir "$data_dir/state" --kind text --content 'Go agent evidence' --idempotency-key verify-import --json)
printf '%s' "$first_import" | grep -q '"replayed":false'
printf '%s' "$second_import" | grep -q '"replayed":true'
"$binary" query --data-dir "$data_dir/state" --question 'unmatched query' --mode strict --json | grep -q '"refusal_reason":"no_local_evidence"'
