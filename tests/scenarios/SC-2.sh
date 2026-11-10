#!/usr/bin/env bash
# SC-2: Audit log integrity (hash chain)
# Catches: R16-4, R27-9, R37-Block-5
#
# 0.1.18: the chain is the audit_chain table of ~/.lore/state/machine.db
# (wire/auditchain.go), never synced; v0.1.17 kept it in .lore/lore.db.
# Editing one entry and deleting another must each break the chain
# (E_AUDIT_CHAIN_BROKEN, exit 23); the untouched copy verifies.
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; isolate_home; init_project audit2

for i in 1 2 3 4 5 6 7 8 9 10; do
    $LORE memory add --body "memory $i" >/dev/null || fail "add $i"
done

MDB="$HOME/.lore/state/machine.db"
[ -f "$MDB" ] || fail "machine store $MDB missing"
$LORE audit verify >/dev/null || fail "verify clean chain failed"
cp "$MDB" "$TMP/machine.clean"

# tamper: python's sqlite3, behind lore's back
tamper() {
    cp "$TMP/machine.clean" "$MDB"
    python3 - "$MDB" "$1" <<'PY' || fail "tamper step failed"
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
n = c.execute("SELECT count(*) FROM audit_chain").fetchone()[0]
if n < 10:
    sys.exit("only %d chain entries" % n)
pos = "(SELECT pos FROM audit_chain ORDER BY pos LIMIT 1 OFFSET 4)"
if sys.argv[2] == "edit":
    c.execute("UPDATE audit_chain SET target_id='HACKED' WHERE pos = " + pos)
else:
    c.execute("DELETE FROM audit_chain WHERE pos = " + pos)
c.commit()
PY
}

for how in edit delete; do
    tamper "$how"
    OUT=$($LORE audit verify 2>&1); RC=$?
    [ "$RC" -eq 23 ] || fail "verify after $how: exit $RC, want 23 ($OUT)"
    echo "$OUT" | grep -q "E_AUDIT_CHAIN_BROKEN" || fail "verify did not detect $how: $OUT"
done

cp "$TMP/machine.clean" "$MDB"
$LORE audit verify >/dev/null || fail "restored chain does not verify"
pass SC-2
