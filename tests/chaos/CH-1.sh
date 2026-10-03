#!/usr/bin/env bash
# CH-1: Corrupt DB → repair tier-2 succeeds
# Catches: R16-7, R23-44
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project ch1

# Populate ~200 rows so most pages are used; random corruption in a freshly-
# init DB tends to hit free pages that integrity_check ignores.
for i in $(seq 1 200); do
    $LORE memory add --body "row-$i filler $(date +%s%N 2>/dev/null || echo $i)" >/dev/null
done
$LORE backup >/dev/null || fail "backup failed"

# Force WAL checkpoint so corruption to main file actually affects reads.
sqlite3 .lore/lore.db "PRAGMA wal_checkpoint(TRUNCATE)" >/dev/null 2>&1 || true

# Overwrite the whole middle half of the file (deterministic: three 4 KiB
# blots used to miss live pages on some runs and the scenario skipped).
SIZE=$(stat -f%z .lore/lore.db 2>/dev/null || stat -c%s .lore/lore.db)
dd if=/dev/urandom of=.lore/lore.db bs=4096 count=$((SIZE/2/4096)) seek=$((SIZE/4/4096)) conv=notrunc 2>/dev/null

$LORE doctor 2>&1 | grep -qE "corrupt|E_DB_CORRUPT|broken|integrity|page" || fail "doctor did not detect a corrupted DB"

$LORE repair --tier=2 --confirm >/dev/null || fail "repair failed"
$LORE memory search "row-1" 2>&1 | grep -q "row-1" || fail "data not recovered"
pass CH-1
