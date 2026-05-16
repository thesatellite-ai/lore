#!/usr/bin/env bash
# SC-18: NFC normalization on write
# Catches: R18-20, R24-1, R27-31
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
need jq
mk_tmp; init_project nfc

# NFC form: 'é' is one codepoint (0xC3 0xA9 in UTF-8).
$LORE memory add "$(printf 'caf\xc3\xa9')" >/dev/null
# NFD form: 'e' + combining acute (0xCC 0x81).
$LORE memory add "$(printf 'cafe\xcc\x81')" >/dev/null

# Both rows should normalize to NFC, so query with NFC matches both.
HITS=$($LORE memory search "café" --json | jq '.count')
[ "$HITS" -eq 2 ] || fail "expected 2 NFC-normalized hits, got $HITS"
pass SC-18
