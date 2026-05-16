# Example: Start using lore in an existing project

**Scenario:** Alice has a Go monorepo at `~/projects/api-server`. She wants Claude Code to actually remember what she teaches it. She has an old `CLAUDE.md` she's been hand-editing.

## Session transcript

```
Alice: Let's set up lore for this project.

Claude: I see ~/projects/api-server has an existing CLAUDE.md.
        I'll initialize lore, ingest your existing markdown,
        then render a fresh CLAUDE.md from the structured store.
```

```bash
cd ~/projects/api-server
lore init --non-interactive
# ✓ lore initialized at /Users/alice/projects/api-server
#     project_id: prj_019e...
#     name:       api-server
#     identity:   human:alice@example.com (git config)

lore learn-from docs
# Ingested 12 candidates from CLAUDE.md
#   review: lore learn list

lore learn list --json | jq -r '.data[] | "\(.id) \(.body[0:60])"'
# lcd_019e... "Use Tailwind v4 for styling..."
# lcd_019e... "All errors wrapped with fmt.Errorf..."
# lcd_019e... "Database is PostgreSQL 16 with..."
# ...
```

```
Alice: They all look good, accept them all.
```

```bash
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done
# ✓ 12 candidates accepted

lore render
# ✓ CLAUDE.md written (2,847 bytes, 12 memories, 0 rules)
```

```
Claude: Done. CLAUDE.md is now rendered from the lore DB.
        Next session I'll see the same context.
        Going forward, when you correct me, I'll capture it
        with `lore rule add` or `lore memory add` and
        re-render so it persists.
```

## What just happened

- `.lore/lore.db` created (SQLite, WAL mode, auto-vacuum)
- 12 facts extracted from existing CLAUDE.md → `memories` table
- New CLAUDE.md generated deterministically from the DB
- `.gitignore` updated to exclude `.lore/lore.db` (the DB is user-local)

## What to commit

```bash
git status
# new file:    CLAUDE.md           ← commit this
# modified:    .gitignore          ← commit this
# new file:    .lore/           ← ignored (per .gitignore)
```

Only `CLAUDE.md` and `.gitignore` enter version control. The DB is per-developer.
