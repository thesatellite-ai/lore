#!/usr/bin/env bash
# SC-11: Project name collision in Mode B
# Catches: R20
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
SHARED="$TMP/shared.db"

mkdir -p "$TMP/p1"; cd "$TMP/p1"
$LORE project shared-init --db="$SHARED" --name=same >/dev/null || fail "first init"

# Re-using the same name from a different cwd MUST attach to the existing project,
# not crash and not silently shadow.
mkdir -p "$TMP/p2"; cd "$TMP/p2"
OUT=$($LORE project shared-init --db="$SHARED" --name=same --json) || fail "second init"
echo "$OUT" | grep -q '"reused": *true' || fail "second init did not reuse existing project"
pass SC-11
