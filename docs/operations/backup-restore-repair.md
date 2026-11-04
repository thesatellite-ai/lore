# Back up, restore and repair

**In short:** lore keeps your notes in one database file. `lore backup` makes a safe copy of it, `lore restore` puts a copy back, and `lore repair` brings a damaged file back to life. Your shared notes also live as plain files in git, so even a lost database can be rebuilt.

## What it is and when you want it

The database file is `.lore/lore.db` in your repo. It is not committed to git (it is listed in `.gitignore`), so it only exists on your machine. Think of it as your notebook; a backup is a photocopy you keep in a drawer.

There are two layers of safety:

1. **Backups** in `.lore/backups/`. lore makes one by itself before it changes the database in a big way (the files named `…-pre-sync.sqlite`), and you can make one any time with `lore backup`.
2. **The plain files in `.lore/data`.** Every shared note (rules, memories, decisions, tasks and more) is also written there and committed with your code. After a restore or repair, lore reads them again and fills the gaps.

You want this page when you are about to try something risky, when you deleted something by mistake, or when `lore doctor` says BROKEN.

## Before you start

- lore is set up in the repo (`lore init`).
- Commit your `.lore/data` changes first when you can. Then git holds a second copy of everything shared.

## Step 1 — Make a backup

<!-- ds:block id=backup-default-path-hccatu2u -->
`lore backup` copies the database while lore keeps running (it uses the online copy built into SQLite, the database engine lore uses, so a half-written copy cannot happen). Without `--out` it writes `.lore/backups/<date-time>.sqlite`. Here Alice (an agency developer) names the copy so she can find it again:

<!-- example: ops#backup -->
```console
# alice, in website
$ lore backup --out .lore/backups/before-buttons.sqlite
✓ ~/work/website/.lore/backups/before-buttons.sqlite (1441792 bytes)
$ ls .lore/backups
20261102-100000.000000000-pre-sync.sqlite
before-buttons.sqlite
```
<!-- /example -->

The other file is the copy lore made by itself when the repo was set up.

## Step 2 — Restore a backup

Alice adds a rule, then decides to go back to the copy she made before. `lore restore` refuses to overwrite the database unless you add `--confirm`:

<!-- example: ops#restore only=1,2,3,4 -->
```console
# alice, in website
$ lore rule add --body 'Buttons are blue'
✓ rul_c0f3…b5b8 
  scope: [master] severity: must
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
rul_c0f3…b5b8 [must/always] Buttons are blue
$ lore restore .lore/backups/before-buttons.sqlite
ERROR: [E_INVALID_INPUT] refusing to overwrite DB without --confirm
(exit 20)
$ lore restore .lore/backups/before-buttons.sqlite --confirm
✓ restored from .lore/backups/before-buttons.sqlite
  previous DB preserved at: ~/work/website/.lore/lore.db.broken.20261102-100000
  next command re-syncs .lore/data preferring: db
```
<!-- /example -->

The old database is not thrown away: lore keeps it next to the new one as `.lore/lore.db.broken.<date-time>`, so you can undo the restore by restoring that file.

## Step 3 — Let the next command catch up with git

The restored database and the files in `.lore/data` may now disagree. The first lore command after a restore settles that. By default the restored database wins (`--prefer db`): rules that exist only in the files are removed, and the files are written again from the database:

<!-- example: ops#restore only=5,6 -->
```console
# alice, in website
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
rul_c0f3…b5b8 [must/always] Buttons are blue
$ git status --short
?? .lore/data/rules/rul_7a67…9504.json
?? .lore/data/rules/rul_c0f3…b5b8.json
```
<!-- /example -->

"Buttons are blue" is gone, as Alice wanted. `git status` shows the files lore rewrote; review them with `git diff` and commit. You can choose another winner when you restore:

| `--prefer` | Who wins when the database and `.lore/data` disagree |
|---|---|
| `db` (default) | The restored database. Rows only in the files are removed. |
| `files` | The committed files. Rows only in the database are moved to the sync trash (`lore sync trash`), so you can still get them back. |
| `newest` | The newer version of each row. The other version is kept aside under `lore sync conflicts`. |

## Step 4 — Repair a damaged database

If the database file gets damaged (a disk problem, a copy that stopped halfway), `lore doctor` says BROKEN. Your shared notes are safe: commands such as `lore rule list` read them from the files in `.lore/data`, so they still work:

<!-- example: ops#repair only=1,2 -->
```console
# alice, in website
$ lore doctor
Status: BROKEN
Binary: 0.1.0-dev
[db]
  ok:       false
  wal_size: 0 bytes
[identity]
  resolved: human:alice@agency.example
  source:   git config
  stable:   yes
Errors:
  - E_DB_CORRUPT: DB file is corrupt (pragmas: apply "PRAGMA auto_vacuum=INCREMENTAL": file is not a database (26)); recover with `lore repair --tier=2 --confirm` (latest backup) — shared rows also rebuild from .lore/data
(exit 2)
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
rul_c0f3…b5b8 [must/always] Buttons are blue
```
<!-- /example -->

