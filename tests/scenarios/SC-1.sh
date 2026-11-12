#!/usr/bin/env bash
# SC-1: Fresh project bootstrap → first memory → render
# Catches: R14-1, R21-61, R22-41, ship-gate Tier-2 #1, #2, #4
#
# 0.1.18 layout: rows are files under .lore/data (committed), the search
# cache is under the lore home, ~/.lore/cache/repo/<folder>-<hash>.db
# (never in the work tree, rebuilt from the row files);
# v0.1.17's .lore/lore.db is gone. With no tool file wired, render writes
# the AGENTS.md pointer; --target claude_code writes CLAUDE.md's @import.
#
# Run via: LORE=/path/to/bin/lore bash tests/scenarios/SC-1.sh
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; isolate_home
git init -q
git remote add origin https://github.com/test/myproject.git

# 1. Init writes the project's row files and gitignores the cache
$LORE init --non-interactive --name=myproject >/dev/null || fail "init failed"
[ -f .lore/data/_meta.json ] || fail ".lore/data/_meta.json not created"
ls .lore/data/projects/prj_*.json >/dev/null 2>&1 || fail "project row file not created"
[ -f "$(repo_cache)" ] || fail "the repo cache $(repo_cache) not created"
[ ! -e .lore/lore-cache.db ] || fail "init created a cache in the work tree"
[ ! -e .lore/lore.db ] || fail "init created the retired .lore/lore.db"
git check-ignore -q .lore/data/_meta.json && fail "git ignores .lore/data (rows must be committed)"

# 2. memory add succeeds and writes a row file
$LORE memory add --body "Use Tailwind v4" >/dev/null || fail "memory add failed"
grep -rq "Use Tailwind v4" .lore/data/memories/ || fail "memory row file missing"

# 3. render writes .lore/LORE.md (must-rules pinned) and a pointer:
#    AGENTS.md when no tool is wired, CLAUDE.md's @import for claude_code
$LORE rule add --severity=must --body "Never force-push main" >/dev/null || fail "rule add failed"
$LORE render >/dev/null || fail "render failed"
grep -q "Never force-push main" .lore/LORE.md || fail "LORE.md missing must rule"
grep -q "\.lore/LORE\.md" AGENTS.md || fail "AGENTS.md missing pointer"
[ ! -e CLAUDE.md ] || fail "render wrote CLAUDE.md though no tool is wired"
$LORE render --target claude_code >/dev/null || fail "render --target claude_code failed"
[ -f CLAUDE.md ] || fail "CLAUDE.md not written"
grep -q "@.lore/LORE.md" CLAUDE.md || fail "CLAUDE.md missing @import pointer"

# 4. search returns the memory via JSON output
RESULTS=$($LORE memory search "Tailwind" --json)
# python3 rather than jq: jq is not installed on every dev box, and this
# assertion must run everywhere instead of being skipped.
echo "$RESULTS" | python3 -c 'import json,sys; sys.exit(0 if "Tailwind" in json.load(sys.stdin)["data"][0]["row"]["body"] else 1)' \
    || fail "search did not return memory"

pass SC-1
