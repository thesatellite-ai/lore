# Conflicts and duplicates

**In short:** when two people change the same note, lore merges it field by field, so a change to the rule's strength and a change to its text both survive. Only when both people rewrote the same text does git stop and ask you to choose, like any code conflict. This page also shows how to fold the same fact written twice into one note, and how to repair a repo where two people set up lore separately.

## What it is and when you want it

Each note is one small file with one field per line ([How notes travel with git](how-notes-travel-with-git.md)). git alone merges files line by line, and two edits of the same note usually touch neighbouring lines (both change `updated_at`, the time of the last change), so plain git would call almost every such merge a conflict.

lore installs a **merge driver**: a small helper git calls to combine two edits of the same note (see [Hooks and the merge driver](hooks-and-merge-driver.md)). It merges a note field by field:

<!-- ds:block id=sync-strategyfor-db9rgsqn -->
| Both sides changed... | Result |
|---|---|
| different fields (one the strength, one the text) | both changes kept, no conflict |
| the same field to the same value | that value |
| the same choice-type field (status, priority, an archive date, a link to another row) | the side changed last wins |
| the same free-text field (title, body) | a real conflict: both versions are written into the value, and you choose |
| a list that works like a set (labels, approvers) | additions from both sides kept, removals honoured |

Text stays a loud conflict on purpose: quietly keeping one of two rewritten rules would lose someone's work.

You want this page after a `git pull` or merge that touched `.lore/data`, when `lore sync status` shows conflicts, or when the same note appears twice.

## Before you start

- A repo set up with lore, cloned by two people (here Alice and Bob, agency developers, in `github.com/client/website`).
- The merge driver is set up in each clone. lore does this on its first command in a clone.

## Step 1 — Two edits of different fields merge by themselves

Alice makes "Buttons are blue" a must-have rule (the `severity` field). At the same time Bob adds "links are green" to its text. Bob pulls: git calls lore's driver, and the rule ends up with both changes:

<!-- example: sync#merge-by-field only=5,6,7,8 -->
```console
# alice, in website
$ lore rule edit rul_b4f4…a5b0 --severity must && git commit -qam 'lore: buttons are a must' && git push -q origin main
lore: wrote ~/work/website/.lore/data/rules/rul_b4f4…a5b0.json
✓ rul_b4f4…a5b0 updated

# bob, in website
$ lore rule edit rul_b4f4…a5b0 --body 'Buttons are blue, links are green.' && git commit -qam 'lore: link colour'
lore: wrote ~/work/website/.lore/data/actors/act_28f8…03df.json
lore: wrote ~/work/website/.lore/data/rules/rul_b4f4…a5b0.json
✓ rul_b4f4…a5b0 updated
$ git pull --no-rebase origin main
From /srv/git/github.com/client/website
 * branch            main       -> FETCH_HEAD
Auto-merging .lore/data/rules/rul_b4f4…a5b0.json
Merge made by the 'ort' strategy.
$ lore rule list
rul_162d…9666 [must/always] Run the tests before you push.
rul_76bd…3f10 [must/always] Never put passwords in the code.
rul_b4f4…a5b0 [must/always] Buttons are blue, links are green.
```
<!-- /example -->

## Step 2 — The same text changed on both sides

Now Alice writes "dark blue" and Bob writes "light blue". Alice pulls and gets a conflict. lore puts both versions inside the `body` value, in git's usual `<<<<<<< ours / ======= / >>>>>>> theirs` form, so the file is still valid JSON:

