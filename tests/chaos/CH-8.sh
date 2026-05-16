#!/usr/bin/env bash
# CH-8: Stale flock from killed process — reclaim, not refuse
# Catches: R23-27
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch8

mkdir -p .lore/state
# Pick a PID that is guaranteed not to exist.
echo "9999999" > .lore/state/lock

$LORE memory add "ok" >/dev/null || fail "stale lock not reclaimed"
pass CH-8
