#!/usr/bin/env bash
# SC-22: O_NOFOLLOW refuses a symlinked database
# Catches: R16-13, R18-13, R29-36
#
# 0.1.18: the repo's database is its cache under the lore home,
# ~/.lore/cache/repo/<folder>-<hash>.db (v0.1.17's .lore/lore.db is gone;
# the cache left the work tree). A link in its place, pointing at a file outside,
# is refused with E_SYMLINK_DB (exit 23) on init and on every write in a
# set-up project, and the file it points at is never touched. Removing the
# link heals it. A write in the repo before init refuses earlier (S-2: lore
# isn't set up there, exit 19) and never opens the cache either.
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

# before init: no project, so the write refuses before the cache (S-2)
OUT=$($LORE memory add --body x 2>&1); RC=$?
[ "$RC" -eq 19 ] || fail "memory add before init: exit $RC, want 19 ($OUT)"
echo "$OUT" | grep -q "E_NOT_A_STORE" || fail "memory add before init: $OUT"
[ "$(cksum < "$TMP/decoy")" = "$DECOY_SUM" ] || fail "memory add before init wrote through the link"
[ -L "$CACHE" ] || fail "memory add before init replaced the link"
[ ! -e .lore ] || fail "memory add before init made .lore/"

# the hint's fix works
rm "$CACHE"
$LORE init --non-interactive --name=symdb >/dev/null 2>&1 || fail "init after removing the link"

# a set-up project whose cache is then replaced by a link: every write refuses
rm "$CACHE"
ln -s "$TMP/decoy" "$CACHE"
refused memory add --body x
rm "$CACHE"
$LORE memory add --body x >/dev/null || fail "memory add after removing the link"
[ ! -L "$CACHE" ] && [ -f "$CACHE" ] || fail "cache not a regular file"
pass SC-22
