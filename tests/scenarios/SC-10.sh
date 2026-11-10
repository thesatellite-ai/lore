#!/usr/bin/env bash
# SC-10: --no-inherit strict scope
# Catches: R35
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project ni
$LORE repo add web1 --origin=https://github.com/org/web.git >/dev/null
$LORE memory add --body "MasterRule" >/dev/null
$LORE memory add --body "Web1Specific" --repo=web1 >/dev/null

DEF=$($LORE memory list --repo=web1 --json)
echo "$DEF" | json_true 'any("MasterRule" in r["body"] for r in d["data"])' >/dev/null || fail "default missing master"
echo "$DEF" | json_true 'any("Web1Specific" in r["body"] for r in d["data"])' >/dev/null || fail "default missing repo"

STRICT=$($LORE memory list --repo=web1 --no-inherit --json)
echo "$STRICT" | json_true 'any("Web1Specific" in r["body"] for r in d["data"])' >/dev/null || fail "strict missing repo"
echo "$STRICT" | json_true 'any("MasterRule" in r["body"] for r in d["data"])' >/dev/null && fail "strict leaked master"
pass SC-10
