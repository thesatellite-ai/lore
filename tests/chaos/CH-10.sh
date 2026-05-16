#!/usr/bin/env bash
# CH-10: Secret pasted into memory body refused, no row inserted
# Catches: R16-12
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project ch10

$LORE memory add "AKIAIOSFODNN7EXAMPLE/wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" 2>&1 | \
    grep -qE "E_SECRET_DETECTED|secret" || fail "AWS-pair secret not refused"

COUNT=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM memories")
[ "$COUNT" -eq 0 ] || fail "secret leaked into DB ($COUNT rows)"
pass CH-10
