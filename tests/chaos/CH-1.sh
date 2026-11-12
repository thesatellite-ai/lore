#!/usr/bin/env bash
# CH-1: Corrupt cache → lore notices, sets it aside and rebuilds it from the row files
# Catches: R16-7, R23-44 (0.1.18: .lore/data/<table>/<id>.json is the truth;
# the repo cache under ~/.lore/cache/repo is rebuilt from it, so a damaged cache loses nothing)
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project ch1

# ~200 rows so most cache pages are in use (a blot in a fresh file tends to
# hit free pages SQLite never reads). Added 8 at a time: concurrent adds are
# safe (SC-5 proves it), and one at a time this seed was most of the gate's
# chaos step. Every add must succeed; the count is checked again below.
seq 1 200 | xargs -P 8 -I{} "$LORE" memory add --body "row-{} filler" >/dev/null || fail "seed: an add failed"
[ "$(find .lore/data/memories -name 'mem_*.json' | wc -l | tr -d ' ')" -eq 200 ] || fail "seed: not 200 row files"

CACHE=$(repo_cache)
[ -f "$CACHE" ] || fail "no cache at $CACHE"

# Overwrite the whole middle half of the cache file.
SIZE=$(stat -f%z "$CACHE" 2>/dev/null || stat -c%s "$CACHE")
dd if=/dev/urandom of="$CACHE" bs=4096 count=$((SIZE/2/4096)) seek=$((SIZE/4/4096)) conv=notrunc 2>/dev/null

# The next command says what it did; nothing is lost and lore keeps working.
OUT=$($LORE doctor 2>&1); rc=$?
echo "$OUT" | grep -q "was damaged; moved it to" || fail "the damaged cache was not reported: $OUT"
echo "$OUT" | grep -q "rebuilt it from the row files (nothing lost)" || fail "no rebuild reported: $OUT"
[ "$rc" -eq 0 ] || fail "doctor not healthy after the rebuild (exit $rc): $OUT"
ls "$CACHE".corrupt-* >/dev/null 2>&1 || fail "the damaged file was not kept aside beside it ($CACHE.corrupt-*)"
[ "$(lore_count memories)" -eq 200 ] || fail "rows lost: $(lore_count memories)/200"
$LORE memory search '"row-1"' 2>&1 | grep -q "row-1 filler" || fail "search does not find a row after the rebuild"
$LORE memory add --body "after the rebuild" >/dev/null || fail "write after the rebuild"
pass CH-1
