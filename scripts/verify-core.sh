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
candidate_id=$(printf '%s' "$first_import" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
updated=$(
  "$binary" candidate-update --data-dir "$data_dir/state" --candidate-id "$candidate_id" --expected-version 1 --content 'Go agent evidence revised' --json
)
printf '%s' "$updated" | grep -q '"state":"editing"'
"$binary" candidate-pending --data-dir "$data_dir/state" --json | grep -q 'Go agent evidence revised'
approval=$("$binary" candidate-approval --data-dir "$data_dir/state" --candidate-id "$candidate_id" --json)
approval_id=$(printf '%s' "$approval" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
resolved=$("$binary" approval-resolve --data-dir "$data_dir/state" --approval-id "$approval_id" --approve --json)
token=$(printf '%s' "$resolved" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
promoted=$("$binary" candidate-promote --data-dir "$data_dir/state" --candidate-id "$candidate_id" --token "$token" --json)
printf '%s' "$promoted" | grep -q '"knowledge"'
knowledge_id=$(printf '%s' "$promoted" | sed -n 's/.*"knowledge":{"id":"\([^"]*\)".*/\1/p')
revision_id=$(printf '%s' "$promoted" | sed -n 's/.*"revision":{"id":"\([^"]*\)".*/\1/p')
"$binary" knowledge-revise --data-dir "$data_dir/state" --knowledge-id "$knowledge_id" --expected-revision-id "$revision_id" --content 'Go agent evidence corrected' --reason fact_update --json | grep -q '"parent_revision_id"'
"$binary" knowledge --data-dir "$data_dir/state" --json | grep -q 'Go agent evidence corrected'
rejected_import=$("$binary" import --data-dir "$data_dir/state" --kind text --content 'Rejected candidate' --idempotency-key verify-reject --json)
rejected_id=$(printf '%s' "$rejected_import" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
"$binary" candidate-reject --data-dir "$data_dir/state" --candidate-id "$rejected_id" --expected-version 1 --json | grep -q '"state":"rejected"'
query_result=$("$binary" query --data-dir "$data_dir/state" --question 'unmatched query' --mode strict --json)
printf '%s' "$query_result" | grep -q '"refusal_reason":"no_local_evidence"'
run_id=$(printf '%s' "$query_result" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
"$binary" run --data-dir "$data_dir/state" --run-id "$run_id" --json | grep -q '"trace"'
