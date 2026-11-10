#!/usr/bin/env bash
# SC-34: a missing row is E_NOT_FOUND with its own exit, a message and a hint
# Catches: the generic error codes (E_NOT_FOUND) print code, message and hint
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project nf34

MISSING=mem_01a1bb1b632f7b7bac4be168a78ea599
OUT=$($LORE memory show "$MISSING" --json); rc=$?
[ "$rc" -ne 0 ] || fail "show of a missing row exited 0"
printf '%s' "$OUT" | json_true 'd["error"]["code"] == "E_NOT_FOUND"' || fail "not E_NOT_FOUND: $OUT"
[ "$(printf '%s' "$OUT" | json_get 'd["error"]["exit"]')" = "$rc" ] || fail "envelope exit is not the process exit ($rc): $OUT"
printf '%s' "$OUT" | json_true 'bool(d["error"]["message"]) and bool(d["error"]["hint"])' || fail "no message or hint: $OUT"
$LORE memory show "$MISSING" 2>&1 | grep -q "E_NOT_FOUND" || fail "text output lacks the code"
pass SC-34
