#!/usr/bin/env bash
# SC-26: a tampered cache is rebuilt from the row files, never trusted
# Catches: R21-49 (0.1.18: the row files are the truth; the repo cache
# records each table's schema fingerprint and its layout version, and a
# table or file that does not match is rebuilt instead of read)
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
need python3
mk_tmp; init_project mig
$LORE memory add --body "truth from the file" >/dev/null || fail "memory add"
CACHE=$(repo_cache) # under the lore home: cache/repo/<folder>-<hash>.db
[ -f "$CACHE" ] || fail "no cache file at $CACHE"

body() { $LORE memory list --json | json_get '",".join(r["body"] for r in d["data"])'; }
[ "$(body)" = "truth from the file" ] || fail "memory not listed before the tamper"

# 1. A table whose recorded fingerprint is wrong is rebuilt: the tampered
#    cached body never shows.
sqlite3 "$CACHE" "UPDATE memories SET body='tampered'" || fail "tamper body"
sqlite3 "$CACHE" "UPDATE _tables SET fingerprint='tampered' WHERE name='memories'" || fail "tamper fingerprint"
[ "$(body)" = "truth from the file" ] || fail "a table with a tampered fingerprint was read: $(body)"

# 2. A file of another layout version is emptied and rebuilt.
sqlite3 "$CACHE" "UPDATE memories SET body='tampered again'" || fail "tamper body"
sqlite3 "$CACHE" "PRAGMA user_version = 99" || fail "tamper layout version"
[ "$(body)" = "truth from the file" ] || fail "a cache of another layout version was read: $(body)"
[ "$(sqlite3 "$CACHE" "PRAGMA user_version")" != 99 ] || fail "the cache kept the foreign layout version"

$LORE doctor >/dev/null || fail "doctor not healthy after the rebuilds"
pass SC-26