`lore repair` has three tiers, from gentle to drastic. Like restore, it needs `--confirm`. Today only tier 2 is a real way back; use the rebuild in Step 5 when you have no good backup:

<!-- ds:block id=repair-tiers-79kbzg3d lines=1-7 -->
| Tier | What it is meant to do | Today |
|---|---|---|
| `--tier=1` | Rebuild the database from whatever SQLite can still read in the damaged file. | Not built yet: it stops with `E_NOT_IMPLEMENTED`. |
| `--tier=2` (default) | Restore the newest file in `.lore/backups/`. | Works. This is what doctor suggests. |
| `--tier=3` | Start a new, empty database. | Avoid: it leaves an empty file that every command reports as `E_DB_CORRUPT` (see Pitfalls). Use Step 5 instead. |

Tier 2 in action, then the next command reads `.lore/data` again:

<!-- example: ops#repair only=3,4 -->
```console
# alice, in website
$ lore repair --tier=2 --confirm
✓ restored from ~/work/website/.lore/backups/20261102-100000.000000000-pre-sync.sqlite
  previous (broken) DB at: ~/work/website/.lore/lore.db.broken.20261102-100001
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
rul_c0f3…b5b8 [must/always] Buttons are blue
```
<!-- /example -->

Tier 1, for the record:

<!-- example: ops#repair-tier-1 -->
```console
# alice, in website
$ lore repair --tier=1 --confirm
ERROR: [E_NOT_IMPLEMENTED] repair tier 1 not implemented in v0.1
  hint: use --tier=2 (recommended) or --tier=3
(exit 1)
```
<!-- /example -->

## Step 5 — Rebuild from the files in git

When there is no backup you trust, move the database out of the way. Shared notes still list straight from `.lore/data`; the next `lore sync` builds a new database and fills it from those files. It is like reprinting a notebook from the photocopies you filed:

<!-- example: ops#rebuild-from-data -->
```console
# alice, in website
$ mv .lore/lore.db .lore/lore.db.old && rm -f .lore/lore.db-wal .lore/lore.db-shm
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
rul_c0f3…b5b8 [must/always] Buttons are blue
$ lore sync
· already in sync
$ lore doctor | head -1
Status: HEALTHY
```
<!-- /example -->

Only shared notes come back this way. Things lore keeps on your machine only are not in `.lore/data` and start empty: the [audit log](audit-log.md), test questions for [`lore bench`](bench.md), [secret patterns](secrets-and-pii.md#add-your-own-patterns), the [plugin list and database settings](plugins-and-db-config.md). Delete `.lore/lore.db.old` once you are happy.

## Step 6 — Check the result

Run `lore doctor` again, and `lore sync conflicts` to see whether anything was kept aside. Shared notes such as rules and memories are always taken from the files, so they never conflict here; if another row does, `lore sync resolve <n> --take kept` accepts what lore kept:

<!-- example: ops#after-repair lines=8 -->
```console
# alice, in website
$ lore doctor
Status: HEALTHY
Binary: 0.1.0-dev
[db]
  ok:       true
  wal_size: 4152 bytes
[sync]
  baselined:        true
  pending exports:  0
…
$ lore sync conflicts | head -3
· no conflicts
```
<!-- /example -->

## Pitfalls

- **The `.lore/lore.db.broken.*` files are not ignored by git.** After a restore or repair, `git status` lists them as new files. Do not commit them (they are whole databases, with private rows in them); move or delete them once you are happy with the result.
- **Repair tier 2 takes the newest backup, whatever made it.** That can be one of lore's own `…-pre-sync.sqlite` copies, not the one you made. If you want a specific copy, use `lore restore <file> --confirm` instead.
- **`lore repair --tier=3` is a dead end today.** It creates an empty file, says "run `lore init`", but `lore init` then refuses (`E_ALREADY_INITIALIZED`) and every other command reports `E_DB_CORRUPT`. Get out of it by deleting `.lore/lore.db` and running any lore command (Step 5).
- **Backups contain everything in the database,** including rows that never go to git (for example a note confirmed with `--yes` as not a secret). Keep `.lore/backups/` out of shared folders; it is in `.gitignore` already.
- **A restore can remove rows from `.lore/data`.** With the default `--prefer db`, rows that are only in the files are deleted from the files. Read `git diff` before you commit.
- **Backups are per machine.** The database is not in git, so a backup on your laptop does not help a teammate. Shared notes reach them through `.lore/data`.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [Conflicts and duplicates in git sync](../sync/conflicts-and-duplicates.md)
- [The audit log](audit-log.md)
- [Operations index](README.md)
