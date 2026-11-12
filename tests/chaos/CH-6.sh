#!/usr/bin/env bash
# CH-6: DB truncated to 0 bytes
# Catches: R23-9
#
# 0.1.18: the repo's database is its cache under ~/.lore/cache/repo; the rows are
# files in .lore/data, and lore backup/repair were removed (CHANGELOG 0.1.18
# "Removed"). A cache truncated to 0 bytes must lose nothing: the next read
# finds every row, the cache is rebuilt to a real database, and doctor's
# [db] section is ok afterwards.
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; isolate_home; init_project ch6
CACHE=$(repo_cache)
$LORE memory add --body "zero-byte survivor" >/dev/null || fail "add"
$LORE rule add --severity=must --body "zero-byte rule" >/dev/null || fail "rule add"

: > "$CACHE"

$LORE memory search "survivor" 2>&1 | grep -q "zero-byte survivor" || fail "data not restored (search)"
$LORE rule list 2>&1 | grep -q "zero-byte rule" || fail "data not restored (rule list)"
[ -s "$CACHE" ] || fail "cache still 0 bytes after a read"
$LORE doctor --json 2>/dev/null | grep -q '"db_ok": *true' || fail "doctor --json db_ok is not true after the rebuild"
$LORE doctor 2>&1 | sed -n '/^\[db\]/,/^$/p' | grep -q "ok: *true" || fail "doctor [db] not ok after the rebuild"
pass CH-6
