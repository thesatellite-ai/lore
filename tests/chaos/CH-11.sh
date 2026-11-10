#!/usr/bin/env bash
# CH-11: Trojan Source bidi attack — stored bytes must not contain U+202E/202D
# Catches: R23-21
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch11

INPUT="$(printf 'visible \xE2\x80\xAE\xE2\x80\xAE invisible')"
$LORE memory add --body "$INPUT" >/dev/null || fail "memory add"

# The row file is what git shares: no U+202E/U+202D, raw or JSON-escaped.
FILE=$(find .lore/data/memories -name 'mem_*.json' | head -1)
[ -n "$FILE" ] || fail "no memory row file"
od -An -tx1 "$FILE" | tr -d ' \n' | grep -qE "e280a[de]" && fail "bidi bytes preserved in $FILE"
grep -qiE '\\u202[de]' "$FILE" && fail "bidi char stored escaped in $FILE"
STORED=$($LORE memory list --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"][0]["body"])') || fail "memory list"
printf '%s' "$STORED" | od -An -tx1 | tr -d ' \n' | grep -qE "e280a[de]" && fail "bidi bytes read back"
pass CH-11
