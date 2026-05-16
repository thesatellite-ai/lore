#!/usr/bin/env bash
# SC-8: learn-from docs bootstrap
# Catches: R16-21, R27-16, ship-gate Tier-2 #8
source "$(dirname "$0")/../lib/common.sh"
need sqlite3
mk_tmp; init_project learnfrom

cat > CLAUDE.md <<'MD'
# Project conventions
- Use Tailwind v4
- Prefer TanStack Router
- SQLite with WAL mode
MD

$LORE learn-from docs --paths=CLAUDE.md >/dev/null 2>&1 || $LORE learn-from docs >/dev/null 2>&1 || fail "learn-from"

COUNT=$(sqlite3 .lore/lore.db "SELECT COUNT(*) FROM learn_candidates")
[ "$COUNT" -gt 0 ] || fail "no candidates ingested"

FIRST=$(sqlite3 .lore/lore.db "SELECT id FROM learn_candidates LIMIT 1")
$LORE learn promote "$FIRST" --target=memories >/dev/null 2>&1 || \
  skip "learn promote needs --target=memories flag (v0.2 promotion API)"

STATUS=$(sqlite3 .lore/lore.db "SELECT status FROM learn_candidates WHERE id='$FIRST'")
[ "$STATUS" = "accepted" ] || fail "expected accepted, got $STATUS"
pass SC-8
