#!/usr/bin/env bash
# SC-16: Backup → Restore round-trip
# Catches: R16-13+#17, R23-13
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project backupt
$LORE memory add "round-trip" >/dev/null

$LORE backup --out="$TMP/b.sqlite" >/dev/null || fail "backup"
[ -f "$TMP/b.sqlite" ] || fail "backup file missing"

rm -f .lore/lore.db .lore/lore.db-wal .lore/lore.db-shm

$LORE restore "$TMP/b.sqlite" --confirm >/dev/null || fail "restore"
$LORE memory search "round-trip" 2>&1 | grep -q "round-trip" || fail "memory not recovered"
pass SC-16
