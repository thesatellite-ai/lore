#!/usr/bin/env bash
# SC-5: Concurrent writes from two terminals
# Catches: R16-6, R18-6, R23-27
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project concurrent

# Each failed add records its exit code and stderr, so a lost write is
# explained instead of only counted.
add_many() {
    for i in $(seq 1 25); do
        out=$($LORE memory add --body "$1-$i" 2>&1 >/dev/null)
        rc=$?
        if [ "$rc" -ne 0 ]; then
            printf '%s-%s exit=%s: %s\n' "$1" "$i" "$rc" "$out" >> failures.txt
        fi
    done
}
add_many A &
PID_A=$!
add_many B &
PID_B=$!
wait $PID_A $PID_B

COUNT=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM memories")
if [ "$COUNT" -ne 50 ]; then
    [ -f failures.txt ] && sed 's/^/  /' failures.txt >&2
    fail "expected 50 rows, got $COUNT (failed adds above; none listed = a write reported success but is missing)"
fi
pass SC-5
