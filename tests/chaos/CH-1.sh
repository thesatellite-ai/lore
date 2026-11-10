#!/usr/bin/env bash
# CH-1: Corrupt cache → lore notices, sets it aside and rebuilds it from the row files
# Catches: R16-7, R23-44 (0.1.18: .lore/data/<table>/<id>.json is the truth;
# .lore/lore-cache.db is a cache rebuilt from it, so a damaged cache loses nothing)
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch1

# ~200 rows so most cache pages are in use (a blot in a fresh file tends to
# hit free pages SQLite never reads).
for i in $(seq 1 200); do
    $LORE memory add --body "row-$i filler" >/dev/null || fail "seed $i"
done
CACHE=.lore/lore-cache.db
[ -f "$CACHE" ] || fail "no cache at $CACHE"

# Overwrite the whole middle half of the cache file.
SIZE=$(stat -f%z "$CACHE" 2>/dev/null || stat -c%s "$CACHE")
dd if=/dev/urandom of="$CACHE" bs=4096 count=$((SIZE/2/4096)) seek=$((SIZE/4/4096)) conv=notrunc 2>/dev/null

# The next command says what it did; nothing is lost and lore keeps working.
OUT=$($LORE doctor 2>&1); rc=$?
echo "$OUT" | grep -q "was damaged; moved it to" || fail "the damaged cache was not reported: $OUT"
echo "$OUT" | grep -q "rebuilt it from the row files (nothing lost)" || fail "no rebuild reported: $OUT"
[ "$rc" -eq 0 ] || fail "doctor not healthy after the rebuild (exit $rc): $OUT"
ls .lore/backups/lore-cache.db.corrupt-* >/dev/null 2>&1 || fail "the damaged file was not kept aside in .lore/backups"
[ "$(lore_count memories)" -eq 200 ] || fail "rows lost: $(lore_count memories)/200"
$LORE memory search '"row-1"' 2>&1 | grep -q "row-1 filler" || fail "search does not find a row after the rebuild"
$LORE memory add --body "after the rebuild" >/dev/null || fail "write after the rebuild"
pass CH-1
