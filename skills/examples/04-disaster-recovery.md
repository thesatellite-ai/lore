# Example: My DB is broken

**Scenario:** Dave opens his project and `lore doctor` reports broken. Power loss happened mid-write.

## Triage

```bash
lore doctor --json | jq
# {
#   "schema_version": 1,
#   "status": "broken",
#   "db_ok": false,
#   "errors": [
#     "quick_check: page 3: row 2 missing from index project_name_unique",
#     ...
#   ]
# }
```

## Tier 1 — try WAL replay

```bash
lore repair --tier=1 --confirm
# Rebuild from WAL fragments using SQLite .recover
# ...
# ✓ Recovered 142 rows from WAL
```

If that worked: `lore doctor` should return healthy. Done.

## Tier 2 — restore latest backup

```bash
ls .lore/backups/
# 20260510-142359.sqlite
# 20260510-180412.sqlite      ← most recent

lore repair --tier=2 --confirm
# Using backup: .lore/backups/20260510-180412.sqlite
# ✓ DB restored
# ✓ doctor: HEALTHY
```

Tier 2 is the **default** (`lore repair --confirm` without `--tier`).

## Tier 3 — bootstrap empty + re-learn from CLAUDE.md

If you have no backups but you DID commit `CLAUDE.md`:

```bash
lore repair --tier=3 --confirm
# ✓ Empty DB created
# (Note: all rows lost — only CLAUDE.md committed to git survives)

lore learn-from docs   # re-ingest from CLAUDE.md
lore learn list --json | jq -r '.data[].id' | xargs -I{} lore learn promote {} --target=memories
lore render
# ✓ DB rebuilt from CLAUDE.md
```

## Prevention

Add `lore backup` to your daily routine:

```bash
# In a cron or pre-push hook
lore backup
ls -la .lore/backups/ | head
```

Or run it before risky operations:

```bash
lore backup && lore learn-from docs --paths='/path/to/imported/*.md'
```

## What if even the backup is broken?

The backup files are full SQLite files — open one with `sqlite3` directly:

```bash
sqlite3 .lore/backups/20260510-142359.sqlite
sqlite> .schema memories
sqlite> SELECT id, body FROM memories;
```

Worst case: dump rows manually and feed them back via `lore memory add`.
