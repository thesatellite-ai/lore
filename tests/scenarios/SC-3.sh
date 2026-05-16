#!/usr/bin/env bash
# SC-3: Disaster recovery — corrupt DB → repair from backup
# Catches: R16-7+#8, R23-13, R23-44
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project drtest
$LORE memory add "important data" >/dev/null || fail "add"
$LORE backup >/dev/null || fail "backup"
ls .lore/backups/*.sqlite >/dev/null 2>&1 || fail "no backup file written"

# Truncate the DB.
: > .lore/lore.db

# doctor reports broken (non-zero exit).
if $LORE doctor >/dev/null 2>&1; then
    fail "doctor should fail on truncated DB"
fi

$LORE repair --tier=2 --confirm >/dev/null || fail "repair tier-2"
$LORE memory search "important" 2>&1 | grep -q "important data" || fail "data not recovered"
pass SC-3
