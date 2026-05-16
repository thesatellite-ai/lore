#!/usr/bin/env bash
# SC-27: Worktree detection — .git as file (not dir)
# Catches: R18-15
#
# Per R14, aicoder does NOT walk up to find .lore/. From inside a worktree,
# commands either need explicit --db or a fresh .lore/ in the worktree.
# This scenario verifies: (1) worktree's .git is a regular file, and
# (2) `aicoder memory add --db=<main>` from inside the worktree works.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
git init -q .
git config user.email "test@test.invalid"
git config user.name  "Test"
git commit --allow-empty -q -m init
$LORE init --non-interactive --name=worktreemain >/dev/null || fail "init main"
MAIN_DB="$TMP/.lore/lore.db"

git worktree add "$TMP/wt" -b feature >/dev/null 2>&1 || skip "git worktree unavailable"

cd "$TMP/wt"
[ -f .git ] || fail "worktree .git is not a regular file"

# Without --db, aicoder refuses (no walk-up). That refusal IS the correct behavior.
$LORE memory add "should-refuse" 2>&1 | grep -qE "E_NOT_PROJECT_ROOT|not an aicoder" \
    || fail "worktree without --db should refuse"

# With explicit --db pointing at the main repo's DB, write succeeds.
$LORE memory add "from-worktree" --db="$MAIN_DB" >/dev/null \
    || fail "memory add with explicit --db from worktree failed"
pass SC-27
