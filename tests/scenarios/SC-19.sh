#!/usr/bin/env bash
# SC-19: Bidi / zero-width strip (Trojan Source defense)
# Catches: R23-21, R23-22
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project bidi

INPUT="$(printf 'normal text \xE2\x80\xAE attack')"
$LORE memory add "$INPUT" >/dev/null

# DB column must not contain U+202E (E2 80 AE).
STORED=$(sqlite3 .lore/lore.db "SELECT body FROM memories LIMIT 1")
echo -n "$STORED" | xxd -p | tr -d '\n' | grep -q "e280ae" && fail "bidi char preserved"
echo "$STORED" | grep -q "normal text" || fail "regular text lost"
pass SC-19
