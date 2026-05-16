#!/usr/bin/env bash
# SC-31: bench eval add/list/show round-trip
# Catches: bench engine P2.2 (eval CLI surface)
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project bench31

$LORE rule add --severity=must "Test rule" >/dev/null || fail "rule add"

$LORE bench eval add \
    --category=rule-trigger --link=rule:R-1 \
    --prompt="Test prompt" \
    --grader-kind=programmatic \
    --grader-cmd='exit 0' >/dev/null || fail "eval add"

# list should show 1
count=$($LORE bench eval list --json | jq '.count // (.data | length)')
[ "$count" = "1" ] || fail "expected 1 eval, got $count"

# show by code works
$LORE bench eval show E1-001 --json | jq -e '.data.category == "rule-trigger"' >/dev/null \
    || fail "show by code or category mismatch"

# edit changes the grader_cmd
$LORE bench eval edit E1-001 --grader-cmd='exit 1' >/dev/null || fail "eval edit"
spec=$($LORE bench eval show E1-001 --json | jq -r '.data.grader_spec.cmd')
[ "$spec" = "exit 1" ] || fail "edit didn't persist; got '$spec'"

# archive → not in default list, in --include-archived list
$LORE bench eval archive E1-001 >/dev/null
default=$($LORE bench eval list --json | jq '.count // (.data | length)')
withArchived=$($LORE bench eval list --include-archived --json | jq '.count // (.data | length)')
[ "$default" = "0" ] && [ "$withArchived" = "1" ] || fail "archive flags not respected"

pass SC-31
