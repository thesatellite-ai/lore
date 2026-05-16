#!/usr/bin/env bash
# SC-33: bench grader audit on synthetic data
# Catches: bench engine P2.8 (grader audit flags suspect graders)
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project bench33

# Just verify grader audit runs without an LLM and exits clean
# when there are no results yet.
$LORE bench eval add --category=custom --prompt="x" \
    --grader-kind=programmatic --grader-cmd='exit 0' >/dev/null
out=$($LORE bench grader audit --json 2>&1)
echo "$out" | jq -e '.data == null or (.data | type == "array")' >/dev/null \
    || fail "audit JSON shape wrong: $out"
pass SC-33
