#!/usr/bin/env bash
# SC-17: Empty body refused
# Catches: R23-19, R24-16
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project emp
for body in "" "    " $'\n\n\t'; do
    $LORE memory add "$body" 2>&1 | grep -qE "empty|whitespace|E_EMPTY_BODY|E_INVALID_INPUT" || fail "empty/whitespace not refused: '$body'"
done
pass SC-17
