#!/usr/bin/env bash
# SC-2: Audit log integrity (hash chain)
# Catches: R16-4, R27-9, R37-Block-5
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project audit2

for i in 1 2 3 4 5 6 7 8 9 10; do
    $LORE memory add "memory $i" >/dev/null || fail "add $i"
done

# audit verify subcommand may not exist yet in v0.1 → skip if missing.
$LORE audit --help >/dev/null 2>&1 || skip "audit verify not implemented yet (R37 v0.2)"

$LORE audit verify || fail "verify clean chain failed"

# Tamper with one row.
sqlite3 .lore/lore.db "UPDATE memories SET body='HACKED' WHERE display_num=5"

if $LORE audit verify 2>&1 | grep -qE "audit chain broken|hash mismatch|tampered"; then
    pass SC-2
fi
fail "verify did not detect tampered row"
