#!/usr/bin/env bash
# CH-2: Clock-skew sim — tx_at monotonic regardless of wall clock
# Catches: R16-10, R27-29
#
# With libfaketime the clock really jumps back between two writes. Without
# it, the same situation is reproduced by moving the DB's tx_at high-water
# mark one hour ahead of the wall clock (= "the previous write happened at
# a later wall-clock time"). Either way, tx_at must increase in insertion
# order (rowid) — ids are clock-derived, so they are not the reference.
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
need python3
mk_tmp; init_project ch2

if command -v faketime >/dev/null 2>&1; then
    faketime "2026-05-11 10:00:00" $LORE memory add --body "first" >/dev/null || fail "first add"
    faketime "2026-05-11 09:00:00" $LORE memory add --body "second" >/dev/null || fail "second add (clock back)"
else
    $LORE memory add --body "first" >/dev/null || fail "first add"
    AHEAD=$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc)+d.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%S.%f000Z"))')
    sqlite3 .lore/lore.db "UPDATE config SET value='$AHEAD' WHERE key='tx_clock_high_water'" || fail "set high-water"
    $LORE memory add --body "second" >/dev/null || fail "second add (clock behind high-water)"
fi

sqlite3 .lore/lore.db "SELECT tx_at FROM memories ORDER BY rowid" > tx.txt || fail "read tx_at"
python3 - tx.txt "${AHEAD:-}" <<'PY' || fail "tx_at went backward"
import sys, datetime as d, re
def parse(s):
    s = re.sub(r" m=[+-].*$", "", s.strip())          # Go monotonic suffix
    s = re.sub(r" [A-Z]{2,5}$", "", s)                 # zone name
    for f in ("%Y-%m-%d %H:%M:%S.%f %z", "%Y-%m-%d %H:%M:%S %z"):
        try:
            return d.datetime.strptime(s[:26] + s[s.rfind(" "):] if "." in s else s, f)
        except ValueError:
            pass
    raise SystemExit("unparseable tx_at: " + s)
ts = [parse(l) for l in open(sys.argv[1]) if l.strip()]
if len(ts) < 2: raise SystemExit("expected 2 rows")
if not all(b > a for a, b in zip(ts, ts[1:])): raise SystemExit(f"not monotonic: {ts}")
if len(sys.argv) > 2 and sys.argv[2]:
    hw = d.datetime.strptime(sys.argv[2][:26], "%Y-%m-%dT%H:%M:%S.%f").replace(tzinfo=d.timezone.utc)
    if not ts[-1] > hw: raise SystemExit(f"second write {ts[-1]} is not after the high-water mark {hw}")
PY
pass CH-2
