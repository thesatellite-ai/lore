#!/usr/bin/env bash
# SC-3: Disaster recovery — damaged local database → data comes back
# Catches: R16-7+#8, R23-13, R23-44
#
# 0.1.18: the only local database of a repo is the search cache
# .lore/lore-cache.db; the rows are files in .lore/data. `lore backup` and
# `lore repair` were removed (CHANGELOG 0.1.18 "Removed"): a damaged cache
# is moved to .lore/backups and rebuilt from the row files by the next
# command, and `lore search rebuild` rebuilds it on demand. Nothing is lost.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home; init_project drtest
$LORE memory add --body "important data" >/dev/null || fail "add"

# 1. Garbage over the cache: the next read rebuilds it and says so
head -c 8192 /dev/urandom > .lore/lore-cache.db
OUT=$($LORE memory search "important" 2>&1) || fail "search after damage: $OUT"
echo "$OUT" | grep -q "important data" || fail "data not recovered: $OUT"
echo "$OUT" | grep -q "was damaged" || fail "no notice that the cache was damaged: $OUT"
ls .lore/backups/lore-cache.db.corrupt-* >/dev/null 2>&1 || fail "damaged cache not kept in .lore/backups"
git check-ignore -q .lore/backups/x || fail ".lore/backups is not gitignored"

# 2. Truncated cache, then an explicit rebuild
: > .lore/lore-cache.db
$LORE search rebuild >/dev/null || fail "search rebuild"
$LORE memory search "important" 2>&1 | grep -q "important data" || fail "data not recovered after rebuild"

# 3. Cache deleted outright: recreated from the row files
rm -f .lore/lore-cache.db .lore/lore-cache.db-wal .lore/lore-cache.db-shm
$LORE memory search "important" 2>&1 | grep -q "important data" || fail "data not recovered after cache removal"
[ -f .lore/lore-cache.db ] || fail "cache not recreated"
pass SC-3
