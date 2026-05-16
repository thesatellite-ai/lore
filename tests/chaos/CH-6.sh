#!/usr/bin/env bash
# CH-6: DB truncated to 0 bytes
# Catches: R23-9
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch6
$LORE memory add "x" >/dev/null
$LORE backup >/dev/null

: > .lore/lore.db

$LORE doctor 2>&1 | grep -qE "corrupt|empty|E_DB_CORRUPT" || fail "doctor missed 0-byte DB"
$LORE repair --tier=2 --confirm >/dev/null || fail "repair tier-2"
$LORE memory search "x" 2>&1 | grep -q "x" || fail "data not restored"
pass CH-6
