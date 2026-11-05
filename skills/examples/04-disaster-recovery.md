# Example: My DB is broken

**In short:** lore's local database `.lore/lore.db` is only a cache of the files in `.lore/data`. If it breaks (for example after a power cut), move it out of the way and lore builds a new one from the files. Keep `.lore/data` committed and nothing shared can be lost.

**Scenario:** Bob (an agency developer) opens his project and `lore doctor` reports broken. The power went out while lore was writing.

## Triage

```bash
lore doctor
# Status: BROKEN
# Errors:
#   - E_DB_CORRUPT: DB file is corrupt (...)
lore rule list    # shared notes still list: they are read from .lore/data
```

## Rebuild the database from the files

```bash
mv .lore/lore.db .lore/lore.db.old && rm -f .lore/lore.db-wal .lore/lore.db-shm
lore sync
# lore sync: built .lore/lore.db from .lore/data (… rows)
lore search rebuild
# ✓ rebuilt the search index of … stores
lore doctor | head -1
# Status: HEALTHY
```

Delete `.lore/lore.db.old` once you are happy. Only machine-local things (the audit trail on this machine, sessions) start empty.

## If `.lore/data` itself is gone

The files are in git:

```bash
git checkout HEAD -- .lore/data
lore sync
```

A file deleted in an earlier commit comes back from the commit before it (`git log --diff-filter=D -- .lore/data/<table>/<id>.json`).

## Prevention

Commit `.lore/data` with your code. That is the backup: every clone holds a full copy, and any of them can rebuild its database.

```bash
git status --porcelain -- .lore/data   # empty = everything is in git
```
