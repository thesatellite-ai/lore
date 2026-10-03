#!/usr/bin/env bash
# SC-2: Audit log integrity (hash chain)
# Catches: R16-4, R27-9, R37-Block-5
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project audit2

for i in 1 2 3 4 5 6 7 8 9 10; do
    $LORE memory add --body "memory $i" >/dev/null || fail "add $i"
done


$LORE audit verify || fail "verify clean chain failed"

# Tamper with one row behind lore's back. python's sqlite3 (FTS5-enabled) is
# used because the memories table carries FTS5 triggers that a minimal
# system sqlite3 cannot run.
python3 - <<'PY' || fail "tamper step failed"
import sqlite3
c = sqlite3.connect(".lore/lore.db")
c.execute("UPDATE memories SET body='HACKED' WHERE id = (SELECT id FROM memories ORDER BY rowid LIMIT 1 OFFSET 4)")
c.commit()
PY

if $LORE audit verify 2>&1 | grep -qE "audit chain broken|hash mismatch|tampered"; then
    pass SC-2
fi
fail "verify did not detect tampered row"
