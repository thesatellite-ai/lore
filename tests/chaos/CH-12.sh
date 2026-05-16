#!/usr/bin/env bash
# CH-12: Ambiguous .lore/ (both .db and .toml present)
# Catches: R18-1
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project ch12

# Inject a stray toml alongside the existing lore.db.
cat > .lore/lore.toml <<TML
db_path = "/tmp/other.db"
project_id = "prj_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
TML

$LORE memory search "x" 2>&1 | grep -qE "ambiguous|E_AMBIGUOUS_PROJECT|both" || \
    fail "ambiguous .lore/ not refused"
pass CH-12
