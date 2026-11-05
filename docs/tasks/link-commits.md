# Link commits to tasks

**In short:** a commit is one saved change in git. Linking a commit to a task records "this task was done in that commit", so later you can ask "which commit fixed the pricing page?" or "what was this commit for?". `lore task done --commit` also lets lore notice when your change reaches `main`.

## What it is and when you want it

Git knows what changed. lore knows why: the task, the decision or the memory behind the change. A **link** joins the two. You want links when you look back at old work: a reviewer reads a commit and wants the task behind it, or you want to see every commit that belonged to one task.

There are two ways to connect a task with code:

- `lore link add --entity <id> --commit <sha>` records a link from any row (a task, a decision, a mission) to a commit. `lore link list` and `lore commit-show` read the links back.
- `lore task done <id> --commit <sha>` finishes the task and remembers the commit. lore then watches for that commit on `main` and marks the task *merged* (the change is in `main`) when it gets there. `lore task merged` marks it by hand when lore cannot see the merge.

A *sha* is the id git gives a commit, like `05513aa…`. `git log --oneline` shows the short form; `git rev-parse HEAD` prints the full sha of your last commit.

Alice (an agency developer) fixes the price on the client's pricing page in `github.com/client/website`.

## Before you start

- lore set up in a repo with a remote, and the basics from [Tasks](tasks.md).

## Step 1 — Commit and push your change

Commit as usual. If you finished another task earlier, lore has already written it into `main` on GitHub, so pull before you push (see [Tasks › Pitfalls](tasks.md#pitfalls)):

<!-- example: tasks#push-after-done -->
```console
# alice, in website
$ git commit -qm 'Fix the Pro plan price on the pricing page'
$ git push -q origin main
To /srv/git/github.com/client/website
 ! [rejected]        main -> main (non-fast-forward)
error: failed to push some refs to '/srv/git/github.com/client/website'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart. If you want to integrate the remote changes,
hint: use 'git pull' before pushing again.
hint: See the 'Note about fast-forwards' in 'git push --help' for details.
(exit 1)
$ git pull -q --rebase origin main && git push -q origin main
```
<!-- /example -->

## Step 2 — Link the commit to the task

`--commit HEAD` means "my last commit". lore fills in the commit's message, author and time from git.

<!-- example: tasks#link -->
```console
# alice, in website
$ lore link add --entity tsk_2ad3…f891 --commit HEAD
✓ linked 9b40ce71 → tasks/tsk_2ad3…f891  (cml_6a86…ed8c)
  message: Fix the Pro plan price on the pricing page
$ lore link list --entity tsk_2ad3…f891
cml_6a86…ed8c  9b40ce71 → tasks/tsk_2ad3…f891 — Fix the Pro plan price on the pricing page
```
<!-- /example -->

## Step 3 — Find the rows behind a commit

`lore commit-show` lists every row linked to one commit. Give it the **full** sha, which `git rev-parse HEAD` prints:

<!-- example: tasks#commit-show -->
```console
# alice, in website
$ lore commit-show $(git rev-parse HEAD)
Commit 9b40ce71af28 — 1 linked entities:
  tasks/tsk_2ad3…f891
$ lore commit-show HEAD
(no entities linked to this commit)
```
<!-- /example -->

The second command shows the trap: `commit-show` does not understand `HEAD` or a short sha, and says nothing is linked.

## Step 4 — Finish the task with its commit

`lore task done --commit` records the commit on the task. When that commit is on `main`, lore marks the task merged, and `lore task list --status done` says how it found out (`found by: ancestry` means "the commit is part of main's history"):

<!-- example: tasks#done-with-commit -->
```console
# alice, in website
$ lore task done tsk_2ad3…f891 --commit $(git rev-parse HEAD) --no-lesson
✓ tsk_2ad3…f891 done (commit 9b40ce7); waiting for merge
lore live: (live, pushed)
lore live: landed on main (05513aa); leaves live once main is fetched
$ lore sync
· already in sync
lore live: 1 row(s) left live (main holds them)
$ lore task list --status done
lore: tsk_2ad3…f891 merged 10:04 (found by: ancestry, 9b40ce7) but it could not be recorded: write tsk_2ad3…f891: signals: prepare companion: [E_STALE] engine: tsk_2ad3…f891 kept changing while it was merged: [E_STALE] live row tasks/tsk_2ad3…f891 changed since it was read: [E_STALE] store: row changed since it was read; the next lore command retries
✓ tsk_7a67…9504 Send the invoice reminder before Friday's demo [high] due:2026-11-06
✓ tsk_2ad3…f891 Fix the typo on the pricing page [medium]  ✓ done · not merged yet
```
<!-- /example -->

## Step 5 — Mark a task merged by hand

Sometimes lore cannot see the merge: the task has no commit, or the change reached `main` in a way lore cannot trace. `lore task merged` records it yourself:

<!-- example: tasks#merged -->
```console
# alice, in website
$ lore task add 'Shrink the hero image'
✓ tsk_4f89…b64b — Shrink the hero image [medium]
lore live: (live, pushed)
$ lore task done tsk_4f89…b64b --no-lesson
✓ tsk_4f89…b64b done
lore live: (live, pushed)
lore live: landed on main (dfd8dc8); leaves live once main is fetched
$ lore task merged tsk_4f89…b64b
✓ tsk_4f89…b64b merged (marked by hand, 10:05)
lore live: landed on main (cf563f3); leaves live once main is fetched
```
<!-- /example -->

Merged matters when another task waits for this one with `--after <id>:merged` (see [Team › How it works › Done, then merged](../team/how-it-works.md#done-then-merged)).

## Step 6 — Remove a link

`lore link list` shows every link with its id (`cml_…`). `lore link remove` deletes one; lore asks first, and without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`:

<!-- example: tasks#link-remove -->
```console
# alice, in website
$ lore link list
cml_6a86…ed8c  9b40ce71 → tasks/tsk_2ad3…f891 — Fix the Pro plan price on the pricing page
$ lore link remove cml_6a86…ed8c
ERROR: [E_NEEDS_CONFIRM] engine: run link remove?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore link remove cml_6a86…ed8c --yes
✓ link cml_6a86…ed8c removed
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore trash --local` (undo: `lore trash restore --local <id>`)
```
<!-- /example -->

## Try it

The shortest path from a commit to its task, using the commands above:

<!-- example: tasks#link only=1 -->
```console
# alice, in website
$ lore link add --entity tsk_2ad3…f891 --commit HEAD
✓ linked 9b40ce71 → tasks/tsk_2ad3…f891  (cml_6a86…ed8c)
  message: Fix the Pro plan price on the pricing page
```
<!-- /example -->

## Pitfalls

- **`lore commit-show HEAD` finds nothing.** It only matches the full 40-character sha. Use `lore commit-show $(git rev-parse HEAD)`, or `lore link list --commit <full sha>`.
- **A rebase changes the sha.** `git pull --rebase` gives your commit a new id. Link the commit after you push, as in Step 1 to 2, so the link points at the commit that is really on `main`.
- **`lore task show` does not print the commit.** Its `commit:` line is the task's commitment (how sure it is), not a git commit. Use `lore link list --entity <id>` or `lore task list --status done`.
- **Links are saved like other rows.** `link add` writes a file to `.lore/data` ("not committed — commit them with your change"); commit it, or your teammates will not see the link.

## Related

- [Tasks](tasks.md): add, finish and order tasks.
- [Team commands › lore task done](../team/commands.md#lore-task-done) and [lore task merged](../team/commands.md#lore-task-merged): `--pr` and how merges are found in a team.
- [Run log](run-log.md): a step-by-step record of how the work was done.
