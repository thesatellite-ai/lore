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
✓ linked 05513aa2 → tasks/tsk_2ad3…f891  (cml_4f89…b64b)
  message: Fix the Pro plan price on the pricing page
$ lore link list --entity tsk_2ad3…f891
cml_4f89…b64b  05513aa2 → tasks/tsk_2ad3…f891 — Fix the Pro plan price on the pricing page
```
<!-- /example -->

## Step 3 — Find the rows behind a commit

`lore commit-show` lists every row linked to one commit. Give it the full sha, a unique short sha of 7 or more characters (any case, as `git log --oneline` prints it), or any revision git understands, such as `HEAD` or `HEAD~1`:

<!-- example: tasks#commit-show -->
```console
# alice, in website
$ lore commit-show $(git rev-parse HEAD)
Commit 05513aa29b78 — 1 linked entities:
  tasks/tsk_2ad3…f891
$ lore commit-show HEAD
Commit 05513aa29b78 — 1 linked entities:
  tasks/tsk_2ad3…f891
$ lore commit show $(git rev-parse HEAD)
Commit 05513aa29b78 — 1 linked entities:
  tasks/tsk_2ad3…f891
```
<!-- /example -->

`lore commit show` (with a space) is the same command under the `commit` group and prints the same list. A short sha that two linked commits share is refused with `E_AMBIGUOUS_ID`, which lists both shas; add characters until one is left. A word that is neither a sha nor a git revision is refused with `E_INVALID_INPUT`.

## Step 4 — Finish the task with its commit

`lore task done --commit` records the commit on the task. When that commit is on `main`, lore marks the task merged, and `lore task list --status done` says how it found out (`found by: ancestry` means "the commit is part of main's history"):

<!-- example: tasks#done-with-commit -->
```console
# alice, in website
$ lore task done tsk_2ad3…f891 --commit $(git rev-parse HEAD) --no-lesson
✓ tsk_2ad3…f891 done (commit 05513aa); waiting for merge
lore live: (live, pushed)
lore live: landed on main (dfd8dc8); leaves live once main is fetched
$ lore sync
lore live: 1 row(s) left live (main holds them)
lore live: pushed waiting live writes
$ lore task list --status done
✓ tsk_2ad3…f891 merged 10:04 (found by: ancestry, 05513aa)
✓ tsk_7a67…9504 Send the invoice reminder before Friday's demo [high] due:2026-11-06
✓ tsk_2ad3…f891 Fix the typo on the pricing page [medium]  ✓ done · merged 10:04
lore live: landed on main (cf563f3); leaves live once main is fetched
```
<!-- /example -->

## Step 5 — Mark a task merged by hand

Sometimes lore cannot see the merge: the task has no commit, or the change reached `main` in a way lore cannot trace. `lore task merged` records it yourself:

<!-- example: tasks#merged -->
```console
# alice, in website
$ lore task add 'Shrink the hero image'
✓ tsk_77cb…76c4 — Shrink the hero image [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)
$ lore task done tsk_77cb…76c4 --no-lesson
✓ tsk_77cb…76c4 done
lore live: (live, pushed)
lore live: landed on main (df3a55c); leaves live once main is fetched
$ lore task merged tsk_77cb…76c4
✓ tsk_77cb…76c4 merged (marked by hand, 10:05)
lore live: landed on main (6d00426); leaves live once main is fetched
```
<!-- /example -->

Merged matters when another task waits for this one with `--after <id>:merged` (see [Team › How it works › Done, then merged](../team/how-it-works.md#done-then-merged)).

## Step 6 — Remove a link

`lore link list` shows every link with its id (`cml_…`). `lore link remove` deletes one; lore asks first, and without a terminal it stops with `E_NEEDS_CONFIRM` until you add `--yes`:

<!-- example: tasks#link-remove -->
```console
# alice, in website
$ lore link list
cml_4f89…b64b  05513aa2 → tasks/tsk_2ad3…f891 — Fix the Pro plan price on the pricing page
$ lore link remove cml_4f89…b64b
ERROR: [E_NEEDS_CONFIRM] link remove cml_4f89…b64b?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore link remove cml_4f89…b64b --yes
✓ link cml_4f89…b64b removed
```
<!-- /example -->

## Try it

The shortest path from a commit to its task, using the commands above:

<!-- example: tasks#link only=1 -->
```console
# alice, in website
$ lore link add --entity tsk_2ad3…f891 --commit HEAD
✓ linked 05513aa2 → tasks/tsk_2ad3…f891  (cml_4f89…b64b)
  message: Fix the Pro plan price on the pricing page
```
<!-- /example -->

## Pitfalls

- **`HEAD` means the commit you are on now.** `lore commit show HEAD` resolves `HEAD` in the folder you run it from; after another commit, the linked one is `HEAD~1`. `lore link list --commit` still matches only the full sha.
- **A rebase changes the sha.** `git pull --rebase` gives your commit a new id. Link the commit after you push, as in Step 1 to 2, so the link points at the commit that is really on `main`.
- **`lore task show` does not print the commit.** Its `commit:` line is the task's commitment (how sure it is), not a git commit. Use `lore link list --entity <id>` or `lore task list --status done`.
- **A squash merge keeps the link through the `Lore-Task` trailer.** While you hold a claim on a task, lore's `commit-msg` hook adds a `Lore-Task: <id>` line to the end of your commit messages (only when the task row lives in the repo you commit to). A squash or rebase merge that keeps that line lets lore find the merge on `main` even though the commit sha changed; without it, use Step 5. See [How it works](../team/how-it-works.md).
- **Links are saved like other rows.** `link add` writes a file to `.lore/data` ("not committed — commit them with your change"); commit it, or your teammates will not see the link.

## Related

- [Tasks](tasks.md): add, finish and order tasks.
- [Team commands › lore task done](../team/commands.md#lore-task-done) and [lore task merged](../team/commands.md#lore-task-merged): `--pr` and how merges are found in a team.
- [Run log](run-log.md): how long each try at the work took.
