#!/usr/bin/env bash
# SC-30: aicoder errors list
# Catches: R29-50
source "$(dirname "$0")/../lib/common.sh"
need jq
OUT=$($LORE errors list --json) || fail "errors list"
LEN=$(echo "$OUT" | jq '.errors | length')
[ "$LEN" -ge 20 ] || fail "expected ≥20 error codes, got $LEN"
echo "$OUT" | jq -e '.errors[] | select(.code == "E_DB_LOCKED")' >/dev/null || fail "E_DB_LOCKED missing"
echo "$OUT" | jq -e '.errors[] | select(.code == "E_SECRET_DETECTED")' >/dev/null || fail "E_SECRET_DETECTED missing"
pass SC-30
