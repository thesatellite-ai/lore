# Trash, restore from git and purge

**In short:** a note's file in `.lore/data` is the note. When the file disappears (deleted, reverted, or missing on the branch you switched to), the note is gone from lists too, and git puts it back. A note you delete with a lore command goes to lore's trash instead, and `lore trash restore` brings it back. If the whole `.lore/data` folder is gone, git brings your notes back. And when you really want old archived notes gone for good, `lore trash purge --archived-before` deletes them so no old clone brings them back.

## What it is and when you want it

The files in `.lore/data` are the truth; the database follows them ([How notes travel with git](how-notes-travel-with-git.md)). So when a file goes away, the row goes away too. That is right when a teammate deleted the note on purpose, but it can surprise you. Three tools cover the cases:

| Tool | Use it when |
|---|---|
| `git checkout HEAD -- <file>` | a note's file disappeared and you want it back |
| `lore trash list`, `lore trash show <id>`, then `lore trash restore <id>` | a note was deleted (or folded by `lore sync merge-rows`) with a lore command |
| `lore batch list`, then `lore batch undo <bat_id>` | a bulk command (`delete --where`, a bulk edit, an import) did more than you wanted |
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
would purge 1 row
  mem_05e9…4603 (memories, repo)
dry run: nothing written
$ lore trash purge --archived-before 0d
ERROR: [E_NEEDS_CONFIRM] trash: purge removes every row archived before 0d for good; only tombstones stay
  hint: ask the person, then re-run with --confirm
(exit 26)
$ lore trash purge --archived-before 0d --confirm
purged 1 row
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

Purge empties each purged row's file to a deletion marker (only its id and `"_deleted": true` stay) and records the ids in `.lore/data/_purged.json`. When an old clone or an old branch still has one of those files, lore refuses to bring it back. Until you commit, `git checkout HEAD -- <file>` still brings a purged note back. Comments on a purged row, its commit links and every knowledge link to or from it are purged with it, live or in the trash, so none is left pointing at nothing; `--dry-run` lists them first.

## Step 4 — Delete a note with lore and bring it back

A note deleted with a lore command (`lore memory delete`, `lore rule delete`, …) goes to lore's trash. Its file stays in `.lore/data` with the delete recorded in it, so the delete travels with your next commit like any other change. `--reason` is stored with it; every verb that moves a row to the trash takes it (also `lore task view delete`, `lore task template delete`, `lore link remove`, `lore vars rm`, `lore project delete` and `lore secrets allow rm`), and with the policy `delete.require_reason` on, a delete without one is refused with `E_REASON_REQUIRED`. Rows that belong to the deleted row go into the trash with it in one batch (a task's sub-tasks, a mission's or tasklist's tasks); comments and links on it are their own rows and stay where they are, still pointing at it, so `lore comment list --on-id <id>` keeps listing them and a restore finds the thread intact; only a purge of the row (`lore trash purge <id> --confirm`) takes its comments and links with it. `lore trash list` shows each trashed row with when, by whom, why and the batch it belongs to; `lore trash show <id>` prints the whole row (sensitive values masked); `lore trash restore <id>` brings it back with the same id:

<!-- example: sync#delete-and-restore -->
```console
# alice, in website
$ lore memory add --body 'The demo is on Friday at 10.'
✓ mem_2537…6bbb
  scope: [master]
$ lore memory delete mem_2537…6bbb --reason 'the demo moved' --yes
✓ mem_2537…6bbb moved to the trash (restore: lore trash restore mem_2537…6bbb)
$ lore trash list
mem_2537…6bbb  memories  "The demo is on Friday at 10."  2026-11-02 10:07  by act_54fe…28f2  reason "the demo moved"  bat_b69e…5ed7
mem_3b62…e913  memories  "send the invoice reminder before the dem…"  2026-11-02 10:05  by act_28f8…03df  reason "merged into mem_4c59…6c38"  bat_f147…5139
2 rows in the trash
$ lore trash show mem_2537…6bbb
memories mem_2537…6bbb  "The demo is on Friday at 10."  in the trash
  deleted   2026-11-02 10:07 by act_54fe…28f2 (store repo)
  reason    the demo moved
  batch     bat_b69e…5ed7
  body: The demo is on Friday at 10.
  created_by_actor_id: act_54fe…28f2
  deleted_at: 2026-11-02T10:00:00.000000002Z
  deleted_batch: bat_b69e…5ed7
  deleted_by: act_54fe…28f2
  deleted_reason: the demo moved
  kind: retrieved
  project_id: prj_7a67…9504
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000003Z
$ lore trash restore mem_2537…6bbb
restored 1 row (batch bat_b69e…5ed7)
  mem_2537…6bbb (memories, repo)
$ lore memory list | grep demo
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
mem_2537…6bbb [retrieved] The demo is on Friday at 10.
```
<!-- /example -->

