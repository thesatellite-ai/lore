#!/usr/bin/env bash
# SC-6: Read-only mode refuses writes
# Catches: R14-5, R18-23
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ro
$LORE memory add "seed" >/dev/null || fail "seed"

# Read still works.
$LORE memory search "seed" --read-only >/dev/null || fail "search in ro mode"

# Write must fail.
if $LORE memory add "blocked" --read-only 2>&1 | grep -qE "E_READ_ONLY|read-only"; then
    pass SC-6
fi
fail "write in --read-only did not refuse"
