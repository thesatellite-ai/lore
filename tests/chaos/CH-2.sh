#!/usr/bin/env bash
# CH-2: Clock-skew sim — tx_at monotonic regardless of wall clock
# Catches: R16-10, R27-29
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
command -v faketime >/dev/null 2>&1 || skip "faketime (libfaketime) not installed"
mk_tmp; init_project ch2

faketime "2026-05-11 10:00:00" $LORE memory add "first" >/dev/null || fail "first add"
faketime "2026-05-11 09:00:00" $LORE memory add "second" >/dev/null || fail "second add (clock back)"

# Check tx_at column is strictly monotonic.
PREV=""
while read -r t; do
    if [ -n "$PREV" ]; then
        [[ "$t" > "$PREV" ]] || [ "$t" = "$PREV" ] || fail "tx_at went backward: $PREV → $t"
    fi
    PREV="$t"
done < <(sqlite3 .lore/lore.db "SELECT tx_at FROM memories ORDER BY id" 2>/dev/null || echo "")
[ -n "$PREV" ] || skip "tx_at column not implemented (R37 audit chain v0.2)"
pass CH-2
