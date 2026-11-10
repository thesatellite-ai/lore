# A damaged cache: rebuilt from your files

**In short:** your notes are the files in `.lore/data`, committed with your code. The cache `.lore/lore-cache.db` is only a local copy that lore builds from them, so when it is damaged lore moves it aside and builds a new one by itself, and when it is lost the next command builds it again. `lore search rebuild` rebuilds the search index from the same files. There is nothing to back up by hand.

## What it is and when you want it

The cache file is `.lore/lore-cache.db` in your repo. It is not committed to git (it is listed in `.gitignore`), so it only exists on your machine. Think of it as an index card box built from the notebook: the notebook is `.lore/data`, which git keeps for you and your team.

You want this page when lore says it moved a damaged cache aside, when the cache file was deleted, or when search answers look out of date.

## Before you start

- lore is set up in the repo (`lore init`).
- Commit your `.lore/data` changes when you can. Then git holds every shared note, and any clone can rebuild its cache from them.

## Step 1 — A damaged cache fixes itself

Alice (an agency developer) finds her cache damaged (here its first bytes were overwritten). The next lore command notices, moves the broken file into `.lore/backups/`, rebuilds the cache from `.lore/data` and carries on; `lore doctor` says HEALTHY:

<!-- example: ops#damaged-database -->
```console
# alice, in website
$ lore rule list
lore: the cache ~/work/website/.lore/lore-cache.db was damaged; moved it to ~/work/website/.lore/backups/lore-cache.db.corrupt-20261102T100000Z and rebuilt it from the row files (nothing lost)
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
$ lore doctor | grep '^Status'
Status: HEALTHY
```
<!-- /example -->

Nothing is lost: every note is in `.lore/data`. Delete the copy in `.lore/backups/` once you are happy; it is only kept so you can look at it.

## Step 2 — Rebuild from the files in git

You can also delete the cache yourself, for example to free space or to rule it out while you investigate something. The next command builds a new one from `.lore/data`:

<!-- example: ops#rebuild-from-data -->
```console
# alice, in website
$ rm -f .lore/lore-cache.db .lore/lore-cache.db-wal .lore/lore-cache.db-shm
$ lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
rul_7a67…9504 [must/always] Never put passwords in the code
$ lore doctor | grep '^Status'
Status: HEALTHY
```
<!-- /example -->

The cache holds nothing of its own. What lore keeps on your machine only (the [audit log](audit-log.md), sessions, this machine's settings) lives in `~/.lore/state/machine.db`, not in the repo's cache, so a rebuild does not touch it.

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

## Try it

Delete the cache on purpose and let the next command build it again. `ls .lore` shows `lore-cache.db` gone, then back after one `lore doctor`:

<!-- example: ops#try-cache -->
```console
# alice, in website
$ rm -f .lore/lore-cache.db .lore/lore-cache.db-wal .lore/lore-cache.db-shm && ls .lore
data
lock
state
$ lore doctor | grep '^Status'
Status: HEALTHY
$ ls .lore
data
lock
lore-cache.db
state
```
<!-- /example -->

Nothing was lost: every note is in `.lore/data`, and the cache is only a faster copy of it.

## Pitfalls

- **`.lore/backups/` stays on your machine.** It is in `.gitignore`, and a moved-aside cache is a whole database, so delete it once you are happy with the result rather than copying it anywhere.
- **Uncommitted notes still come back.** The rebuild reads the files in your working tree, committed or not. Only a file you deleted, or never wrote, is gone; get a deleted one back from git (see [Trash, restore from git and purge](../sync/trash-and-purge.md)).
- **The cache is per machine.** It is not in git, so a teammate never needs your copy. Shared notes reach them through `.lore/data`.
- **There is no backup, restore or repair command.** Older lore versions had them for `.lore/lore.db`; in 0.1.18 the cache is rebuilt from the row files, so nothing else is needed. A v0.1.17 `.lore/lore.db` left in a repo is read only by `lore migrate run`.

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [Conflicts and duplicates in git sync](../sync/conflicts-and-duplicates.md)
- [The audit log](audit-log.md)
- [Operations index](README.md)
