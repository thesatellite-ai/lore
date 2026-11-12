#!/usr/bin/env bash
# SC-22: O_NOFOLLOW refuses a symlinked database
# Catches: R16-13, R18-13, R29-36
#
# 0.1.18: the repo's database is its cache under the lore home,
# ~/.lore/cache/repo/<folder>-<hash>.db (v0.1.17's .lore/lore.db is gone;
# the cache left the work tree). A link in its place, pointing at a file outside,
# is refused with E_SYMLINK_DB (exit 23) on init and on every write, and the
# file it points at is never touched. Removing the link heals it.
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; isolate_home
git init -q
git remote add origin https://github.com/test/symdb.git
printf 'decoy, not a database\n' > "$TMP/decoy"
DECOY_SUM=$(cksum < "$TMP/decoy")
CACHE=$(repo_cache)
mkdir -p "$(dirname "$CACHE")"
ln -s "$TMP/decoy" "$CACHE"

refused() {
    local cmd="$*" OUT RC
    OUT=$($LORE "$@" 2>&1); RC=$?
    [ "$RC" -eq 23 ] || fail "lore $cmd: exit $RC, want 23 ($OUT)"
    echo "$OUT" | grep -q "E_SYMLINK_DB" || fail "lore $cmd did not refuse symlinked DB: $OUT"
    [ "$(cksum < "$TMP/decoy")" = "$DECOY_SUM" ] || fail "lore $cmd wrote through the link"
    [ -L "$CACHE" ] || fail "lore $cmd replaced the link"
}
refused init --non-interactive --name=symdb
refused memory add --body x

# the hint's fix works
rm "$CACHE"
$LORE init --non-interactive --name=symdb >/dev/null 2>&1 || fail "init after removing the link"
$LORE memory add --body x >/dev/null || fail "memory add after removing the link"
[ ! -L "$CACHE" ] && [ -f "$CACHE" ] || fail "cache not a regular file"
pass SC-22
