#!/usr/bin/env bash
# SC-12: Determinism — same DB → byte-identical render
# Catches: R16-5, R21-24, R34-4
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project det
for i in $(seq 1 20); do $LORE memory add "memory $i" >/dev/null; done

$LORE render >/dev/null || fail "first render"
SHA1=$(shasum -a 256 CLAUDE.md | awk '{print $1}')
sleep 1
$LORE render >/dev/null || fail "second render"
SHA2=$(shasum -a 256 CLAUDE.md | awk '{print $1}')
[ "$SHA1" = "$SHA2" ] || fail "non-deterministic render: $SHA1 != $SHA2"
pass SC-12
