# Trash, restore from git and purge

**In short:** a note's file in `.lore/data` is the note. When the file disappears (deleted, reverted, or missing on the branch you switched to), the note is gone from lists too, and git puts it back. A note you delete with a lore command goes to lore's trash instead, and `lore trash restore` brings it back. If the whole `.lore/data` folder is gone, git brings your notes back. And when you really want old archived notes gone for good, `lore trash purge --archived-before` deletes them so no old clone brings them back.

## What it is and when you want it

The files in `.lore/data` are the truth; the database follows them ([How notes travel with git](how-notes-travel-with-git.md)). So when a file goes away, the row goes away too. That is right when a teammate deleted the note on purpose, but it can surprise you. Three tools cover the cases:

| Tool | Use it when |
|---|---|
| `git checkout HEAD -- <file>` | a note's file disappeared and you want it back |
| `lore trash list`, then `lore trash restore <id>` | a note was deleted (or folded by `lore sync merge-rows`) with a lore command |
| `git checkout HEAD -- .lore/data` | the `.lore/data` folder was deleted, or files went missing |
| `lore trash purge --archived-before <date-or-age> --confirm` | archived notes should be deleted for good, everywhere |

Archiving (`lore memory archive <id>`, `lore rule archive <id>`, ...) is the normal way to retire a note: it stays in its file with an archive date and stops showing up. Purge is the step after that, for notes that must not come back.

## Before you start

- A repo set up with lore. Here Alice (an agency developer) works in `github.com/client/website`.
- Purge changes shared history for everyone: agree on it with your team and run it on main.

## Step 1 — Get back a note whose file disappeared

Alice deletes a memory's file (here with `git rm`, as a teammate's commit or a revert would). The file is the note, so the memory is gone from `lore memory list` at once. It is not in lore's trash, which only holds notes deleted with a lore command; git has the file, so `git checkout` brings it back and the note is there again:

<!-- example: sync#trash -->
```console
# alice, in website
$ git rm -q .lore/data/memories/mem_05e9…4603.json
$ lore memory list
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
$ git checkout HEAD -- .lore/data/memories/mem_05e9…4603.json
$ lore memory list
mem_05e9…4603 [retrieved] The client replies faster on WhatsApp than email or phone.
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
$ git add -A && git status --short
```
<!-- /example -->

When the deleting commit is already older, take the file from the commit before it: `git log --diff-filter=D -- .lore/data/memories/<id>.json` names that commit. Notes missing because you switched branches ([Branches: peek and promote](branches-peek-promote.md)) come back by themselves when you switch back, because their files are still on that branch.

## Step 2 — Get back a deleted `.lore/data`

If `.lore/data` is deleted, the notes are gone from lists until the files are back; lore never rebuilds them from `lore.db`. The files are the truth, so they come back from git; when nothing was lost, git shows no change at all:

<!-- example: sync#export -->
```console
# alice, in website
$ rm -rf .lore/data && lore sync status
meta quarantined: 0
live rows:        0 (_root)
conflicted files: 0
refused pushes:   0
$ git checkout -q HEAD -- .lore/data && git status --short
```
<!-- /example -->

## Step 3 — Purge archived notes for good

Alice archives a memory and commits that. Later the team decides archived notes older than a cutoff should be gone. `--archived-before` takes a date (`2026-01-31`) or an age back from now (`90d`, `720h`; `0d` means "archived before now"). Preview with `--dry-run`; the real run needs `--confirm`, because purge cannot be undone by git alone:

<!-- example: sync#purge -->
```console
# alice, in website
$ lore memory archive mem_05e9…4603 && git commit -qam 'lore: archive whatsapp memory'
✓ mem_05e9…4603 archived
$ lore trash purge --archived-before 0d --dry-run
would purge 1 rows
  mem_05e9…4603 (memories, repo)
dry run: nothing written
$ lore trash purge --archived-before 0d
ERROR: [E_NEEDS_CONFIRM] trash: purge removes every row archived before 0d for good; only tombstones stay
  hint: ask the person, then re-run with --confirm
(exit 26)
$ lore trash purge --archived-before 0d --confirm
purged 1 rows
  mem_05e9…4603 (memories, repo)
tombstones are kept forever; the old content is still in git history (rotate any leaked secret)
lore live: (live, pushed)
$ cat .lore/data/_purged.json
{
  "_v": 1,
  "ids": [
    "mem_05e9…4603"
  ]
}
$ git add -A && git status --short && git commit -qm 'lore: purge archived rows' && git push -q origin main
A  .lore/data/_purged.json
M  .lore/data/memories/mem_05e9…4603.json
```
<!-- /example -->

Purge empties each purged row's file to a deletion marker (only its id and `"_deleted": true` stay) and records the ids in `.lore/data/_purged.json`. When an old clone or an old branch still has one of those files, lore refuses to bring it back. Until you commit, `git checkout HEAD -- <file>` still brings a purged note back.

## Pitfalls

- **A removed file is not in the trash.** `lore trash list` shows notes deleted with a lore command (`lore <kind> delete`, `lore sync merge-rows`), and `lore trash restore <id>` brings one back with the same id. A file removed with git comes back from git. Prefer `archive` for notes you may still want to read.
- **`lore trash list --local` is only for copies from v0.1.17.** It lists the safety copies an older lore kept in this machine's `lore.db` when a file disappeared; 0.1.18 adds no new ones. Numbers there (`#4`) are per machine, not row ids.
- **`lore sync trash` is the old spelling.** It still works in 0.1.18, prints a note that names `lore trash --local`, and goes away in the next release.
- **Purge only touches archived rows.** A row that is not archived is never purged, however old it is.
- **Run purge on main and commit `_purged.json`.** Purged on a feature branch only, the rows come back from main when the branch merges.

## Related

- [How notes travel with git](how-notes-travel-with-git.md)
- [Conflicts and duplicates](conflicts-and-duplicates.md): rows dropped by `merge-rows` also land in the trash.
- [Troubleshooting (teams)](../team/troubleshooting.md)
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
