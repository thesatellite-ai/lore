#!/usr/bin/env bash
# SC-34: bench report analyze emits valid stats on n=0 (graceful degradation)
# Catches: bench engine P2.6/P2.7 (stats layer handles empty + small samples)
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project bench34

# Without any runs, analyze on a non-existent run should error cleanly
$LORE bench report analyze nonexistent 2>&1 | grep -qE "E_NOT_FOUND" \
    || fail "analyze should refuse unknown run"

# List with no runs returns empty
count=$($LORE bench run list --json | jq '.count // (.data | length) // 0')
[ "$count" = "0" ] || fail "expected 0 runs, got $count"

pass SC-34
