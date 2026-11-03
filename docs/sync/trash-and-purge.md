# Trash, export and purge

**In short:** when a note's file disappears (deleted, reverted, or missing on the branch you switched to), lore removes the note from your database but keeps a copy in a local trash, so you can put it back. If the whole `.lore/data` folder is gone, `lore sync export --all` writes it again from the database. And when you really want old archived notes gone for good, `lore sync purge` deletes them so no old clone brings them back.

## What it is and when you want it

The files in `.lore/data` are the truth; the database follows them ([How notes travel with git](how-notes-travel-with-git.md)). So when a file goes away, the row goes away too. That is right when a teammate deleted the note on purpose, but it can surprise you. Three tools cover the cases:

| Tool | Use it when |
|---|---|
| `lore sync trash` and `lore sync trash restore <number>` | a note disappeared and you want it back |
| `lore sync export --all` | the `.lore/data` folder was deleted, or files went missing, and your database still has the rows |
| `lore sync purge --archived-before <date-or-age> --confirm` | archived notes should be deleted for good, everywhere |

Archiving (`lore memory archive <id>`, `lore rule archive <id>`, ...) is the normal way to retire a note: it stays in its file with an archive date and stops showing up. Purge is the step after that, for notes that must not come back.

## Before you start

- A repo set up with lore. Here Alice (an agency developer) works in `github.com/client/website`.
- Purge changes shared history for everyone: agree on it with your team and run it on main.

## Step 1 — Get back a note whose file disappeared

Alice deletes a memory's file (here with `git rm`, as a teammate's commit or a revert would). The next command moves the row to the trash and says how to undo it. `lore sync trash` lists what is there; restore one by its number, and the next pass writes its file again:

<!-- example: sync#trash -->
```console
# alice, in website
$ git rm -q .lore/data/memories/mem_54fe…28f2.json
$ lore memory list
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore sync trash` (undo: `lore sync trash restore <id>`)
mem_d431…313a [retrieved] Send the invoice reminder before the demo on Friday.
$ lore sync trash
#4 memories/mem_54fe…28f2  2026-11-02T10:05:45Z  (file deleted)
#3 memories/mem_05e9…4603  2026-11-02T10:02:50Z  (file deleted)
#2 rules/rul_c0f3…b5b8  2026-11-02T10:02:00Z  (file deleted)
#1 memories/mem_05e9…4603  2026-11-02T10:02:00Z  (file deleted)
$ lore sync trash restore 4
lore sync: exported 1
✓ restored trash entry #4
$ lore memory list
mem_54fe…28f2 [retrieved] The client replies faster on WhatsApp than by email.
mem_d431…313a [retrieved] Send the invoice reminder before the demo on Friday.
$ git add -A && git status --short
```
<!-- /example -->

The other entries in that list are rows that left the database when Alice switched branches earlier ([Branches: peek and promote](branches-peek-promote.md)). They come back by themselves when you switch back, because their files are still on that branch.

## Step 2 — Rebuild `.lore/data` from the database

If `.lore/data` is deleted, lore does not empty your database. It warns on every command and points at the fix. `lore sync export --all` writes every row file again; when nothing was lost, git shows no change at all:

<!-- example: sync#export -->
```console
# alice, in website
$ rm -rf .lore/data && lore sync status
baselined:        true
data dir present: false
tracked files:    8
pending exports:  0
open conflicts:   0
trash rows:       3
uncommitted:      9
live rows:        0 (_root)
lore sync: ~/work/website/.lore/data is missing (a branch without lore data, or deleted): lore.db is left untouched; run `lore sync export --all` to recreate it
$ lore sync export --all
lore sync: ~/work/website/.lore/data is missing (a branch without lore data, or deleted): lore.db is left untouched; run `lore sync export --all` to recreate it
lore sync: exported 8
✓ 8 file(s) written
$ git status --short
```
<!-- /example -->

`export --all` is also the step to run after a tool changed `lore.db` without going through lore, or after a lore upgrade asks you to rewrite files in a new format; commit the result on main.

## Step 3 — Purge archived notes for good

Alice archives a memory and commits that. Later the team decides archived notes older than a cutoff should be gone. `--archived-before` takes a date (`2026-01-31`) or an age back from now (`90d`, `720h`; `0d` means "archived before now"). Preview with `--dry-run`; the real run needs `--confirm`, because purge cannot be undone by git alone:

<!-- example: sync#purge -->
```console
# alice, in website
$ lore memory archive mem_54fe…28f2 && git commit -qam 'lore: archive whatsapp memory'
✓ mem_54fe…28f2 archived
lore sync: exported 1
lore sync: 1 file(s) in .lore/data not committed — commit them with your change
$ lore sync purge --archived-before 0d --dry-run
would purge 1 archived row(s) archived before 2026-11-02T10:06:30Z
$ lore sync purge --archived-before 0d
ERROR: [E_INVALID_INPUT] purge is durable; pass --confirm (or --dry-run to preview)
(exit 1)
$ lore sync purge --archived-before 0d --confirm
lore sync: removed files 1
✓ purged 1 row(s)
$ cat .lore/data/_purged.json
{
  "_v": 1,
  "ids": [
    "mem_54fe…28f2"
  ]
}
$ git add -A && git status --short && git commit -qm 'lore: purge archived rows' && git push -q origin main
A  .lore/data/_purged.json
D  .lore/data/memories/mem_54fe…28f2.json
```
<!-- /example -->

Purge deletes the row files and records the ids in `.lore/data/_purged.json`. When an old clone or an old branch still has one of those files, lore refuses to bring it back. The rows go to the local trash first, so the person who ran purge can still restore one before committing.

## Pitfalls

- **The trash is local.** It lives in your `lore.db` only. A teammate cannot restore a row from your trash; they would need the file from git history (`git log -- .lore/data/memories/<id>.json`).
- **Restore numbers are per machine.** `#4` is the number in your trash list, not a row id.
- **`lore <kind> delete` is a hard delete.** It removes the file at once. If another branch edited that note, git shows a normal "deleted on one side, changed on the other" conflict. Prefer `archive`.
- **Purge only touches archived rows.** A row that is not archived is never purged, however old it is.
- **Run purge on main and commit `_purged.json`.** Purged on a feature branch only, the rows come back from main when the branch merges.

## Related

- [How notes travel with git](how-notes-travel-with-git.md)
- [Conflicts and duplicates](conflicts-and-duplicates.md): rows dropped by `merge-rows` also land in the trash.
- [Troubleshooting (teams)](../team/troubleshooting.md)
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
