#!/usr/bin/env bash
# SC-9: Identity fallback under stripped env
# Catches: R32
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project idtest

env -i PATH="$PATH" HOME="$TMP" $LORE memory add "x" --db=.lore/lore.db >/dev/null 2>&1 || true

# An actor row must exist regardless of env.
ROWS=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM actors")
[ "$ROWS" -gt 0 ] || fail "no actor row created"
pass SC-9
