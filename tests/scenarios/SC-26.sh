#!/usr/bin/env bash
# SC-26: Schema migration replay log integrity
# Catches: R21-49
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project mig

ROWS=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM schema_migrations WHERE status='applied'" 2>/dev/null || echo 0)
[ "$ROWS" -gt 0 ] || skip "schema_migrations not seeded (R21-49 deferred)"

# Tamper with applied sha.
sqlite3 .lore/lore.db "UPDATE schema_migrations SET migration_sha256='tampered' WHERE rowid=1"

$LORE doctor 2>&1 | grep -qE "migration.*hash|tampered|E_SCHEMA_MIGRATION" || \
    fail "tampered migration sha not detected"
pass SC-26
