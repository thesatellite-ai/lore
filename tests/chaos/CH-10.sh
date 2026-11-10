#!/usr/bin/env bash
# CH-10: Secret pasted into memory body refused, no row inserted
# Catches: R16-12
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch10

$LORE memory add --body "AKIA""IOSFODNN7EXAMPLE/wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" 2>&1 | \
    grep -qE "E_SECRET_DETECTED|secret" || fail "AWS-pair secret not refused"

COUNT=$(lore_count memories)
[ "$COUNT" -eq 0 ] || fail "secret leaked into the store ($COUNT rows)"
[ -z "$(find .lore/data -path '*/memories/*' -name '*.json' 2>/dev/null)" ] || fail "secret leaked into a row file"
pass CH-10
