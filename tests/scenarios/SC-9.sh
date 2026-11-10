#!/usr/bin/env bash
# SC-9: Identity fallback under stripped env
# Catches: R32
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project idtest

# No git identity, no USER, no HOME config: the write still succeeds and
# names an actor (the fallback), never fails for want of an identity.
env -i PATH="$PATH" HOME="$TMP/bare" $LORE memory add --body "x" >/dev/null || fail "memory add under a stripped env"

[ "$(lore_count memories)" -eq 1 ] || fail "the memory was not written"
[ "$(lore_count actors)" -gt 0 ] || fail "no actor row created"
grep -q '"_hlc": *"[^"]*/act_' .lore/data/memories/mem_*.json || fail "the memory names no actor"
pass SC-9
