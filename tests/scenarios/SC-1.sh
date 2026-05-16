#!/usr/bin/env bash
# SC-1: Fresh project bootstrap → first memory → render
# Catches: R14-1, R21-61, R22-41, ship-gate Tier-2 #1, #2, #4
#
# Run via: LORE=/path/to/bin/aicoder bash tests/scenarios/SC-1.sh

set -e
trap 'cleanup' EXIT

LORE="${LORE:-aicoder}"

cleanup() {
    if [ -n "${TMP:-}" ] && [ -d "$TMP" ]; then
        rm -rf "$TMP"
    fi
}

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

TMP=$(mktemp -d)
cd "$TMP"
git init -q
git remote add origin https://github.com/test/myproject.git

# 1. Init creates .lore/lore.db and adds to .gitignore
$LORE init --non-interactive --name=myproject || fail "init failed"
[ -f .lore/lore.db ] || fail ".lore/lore.db not created"
grep -q "\.lore/aicoder\.db" .gitignore || fail ".gitignore missing lore.db"

# 2. memory add succeeds
$LORE memory add "Use Tailwind v4" || fail "memory add failed"

# 3. render writes CLAUDE.md
$LORE render || fail "render failed"
[ -f CLAUDE.md ] || fail "CLAUDE.md not written"
grep -q "Use Tailwind v4" CLAUDE.md || fail "CLAUDE.md missing memory"

# 4. search returns the memory via JSON output
RESULTS=$($LORE memory search "Tailwind" --json)
echo "$RESULTS" | jq -e '.results[0].body | test("Tailwind")' > /dev/null || fail "search did not return memory"

echo "PASS: SC-1"
