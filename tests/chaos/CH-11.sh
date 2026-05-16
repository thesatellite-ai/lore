#!/usr/bin/env bash
# CH-11: Trojan Source bidi attack — stored bytes must not contain U+202E/202D
# Catches: R23-21
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project ch11

INPUT="$(printf 'visible \xE2\x80\xAE\xE2\x80\xAE invisible')"
$LORE memory add "$INPUT" >/dev/null

STORED=$(sqlite3 .lore/lore.db "SELECT body FROM memories LIMIT 1")
echo -n "$STORED" | xxd -p | tr -d '\n' | grep -qE "e280a[de]" && fail "bidi bytes preserved in DB"
pass CH-11
