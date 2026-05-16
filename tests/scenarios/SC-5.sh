#!/usr/bin/env bash
# SC-5: Concurrent writes from two terminals
# Catches: R16-6, R18-6, R23-27
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project concurrent

(for i in $(seq 1 25); do $LORE memory add "A-$i" >/dev/null 2>&1; done) &
PID_A=$!
(for i in $(seq 1 25); do $LORE memory add "B-$i" >/dev/null 2>&1; done) &
PID_B=$!
wait $PID_A $PID_B

COUNT=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM memories")
[ "$COUNT" -eq 50 ] || fail "expected 50 rows, got $COUNT (lock contention?)"
pass SC-5