<!-- example: sync#same-text-conflict only=2,3,4,5 -->
```console
# alice, in website
$ lore rule edit rul_b4f4…a5b0 --body 'Buttons are dark blue.' && git commit -qam 'lore: dark blue'
lore: wrote ~/work/website/.lore/data/rules/rul_b4f4…a5b0.json
✓ rul_b4f4…a5b0 updated

# bob, in website
$ lore rule edit rul_b4f4…a5b0 --body 'Buttons are light blue.' && git commit -qam 'lore: light blue' && git push -q origin main
lore: wrote ~/work/website/.lore/data/rules/rul_b4f4…a5b0.json
✓ rul_b4f4…a5b0 updated

# alice, in website
$ git pull -q --no-rebase origin main
lore merge-driver: .lore/data/rules/rul_b4f4…a5b0.json needs a manual merge (markers are inside the JSON values)
Auto-merging .lore/data/rules/rul_b4f4…a5b0.json
CONFLICT (content): Merge conflict in .lore/data/rules/rul_b4f4…a5b0.json
Automatic merge failed; fix conflicts and then commit the result.
(exit 1)
$ grep body .lore/data/rules/rul_b4f4…a5b0.json
  "body": "<<<<<<< 2026-11-02T10:04:00.000Z/0000/act_28f8…03df\nButtons are light blue.\n=======\nButtons are dark blue.\n>>>>>>> 2026-11-02T10:03:55.000Z/0000/act_54fe…28f2",
```
<!-- /example -->

The commit is refused while the markers are there. The pre-commit hook (a small script git runs just before each commit) checks for them. Settle it with a normal lore edit, which writes a clean file, then commit the merge:

<!-- example: sync#same-text-conflict only=7,8,9 -->
```console
# alice, in website
$ git commit -qam 'merge'
ERROR: [E_INVALID_INPUT] refusing to commit broken lore files: .lore/data/rules/rul_b4f4…a5b0.json (conflict markers in a value)
  hint: fix the file by hand (a row file is one JSON object; settle a conflict with the row's edit command), or restore it with `git checkout -- <path>`; then commit again
(exit 1)
$ lore rule edit rul_b4f4…a5b0 --body 'Buttons are blue.'
lore: invalid-file store repo: .lore/data/rules/rul_b4f4…a5b0.json: [E_INVALID_INPUT] rowfs: "rules/rul_b4f4…a5b0.json": field body holds unresolved conflict markers; resolve them in the file and commit; lists and search leave it out until it is settled; show prints it with both sides (lore sync conflicts)
lore: wrote ~/work/website/.lore/data/rules/rul_b4f4…a5b0.json
✓ rul_b4f4…a5b0 updated
$ git add -A && git commit -qm 'merge: buttons are blue' && git push -q origin main
```
<!-- /example -->

You can also edit the value in the file by hand; the next lore command reads it in.

Or let lore pick one side for you. `lore sync conflicts` lists every row that still holds conflict markers, numbered: first the live rows (tasks shared before they land), then the row files of this repo that a git merge left with markers (with the file path; for those `kept` reads "both" until you choose). `lore sync status` counts the conflicted files too. `lore sync resolve <number> --take kept` keeps the first side of every marker block in that row (your edit; for a git merge, the branch you are on), `--take other` keeps the second (the edit that met it; for a git merge, the branch you merged in). It only ever chooses between texts already there, never writes new text:

- A row file is written back in place and the local cache refreshed; you still `git add` and commit the merge yourself, lore never commits it for you.
- A live row gets a new live version, pushed like any other live edit.
- If the result would still hold markers (nested or hand-made blocks), or a file would not be a valid row afterwards, resolve refuses with `E_INVALID_INPUT` and writes nothing: edit that row by hand. An unknown number is `E_NOT_FOUND`.

## Step 3 — The file and your database both changed

For shared notes (rules, memories, tasks and the other core notes) the file is the only truth: lore reads them straight from `.lore/data`, and the cache under `~/.lore/cache/` is just a local copy. So an edit of the file, by hand or by git, simply wins, and there is no second version to conflict with it:

<!-- example: sync#file-and-database-conflict only=2,3,4,5 -->
```console
# alice, in website
$ sed -i 's/than email/than email or phone/' .lore/data/memories/mem_05e9…4603.json
$ lore memory list
lore: picked up a hand edit of memories mem_05e9…4603 (body): checked and stamped with your name and the time in ~/work/website/.lore/data/memories/mem_05e9…4603.json; commit it with your code
mem_05e9…4603 [retrieved] The client replies faster on WhatsApp than email or phone.
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
$ lore sync
· already in sync
$ lore sync conflicts
· no conflicts
```
<!-- /example -->

Lore 0.1.18 keeps no second copy in its cache that could disagree with the file, so this kind of conflict cannot happen any more. The only conflicts left are conflict markers inside a row, from Step 2 or from a live row two people edited at once; `lore sync conflicts` and `lore sync resolve` settle those.