`lore trash list` takes filters: `--type memories`, `--by @handle`, `--since 7d`, `--reason <text>`, `--search <text>`, `--batch <bat_id>`, `--to <store>`, and `--limit` / `--page` for long lists. `--from-history` lists rows that were purged or deleted for good, read from git history; `lore trash show <id> --from-history` shows such a row's last content, but it never comes back.

## Step 5 — Undo a bulk delete

Every command that changes many rows at once (`delete --where`, task bulk edits, metadata bulk set, imports, `lore secrets move`) records a **batch** with an id `bat_…`. Alice deletes every memory whose body holds "deploys", checks the trash with `lore trash stats`, finds the batch with `lore batch list`, looks at it with `lore batch show`, and undoes it with `lore batch undo`. Undo restores a delete batch, puts back the rows an edit batch changed, and moves the rows a create batch added to the trash:

<!-- example: sync#bulk-delete-and-undo -->
```console
# alice, in website
$ lore memory add --body 'Staging deploys at 9.' >/dev/null && lore memory add --body 'Production deploys on Tuesday.' >/dev/null
$ lore memory delete --where 'body~=deploys' --dry-run
would delete 2 memory(s): mem_0aae…a61f Staging deploys at 9.; mem_6a86…ed8c Production deploys on Tuesday.; (nothing written, --dry-run)
$ lore memory delete --where 'body~=deploys' --yes
✓ 2 memory(s) (--where) moved to the trash (batch bat_4f89…b64b; restore: lore trash restore bat_4f89…b64b)
$ lore trash stats
3 rows in the trash (0 dependents), 0 past trash.retention_days
by type
  memories  3
by store
  repo  3
by deleter
  act_54fe…28f2  2
  act_28f8…03df  1
tombstones (kept forever)
  repo  1
$ lore batch list
batch bat_4f89…b64b  delete  memory delete  by act_54fe…28f2  2026-11-02 10:07  2 rows (repo 2)
batch bat_b69e…5ed7  delete  memory delete  by act_54fe…28f2  2026-11-02 10:07  1 row (repo 1); restore 1, trash 1
$ lore batch show bat_4f89…b64b
batch bat_4f89…b64b  delete  memory delete  by act_54fe…28f2  2026-11-02 10:07  2 rows (repo 2)
  kind: delete; trash 2
  trash mem_0aae…a61f (memories, repo)
  trash mem_6a86…ed8c (memories, repo)
$ lore batch undo bat_4f89…b64b --yes
✓ restored 2 rows of batch bat_4f89…b64b
$ lore memory list | grep deploys
mem_0aae…a61f [retrieved] Staging deploys at 9.
mem_6a86…ed8c [retrieved] Production deploys on Tuesday.
```
<!-- /example -->

Preview a bulk delete with `--dry-run`; it writes nothing, so it never asks. A batch's `rows` count is the rows it wrote, each once: the single-row delete restored in Step 4 stays `1 rows (repo 1)`, and its list line adds `; restore 1, trash 1`. `lore batch list` filters with `--kind task|meta|delete|resource|import|secrets`, `--by` and `--since`; `lore batch show --all` lists every row instead of a sample. `lore trash restore <bat_id>` also brings back a whole delete batch.

## Try it — the old local safety copies

`lore trash list --local` lists this machine's safety copies, the ones an older lore (v0.1.17) kept in its `lore.db` when sync removed a file. On a repo that only ever used 0.1.18 the list is empty; on an older one, `lore trash restore --local <n>` puts copy `<n>` back:

<!-- example: sync#local-copies only=1 -->
```console
# alice, in website
$ lore trash list --local
this machine only: local safety copies of rows sync removed (never shared)
· trash is empty
```
<!-- /example -->

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
