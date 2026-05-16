#!/usr/bin/env bash
# SC-14: aicoder why-context --last-render
# Catches: R22-11, R34-7
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project whyt
$LORE memory add "important context" >/dev/null
$LORE render >/dev/null || fail "render"
$LORE why-context --last-render --json > /tmp/why.json || fail "why-context --json"
jq -e '.schema_version == 1' /tmp/why.json >/dev/null || fail "schema_version missing"
$LORE why-context --last-render --rendered 2>&1 | grep -q "important context" || fail "rendered text missing memory"
pass SC-14