## Step 4 — The same fact written twice

Alice and Bob each wrote "Send the invoice reminder before the demo on Friday" on their own machines. Two ids, one fact. After the merge, both rows are there; `lore memory list` (or `lore search`) shows them side by side. `lore sync merge-rows <table> --keep <id> --drop <id>` folds them: every reference to the dropped row (links, comments, tags, `${…}` references in other rows) moves to the row you keep, and the dropped row goes to the trash in one batch, so `lore trash restore` can bring it back (`lore trash list` shows it with the reason `merged into <kept id>`). Edit the kept row first if it should carry some of the other's text. Deleting the duplicate with its own command also works, but leaves references to it dangling (`lore lint` reports them).

<!-- example: sync#duplicates only=2,3,4,5,6,7,8 -->
```console
# alice, in website
$ lore memory add --body 'Send the invoice reminder before the demo on Friday.' && git add -A && git commit -qm 'lore: invoice' && git push -q origin main
lore: wrote ~/work/website/.lore/data/memories/mem_4c59…6c38.json
✓ mem_4c59…6c38
  scope: [master]

# bob, in website
$ lore memory add --body 'send the invoice reminder before the demo on friday.' && git add -A && git commit -qm 'lore: invoice (bob)'
lore: wrote ~/work/website/.lore/data/memories/mem_3b62…e913.json
✓ mem_3b62…e913
  scope: [master]
$ git pull -q --no-rebase origin main
$ lore memory list | grep -i invoice
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
mem_3b62…e913 [retrieved] send the invoice reminder before the demo on friday.
$ lore sync merge-rows memories --keep mem_4c59…6c38 --drop mem_3b62…e913
lore: wrote ~/work/website/.lore/data/memories/mem_3b62…e913.json
✓ merged mem_3b62…e913 into mem_4c59…6c38 (0 row(s) re-pointed; mem_3b62…e913 is in the trash, batch bat_f147…5139)
$ lore memory list
mem_05e9…4603 [retrieved] The client replies faster on WhatsApp than email.
mem_4c59…6c38 [retrieved] Send the invoice reminder before the demo on Friday.
$ lore trash list
mem_3b62…e913  memories  "send the invoice reminder before the dem…"  2026-11-02 10:05  by act_28f8…03df  reason "merged into mem_4c59…6c38"  bat_f147…5139
1 row in the trash
```
<!-- /example -->

## Step 5 — Two people set up lore on separate branches

If two people ran `lore init` on separate branches before either was merged, each branch has its own project id (the name lore gives a project inside its files). When the branches meet, `_meta.json` conflicts and there are two project rows. `lore sync fix-projects` keeps one (the one `_meta.json` names, or the one you give with `--keep`) and points every row at it. Run it on main after the merge, then commit:

<!-- example: sync#fix-projects only=4,5,6,7,8,9 -->
```console
# alice, in app
$ git fetch -q origin && git merge --no-edit origin/bob-setup
Auto-merging .lore/data/_meta.json
Auto-merging .lore/data/policies/pol_d431…313a.json
ERROR: [E_LEAK_BLOCKED] blocked, this commit would share internal content with the client repo:
  commit .lore/data/repos/rep_77cb…76c4.json row of another project
  hint: this repository holds rows of a second project (two `lore init`s merged?): run `lore sync fix-projects` to keep one project and re-point its rows, then commit again
Not committing merge; use 'git commit' to complete the merge.
(exit 1)
$ lore project list
prj_d191…45c2  app  https://github.com/client2/app
prj_b4d3…11aa  app  https://github.com/client2/app
$ lore sync fix-projects --dry-run
2 project row(s): would keep prj_d191…45c2 and merge prj_b4d3…11aa (1 row(s) to re-point)
$ lore sync fix-projects
lore: wrote ~/work/app/.lore/data/repos/rep_77cb…76c4.json
lore: wrote ~/work/app/.lore/data/projects/prj_b4d3…11aa.json
✓ kept prj_d191…45c2, merged prj_b4d3…11aa (1 row(s) re-pointed)
$ lore project list
prj_d191…45c2  app  https://github.com/client2/app
$ git add -A && git commit -qm 'lore: one project' && git push -q origin main
```
<!-- /example -->

