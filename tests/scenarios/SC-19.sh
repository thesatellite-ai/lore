#!/usr/bin/env bash
# SC-19: Bidi / zero-width strip (Trojan Source defense)
# Catches: R23-21, R23-22
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project bidi

INPUT="$(printf 'normal text \xE2\x80\xAE attack')"
$LORE memory add --body "$INPUT" >/dev/null || fail "memory add"

# Neither the row file (what git shares) nor what lore reads back may hold
# U+202E (E2 80 AE); the regular text stays.
FILE=$(find .lore/data/memories -name 'mem_*.json' | head -1)
[ -n "$FILE" ] || fail "no memory row file"
grep -q "$(printf '\xE2\x80\xAE')" "$FILE" && fail "bidi char stored in $FILE"
grep -q '\\u202[eE]' "$FILE" && fail "bidi char stored escaped in $FILE"
STORED=$($LORE sql "SELECT body FROM memories" --json | json_get 'd["rows"][0]["cells"][0]["value"]') || fail "lore sql"
printf '%s' "$STORED" | grep -q "$(printf '\xE2\x80\xAE')" && fail "bidi char read back"
echo "$STORED" | grep -q "normal text" || fail "regular text lost: $STORED"
pass SC-19
