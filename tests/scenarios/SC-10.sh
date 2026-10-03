#!/usr/bin/env bash
# SC-10: --no-inherit strict scope
# Catches: R35
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project ni
$LORE repo add web1 --origin=https://github.com/org/web.git >/dev/null
$LORE memory add --body "MasterRule" >/dev/null
$LORE memory add --body "Web1Specific" --repo=web1 >/dev/null

DEF=$($LORE memory list --repo=web1 --json)
echo "$DEF" | jq -e '.data[] | select(.body|contains("MasterRule"))' >/dev/null || fail "default missing master"
echo "$DEF" | jq -e '.data[] | select(.body|contains("Web1Specific"))' >/dev/null || fail "default missing repo"

STRICT=$($LORE memory list --repo=web1 --no-inherit --json)
echo "$STRICT" | jq -e '.data[] | select(.body|contains("Web1Specific"))' >/dev/null || fail "strict missing repo"
echo "$STRICT" | jq -e '.data[] | select(.body|contains("MasterRule"))' >/dev/null && fail "strict leaked master"
pass SC-10
