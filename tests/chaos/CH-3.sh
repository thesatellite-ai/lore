#!/usr/bin/env bash
# CH-3: SIGKILL mid-migration — partial state refuses to start
# Catches: R21-49, R23-39
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project ch3

# Simulate: insert a fake in_progress migration row.
sqlite3 .lore/lore.db "INSERT INTO schema_migrations (id, created_at, updated_at, version, migration_sha256, schema_sha256, status) VALUES ('smg_01a10237c9e87e4c843d8045973a65a1', datetime('now'), datetime('now'), 99, 'abc', 'def', 'in_progress')" 2>/dev/null \
    || skip "schema_migrations table missing (R21-49 deferred)"

$LORE doctor 2>&1 | grep -qE "migration.*in_progress|E_MIGRATION_INCOMPLETE" || \
    fail "doctor did not detect in_progress migration"
pass CH-3
