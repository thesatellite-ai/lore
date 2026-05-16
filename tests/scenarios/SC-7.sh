#!/usr/bin/env bash
# SC-7: Mode B shared DB (now fully implemented)
# Catches: R17-2, R36, R20
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
SHARED_DB="$TMP/shared.db"
mkdir -p "$TMP/p1"
cd "$TMP/p1"
$LORE project shared-init --db="$SHARED_DB" --name=alpha >/dev/null || fail "shared-init alpha"
[ -f .lore/lore.toml ] || fail "toml not written"
grep -q "db_path" .lore/lore.toml || fail "toml missing db_path"
$LORE memory add "alpha memory" >/dev/null || fail "alpha write"

mkdir -p "$TMP/p2"
cd "$TMP/p2"
$LORE project shared-init --db="$SHARED_DB" --name=beta >/dev/null || fail "shared-init beta"
$LORE memory add "beta memory" >/dev/null || fail "beta write"

# Each project sees ONLY its own memories.
cd "$TMP/p1"
$LORE memory search "alpha" 2>&1 | grep -q "alpha memory" || fail "p1 lost alpha memory"
$LORE memory search "beta" 2>&1 | grep -q "beta memory" && fail "p1 leaked beta memory"

cd "$TMP/p2"
$LORE memory search "beta" 2>&1 | grep -q "beta memory" || fail "p2 lost beta memory"
$LORE memory search "alpha" 2>&1 | grep -q "alpha memory" && fail "p2 leaked alpha memory"

# shared-list shows both projects.
COUNT=$($LORE project shared-list --db="$SHARED_DB" --json | python3 -c "import sys,json;print(json.load(sys.stdin)['count'])")
[ "$COUNT" -eq 2 ] || fail "shared-list expected 2, got $COUNT"
pass SC-7
