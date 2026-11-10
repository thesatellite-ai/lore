#!/usr/bin/env bash
# SC-14: lore why-context shows the last render
# Catches: R22-11, R34-7
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project whyt
$LORE rule add --severity=must --body "important context" >/dev/null
$LORE render >/dev/null || fail "render"
$LORE why-context --json > "$TMP/why.json" || fail "why-context --json"
json_true 'd["schema_version"] == 1' < "$TMP/why.json" >/dev/null || fail "schema_version missing"
$LORE why-context --rendered 2>&1 | grep -q "important context" || fail "rendered text missing the rule"
pass SC-14
