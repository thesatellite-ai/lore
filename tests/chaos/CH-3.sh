#!/usr/bin/env bash
# CH-3: SIGKILL mid-migration — the lock it left is named, refused, and only released when its process is gone
# Catches: R21-49, R23-39 (0.1.18: `lore migrate run` holds .lore/state/migrate.lock
# with its pid; a killed run leaves it behind)
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch3

LOCK=.lore/state/migrate.lock
mkdir -p .lore/state
# init_project names an unreachable origin: every run is --offline (branch files only).

# A live holder: another migration still running. Refused, never released.
sleep 60 & LIVE=$!
printf '%s' "$LIVE" > "$LOCK"
OUT=$($LORE migrate run --offline 2>&1) && fail "migrate run went past a live lock: $OUT"
echo "$OUT" | grep -q "E_MIGRATE_LOCKED" || fail "no E_MIGRATE_LOCKED: $OUT"
echo "$OUT" | grep -q "pid $LIVE" || fail "the holder's pid is not named: $OUT"
OUT=$($LORE migrate unlock 2>&1) && fail "unlock released a lock whose process is alive: $OUT"
[ -f "$LOCK" ] || fail "unlock removed a live process's lock"
kill "$LIVE" 2>/dev/null; wait "$LIVE" 2>/dev/null

# The killed run: its pid is gone. status says so, and the next run takes
# the lock over and says so (owner item 46: a dead holder's lock is no
# reason to stop).
DEAD=$(sh -c 'echo $$')
printf '%s' "$DEAD" > "$LOCK"
$LORE migrate status 2>&1 | grep -q "stale: pid $DEAD is gone" || fail "status does not name the stale lock"
OUT=$($LORE migrate run --offline 2>&1) || fail "migrate run did not take over a stale lock: $OUT"
echo "$OUT" | grep -q "took over .lore/state/migrate.lock from pid $DEAD, which is no longer running" || fail "the take-over is not said: $OUT"
[ -f "$LOCK" ] && fail "the stale lock is still there"
$LORE doctor >/dev/null || fail "doctor not healthy afterwards"
pass CH-3
