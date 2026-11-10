#!/usr/bin/env bash
# SC-27: Worktree detection — .git as file (not dir)
# Catches: R18-15
#
# Per R14, lore does NOT walk up to find .lore/. 0.1.18: rows are files in
# the checkout (.lore/data), so a worktree reads and writes its OWN
# checkout's rows; v0.1.17's --db escape hatch is gone. This scenario
# verifies: (1) a worktree's .git is a regular file; (2) a worktree of a
# commit without .lore refuses (no walk-up into the main checkout, nothing
# written there); (3) a worktree of a commit that holds .lore reads the
# committed rows, and its own write stays in the worktree until merged.
source "$(dirname "$0")/../lib/common.sh"
mk_tmp; isolate_home
mkdir main && cd main
git init -q .
git commit --allow-empty -q -m init
BARE=$(git rev-parse HEAD)
$LORE init --non-interactive --name=worktreemain >/dev/null || fail "init main"
$LORE memory add --body "from-main" >/dev/null || fail "memory add in main"
git add -A && git commit -q -m "lore data" || fail "commit .lore"
MAIN_FILES=$(find .lore/data -type f | sort | cksum)

# (1) + (2): worktree of the commit before .lore
git worktree add -q "$TMP/wt0" "$BARE" >/dev/null 2>&1 || fail "git worktree add failed"
cd "$TMP/wt0"
[ -f .git ] || fail "worktree .git is not a regular file"
OUT=$($LORE memory add --body "should-refuse" 2>&1); RC=$?
[ "$RC" -eq 27 ] || fail "worktree without a project: exit $RC, want 27 ($OUT)"
echo "$OUT" | grep -q "no lore project" || fail "worktree without a project should refuse: $OUT"
[ "$(cd "$TMP/main" && find .lore/data -type f | sort | cksum)" = "$MAIN_FILES" ] \
    || fail "the refused write reached the main checkout"

# (3): worktree of the commit with .lore
cd "$TMP/main"
git worktree add -q "$TMP/wt" -b feature >/dev/null 2>&1 || fail "worktree add feature"
cd "$TMP/wt"
[ -f .git ] || fail "worktree .git is not a regular file"
$LORE memory search "from" 2>&1 | grep -q "from-main" || fail "worktree does not read the committed rows"
$LORE memory add --body "from-worktree" >/dev/null || fail "memory add in the worktree failed"
$LORE memory search "from" 2>&1 | grep -q "from-worktree" || fail "worktree does not read its own write"
git status --porcelain .lore/data | grep -q "memories/" || fail "the worktree's row file is not in the worktree"
cd "$TMP/main"
$LORE memory search "from" 2>&1 | grep -q "from-worktree" && fail "the worktree's write leaked into main before a merge"
[ "$(find .lore/data -type f | sort | cksum)" = "$MAIN_FILES" ] || fail "main's row files changed"
pass SC-27
