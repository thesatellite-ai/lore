#!/usr/bin/env bash
# SC-25: Auto-gitignore lore.db on init
# Catches: R18-34, R22-41
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; init_project gi
grep -q "\.lore/aicoder\.db" .gitignore || fail ".gitignore missing lore.db"
# WAL / shm should also be excluded (one of these patterns).
grep -qE "\.lore/state|\.db-wal|\.lore/\*\*" .gitignore || \
    fail ".gitignore missing WAL/state pattern"
pass SC-25
