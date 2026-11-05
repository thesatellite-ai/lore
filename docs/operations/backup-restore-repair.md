# A damaged database: rebuild it from your files

**In short:** your notes are the files in `.lore/data`, committed with your code. The database `.lore/lore.db` is only a local copy that lore builds from them, so when it is damaged or lost you move it out of the way and lore builds a new one. `lore search rebuild` rebuilds the search index from the same files. There is nothing to back up by hand.

## What it is and when you want it

The database file is `.lore/lore.db` in your repo. It is not committed to git (it is listed in `.gitignore`), so it only exists on your machine. Think of it as an index card box built from the notebook: the notebook is `.lore/data`, which git keeps for you and your team.

You want this page when `lore doctor` says BROKEN with `E_DB_CORRUPT`, when the database file was deleted, or when search answers look out of date.

## Before you start

- lore is set up in the repo (`lore init`).
- Commit your `.lore/data` changes when you can. Then git holds every shared note, and any clone can rebuild its database from them.

## Step 1 — See what still works

Alice (an agency developer) finds her database damaged (here its first bytes were overwritten). Shared notes are read from `.lore/data`, so they still list; `lore doctor` says BROKEN and names the damaged file:

<!-- example: ops#damaged-database keep=^(rul_|Status|\s+ok:|\s+-\sE_DB_CORRUPT) -->
```console
# alice, in website
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
$ lore doctor
Status: BROKEN
  …
  ok:       false
  …
  - E_DB_CORRUPT: DB file is corrupt (pragmas: apply "PRAGMA auto_vacuum=INCREMENTAL": file is not a database (26)); recover with `lore repair --tier=2 --confirm` (latest backup) — shared rows also rebuild from .lore/data
  …
```
<!-- /example -->

## Step 2 — Rebuild from the files in git

Move the database out of the way. The next `lore sync` builds a new one and fills it from `.lore/data`:

<!-- example: ops#rebuild-from-data -->
```console
# alice, in website
$ mv .lore/lore.db .lore/lore.db.old && rm -f .lore/lore.db-wal .lore/lore.db-shm
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
$ lore sync
· already in sync
$ lore doctor | head -1
Status: HEALTHY
```
<!-- /example -->

Only shared notes come back this way. Things lore keeps on your machine only are not in `.lore/data` and start empty: the [audit log](audit-log.md) and this machine's settings in the database. Delete `.lore/lore.db.old` once you are happy.

## Step 3 — Rebuild the search index and check the result

`lore search rebuild` rebuilds the search index of every attached store from its row files. Then run `lore sync conflicts` to see whether anything was kept aside:

<!-- example: ops#search-rebuild -->
```console
# alice, in website
$ lore search rebuild
✓ rebuilt the search index of 2 stores (repo, machine)
$ lore sync conflicts | head -3
· no conflicts
```
<!-- /example -->

## Pitfalls

- **The `.lore/lore.db.old` file is not ignored by git.** Do not commit it (it is a whole database, with private rows in it); delete it once you are happy with the result.
- **Uncommitted notes still come back.** The rebuild reads the files in your working tree, committed or not. Only a file you deleted, or never wrote, is gone; get a deleted one back from git (see [Trash, restore from git and purge](../sync/trash-and-purge.md)).
- **The database is per machine.** It is not in git, so a teammate never needs your copy. Shared notes reach them through `.lore/data`.
- **There is no repair command.** Older lore versions had one, and an old doctor line may still name it; in 0.1.18 the cache is rebuilt from the row files, so follow Step 2 instead.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [Conflicts and duplicates in git sync](../sync/conflicts-and-duplicates.md)
- [The audit log](audit-log.md)
- [Operations index](README.md)
