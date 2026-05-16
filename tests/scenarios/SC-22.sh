#!/usr/bin/env bash
# SC-22: O_NOFOLLOW refuses symlink to DB
# Catches: R16-13, R18-13, R29-36
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
git init -q
mkdir -p .lore
# Point lore.db at /etc/passwd via symlink.
ln -s /etc/passwd .lore/lore.db
$LORE memory add "x" 2>&1 | grep -qE "symlink|E_SYMLINK_DB|refuses" || \
    fail "did not refuse symlinked DB"
pass SC-22