## Try it — check the notes after a merge with `lore lint`

Merges, folds and deletes can leave a note pointing at a row that is gone. `lore lint` reads every attached store once and reports such problems, each with the row and the command that fixes it; it never changes anything. Here a rule points at a memory with `${row:<id>}`, the memory is deleted, and lint reports `dangling_ref`; restoring the memory clears it:

<!-- example: sync#lint only=2,3,4,5,6 -->
```console
# alice, in website
$ lore memory add --body 'The client signs off every release on Fridays.'
lore: wrote ~/work/website/.lore/data/memories/mem_9a34…b418.json
✓ mem_9a34…b418
  scope: [master]
$ lore rule add --body 'Ship only after the client sign-off, see ${row:mem_9a34…b418}.'
lore: wrote ~/work/website/.lore/data/rules/rul_bfed…eeea.json
✓ rul_bfed…eeea
  scope: [master] severity: must
$ lore memory delete mem_9a34…b418 --yes
lore: wrote ~/work/website/.lore/data/memories/mem_9a34…b418.json
✓ mem_9a34…b418 moved to the trash (restore: lore trash restore mem_9a34…b418)
$ lore lint
dangling_ref rul_bfed…eeea (repo): body: ${row:mem_9a34…b418} points at no live row
  fix: lore edit the row, or restore mem_9a34…b418 (lore trash restore mem_9a34…b418)
1 finding(s)
$ lore trash restore mem_9a34…b418 >/dev/null && lore lint --code dangling_ref
lore: wrote ~/work/website/.lore/data/memories/mem_9a34…b418.json
lint: no findings
```
<!-- /example -->

The other findings lint reports are unknown `${…}` schemes (`unknown_scheme`), variables known by an old name or shadowed by another store (`var_old_name`, `var_shadowed`, `var_shareable_differs`), task parent cycles (`parent_cycle`), tasks with very long time spans or many time entries (`long_span`, `time_entries_many`), rows imported twice from one source (`duplicate_import`), metadata keys no registry defines (`meta_unknown_key`) and stored secrets (`secret_found`). `--code <code>` keeps only some codes (an unknown code is refused with the list of known ones), `--json` prints the findings for a script, and `lore lint --context` runs the separate checks for what renders into agent files (no summary, too long, untagged, near duplicates among pinned entries, …). lint exits 0 even when it finds something, so in CI test the count from `--json`.

## Pitfalls

- **GitHub's merge button does not run lore's driver.** On a pull request, two edits of the same note show as a conflict even when lore could merge them. Merge main into your branch locally (`git pull origin main`), or install [the GitHub workflow](github-workflow.md).
- **A clone that never ran lore has no driver.** If someone merges in a fresh clone before running any lore command, git falls back to line merging. Run `lore sync` once after cloning.
- **Conflict markers in a value are imported, not hidden.** Until you settle them, `lore rule list` shows the markers in the text, and every command repeats the warning. That is deliberate, so the conflict is not forgotten.
- **lore does not look for duplicates in memories for you.** (`lore lint --context` flags near duplicates among pinned entries only.) Find them with `lore search` or `lore memory list`; "We use Stripe" and "Payments go through Stripe" are the same fact to you, not to lore. Then fold them with `lore sync merge-rows`.
- **Resolve numbers are per machine.** `#1` in `lore sync conflicts` is a local number, not a row id; it means nothing on a teammate's machine. Run `lore sync conflicts` again before each `resolve`: the numbers shift once a conflict is settled.
- **Resolve works on this repo only.** Files of an attached store's clone are settled by `lore sync`'s own merge, not by `lore sync resolve`.

## Related

- [Hooks and the merge driver](hooks-and-merge-driver.md): what the driver is and how it is installed.
- [The GitHub workflow](github-workflow.md): merge lore data on pull requests.
- [Trash, restore from git and purge](trash-and-purge.md): where dropped rows go.
- [How it works › Merging two versions of a row](../team/how-it-works.md#merging-two-versions-of-a-row): the same rules for live rows.
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
