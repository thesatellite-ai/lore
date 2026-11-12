#!/usr/bin/env bash
# SC-3: Disaster recovery — damaged local database → data comes back
# Catches: R16-7+#8, R23-13, R23-44
#
# 0.1.18: the only local database of a repo is the search cache
# ~/.lore/cache/repo/<folder>-<hash>.db; the rows are files in .lore/data. `lore backup` and
# `lore repair` were removed (CHANGELOG 0.1.18 "Removed"): a damaged cache
# is moved aside beside it (<cache>.corrupt-<time>) and rebuilt from the row files by the next
# command, and `lore search rebuild` rebuilds it on demand. Nothing is lost.
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; isolate_home; init_project drtest
CACHE=$(repo_cache)
$LORE memory add --body "important data" >/dev/null || fail "add"

# 1. Garbage over the cache: the next read rebuilds it and says so
head -c 8192 /dev/urandom > "$CACHE"
OUT=$($LORE memory search "important" 2>&1) || fail "search after damage: $OUT"
echo "$OUT" | grep -q "important data" || fail "data not recovered: $OUT"
echo "$OUT" | grep -q "was damaged" || fail "no notice that the cache was damaged: $OUT"
ls "$CACHE".corrupt-* >/dev/null 2>&1 || fail "damaged cache not kept beside it ($CACHE.corrupt-*)"
LEFT=$(git status --porcelain --untracked-files=all .lore | grep -v ' \.lore/data/' || true)
[ -z "$LEFT" ] || fail "the damaged cache left a file in the repo outside .lore/data: $LEFT"

# 2. Truncated cache, then an explicit rebuild
: > "$CACHE"
$LORE search rebuild >/dev/null || fail "search rebuild"
$LORE memory search "important" 2>&1 | grep -q "important data" || fail "data not recovered after rebuild"

# 3. Cache deleted outright: recreated from the row files
rm -f "$CACHE" "$CACHE-wal" "$CACHE-shm"
$LORE memory search "important" 2>&1 | grep -q "important data" || fail "data not recovered after cache removal"
[ -f "$CACHE" ] || fail "cache not recreated"
pass SC-3
