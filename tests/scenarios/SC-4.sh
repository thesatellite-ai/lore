#!/usr/bin/env bash
# SC-4: Multi-repo project — repo-scoped memories don't leak
# Catches: R17, R20, R22
source "$(dirname "$0")/../lib/common.sh"
need jq
mk_tmp; init_project multi
$LORE repo add web1 --origin=git@github.com:org/web.git >/dev/null || fail "repo add web1"
$LORE repo add admin --origin=git@github.com:org/admin.git >/dev/null
$LORE repo add api --origin=git@github.com:org/api.git >/dev/null

$LORE memory add "Tailwind in web1" --repo=web1 >/dev/null
$LORE memory add "Mantine in admin" --repo=admin >/dev/null
$LORE memory add "gqlgen in api" --repo=api >/dev/null
$LORE memory add "Client uses USD" >/dev/null

OUT=$($LORE memory search "" --repo=web1 --json)
echo "$OUT" | jq -e '.results[] | select(.body | contains("Tailwind in web1"))' >/dev/null || fail "web1 memory missing"
echo "$OUT" | jq -e '.results[] | select(.body | contains("Client uses USD"))' >/dev/null || fail "master memory missing"
echo "$OUT" | jq -e '.results[] | select(.body | contains("Mantine"))' >/dev/null && fail "admin memory leaked"
echo "$OUT" | jq -e '.results[] | select(.body | contains("gqlgen"))' >/dev/null && fail "api memory leaked"
pass SC-4
