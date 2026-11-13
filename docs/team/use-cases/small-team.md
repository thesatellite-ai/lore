# Small team in one repo

**In short:** Alice and Bob (agency developers) work in one repo on different branches. When Bob gives Alice a task and runs `lore sync`, she sees it after her own `lore sync`, without waiting for a merge. When she finishes it, her next `lore sync` saves the task on `main`.

## The situation

Alice and Bob work in one repo, `github.com/agency/billing`, each on their own feature branch. When Bob gives Alice a task, Alice must see it now, not after Bob's branch merges.

## The config

None. Tasks are live by default: they go to a hidden list on GitHub that is not a branch (`refs/lore/live`), so every teammate sees them on any branch. lore never pushes by itself: each person shares their writes with `lore sync` or their next `git push`. With no org (company setup), each developer registers on the repo's own roster (the list of people and agents) so `@handles` work.

## The commands

Each developer registers once, then shares the registration with `lore sync` (lore never pushes by itself). With no org, the roster entry itself is live, so `@alice` works on every clone after that sync and the next fetch:

<!-- example: small-team#register -->
```console
# alice, in billing
$ lore actor register
lore: wrote ~/work/billing/.lore/data/actors/act_7a67…9504.json
✓ act_7a67…9504 alice (@alice) already registered (refreshed) in this repo
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)

# bob, in billing
$ lore actor register
lore actor register: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/billing/.git/config; hook blocks in ~/work/billing/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: wrote ~/work/billing/.lore/data/actors/act_2ad3…f891.json
✓ act_2ad3…f891 bob (@bob) registered in this repo
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)

# alice, in billing
$ lore sync
lore live: pushed waiting live writes commit 601d285 to origin refs/lore/live
lore live: landed on main (86dbae4); leaves live once main is fetched

# bob, in billing
$ lore sync
lore live: pushed waiting live writes commit 9b40ce7 to origin refs/lore/live
lore live: landed on main (05513aa); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
lore live: pushed waiting live writes commit dfd8dc8 to origin refs/lore/live
```
<!-- /example -->

Bob, on `feat/payments`, gives Alice a task and shares it with `lore sync`. Alice, on `feat/login`, sees it. Her first command used the view from her last fetch; `lore sync` fetches now:

<!-- example: small-team#assign-across-branches -->
```console
# bob, in billing
$ git pull -q && git switch -q -c feat/payments && lore task add --title 'check the checkout page on a phone' --assign @alice
lore: wrote ~/work/billing/.lore/data/task_lists/tlt_162d…9666.json
lore: wrote ~/work/billing/.lore/data/tasks/tsk_54fe…28f2.json
✓ tsk_54fe…28f2 — check the checkout page on a phone [medium]
  created tasklist tlt_162d…9666 "default" for tasks added without --tasklist
$ lore sync
lore live: pushed waiting live writes commit cf563f3 to origin refs/lore/live
lore live: 1 row(s) left live (main holds them)
lore live: landed on main; this checkout is on feat/payments, so those rows show here after you merge main
lore live: pushed waiting live writes commit df3a55c to origin refs/lore/live

# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_54fe…28f2 check the checkout page on a phone [medium]
$ lore inbox
-  assigned              tsk_54fe…28f2  check the checkout page on a phone (lead)
```
<!-- /example -->

Alice finishes it and names the commit that did the work. Her next `lore sync` lands the task on `main`, as a commit of its own (this repo's `main` is not protected yet):

<!-- example: small-team#done-lands-on-main -->
```console
# alice, in billing
$ lore task start --id tsk_54fe…28f2
lore: wrote ~/work/billing/.lore/data/tasks/tsk_54fe…28f2.json
✓ tsk_54fe…28f2 started
$ echo ok > checkout.txt && git add checkout.txt && git commit -qm 'fix the checkout page on phones' && lore task done --id tsk_54fe…28f2 --commit $(git rev-parse --short HEAD)
lore: wrote ~/work/billing/.lore/data/tasks/tsk_54fe…28f2.json
✓ tsk_54fe…28f2 done (commit 6d00426); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
$ lore sync
lore live: pushed waiting live writes commit 34ee210 to origin refs/lore/live
lore live: landed on main (7898daa); leaves live once main is fetched
lore live: landed on main; this checkout is on feat/login, so those rows show here after you merge main
$ git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3
7898daa lore: land 2 rows (_root)
05513aa lore: land 1 row (_root)
86dbae4 lore: land 1 row (_root)
ccfb0bb lore: init
9d655a5 init
 .../tlt_162d…9666.json      | 27 +++++++
 .../tsk_54fe…28f2.json      | 87 ++++++++++++++++++++++
 2 files changed, 114 insertions(+)
$ git log --oneline -1 && git status --short
6d00426 fix the checkout page on phones
```
<!-- /example -->

Alice's branch and working folder are untouched. Her `lore sync` pushed the landing to the remote's `main` directly; finishing the task alone pushed nothing.

An urgent rule learned on a feature branch can reach `main` without switching branches. Add it as usual, copy it onto `main` with `lore sync promote`, then push `main` (or open a pull request for it):

```console
$ lore rule add --body 'Never deploy on a Friday'
$ lore sync promote --ids rul_05e9…4603 --to main --dry-run
$ lore sync promote --ids rul_05e9…4603 --to main
$ git push origin main
```

## When main is protected

Most teams protect `main` (only pull requests may change it). Then `lore sync` cannot land it, and the landing rides on the developer's next commit, as a separate commit:

<!-- example: small-team#protected-main -->
```console
# bob, in billing
$ lore task add --title 'ask the client for the new logo' --assign @alice
lore: wrote ~/work/billing/.lore/data/tasks/tsk_c0f3…b5b8.json
✓ tsk_c0f3…b5b8 — ask the client for the new logo [medium]
  on tasklist tlt_162d…9666 "default" (the default; --tasklist picks another)
$ lore sync
lore live: pushed waiting live writes commit 51886a5 to origin refs/lore/live
lore live: 2 row(s) left live (main holds them)
lore live: landed on main; this checkout is on feat/payments, so those rows show here after you merge main
lore live: pushed waiting live writes commit 22efc3b to origin refs/lore/live

# alice, in billing
$ lore sync >/dev/null && lore task done --id tsk_c0f3…b5b8
lore: wrote ~/work/billing/.lore/data/tasks/tsk_c0f3…b5b8.json
✓ tsk_c0f3…b5b8 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
$ lore sync
lore live: pushed waiting live writes commit 8c373dc to origin refs/lore/live
lore live: main is protected: the rows ride on your next push (lore: land N rows)
$ lore explain --id tsk_c0f3…b5b8
tsk_c0f3…b5b8 in ~/work/billing (folder _root)
  fetched 5s ago
  live (local 8c373dc): row, live since 2026-11-02T10:01:40Z, hlc 2026-11-02T10:01:50.000Z/0000/act_7a67…9504
  this branch (HEAD): not present
  origin/main: not present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: waits: main is protected; rides on your next push
$ echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'
lore: added "lore: land 1 row (_root)" to feat/login (tsk_c0f3…b5b8)
[feat/login 6f11034] fix login timeout
 1 file changed, 1 insertion(+)
 create mode 100644 login.txt
$ git log --oneline -3
468e9a8 lore: land 1 row (_root)
6f11034 fix login timeout
6d00426 fix the checkout page on phones
$ git push -q -u origin feat/login
$ git status --short
```
<!-- /example -->

`lore explain` says why the row has not landed yet ("waits: main is protected; rides on your next push"). The commit hook (a small script git runs after each commit) added `lore: land 1 row (_root)` on top of Alice's own commit. The pull request for `feat/login` now carries the task's file, and merging it lands the task. `git status` stays clean.

## What happened

```text
 bob (feat/payments)            remote                          alice (feat/login)
 task add --assign @alice
 lore sync ────────push──▶ refs/lore/live ──fetch (≤60s, or lore sync)──▶ task list --mine
                                                                  task done, lore sync
                                  refs/heads/main ◀──push "lore: land 2 rows"──┘   (unprotected)
                                  feat/login PR   ◀──"lore: land 1 row" rides on git push   (protected)
```

## Pitfalls

- **Nothing pushes by itself.** A write stays on your machine until you run `lore sync` or `git push` (the pre-push hook sends the live list with it). `lore config set --key live.autopush --value true` sends each write at once; lore then says once how to turn it off.
- **The 60-second cache.** Lore fetches once per command and reuses that fetch for 60 seconds (`live.fetch_ttl`). If you need a teammate's new row right now, run `lore sync`.
- **Register first, then branch from a fresh main.** `--assign @alice` fails with `E_NOT_FOUND: no person or team with handle @alice` until Alice has run `lore actor register` and your branch holds her roster entry. A roster entry lands on main on its person's `lore sync` and then leaves the live list, so a branch cut before it landed does not see it: `git pull` main before `git switch -c`, or `git merge origin/main` into an existing branch (the same rule as any row that left live, see `lore sync peek`).
- **Rules, decisions and memories travel with the code.** That is on purpose: most rules belong with the change that needs them. Use `lore sync promote --ids <id> --to main` for the one that cannot wait.
- **`git push --no-verify`** skips every local check lore runs at push: the ride-along (the rows simply wait and ride on a later push) and, in a client repo, the push leak check (see [Agency with a client repo](agency-client-repo.md)).
- **Unprotected `main`** receives small `lore: land N rows` commits directly. They change only files under `.lore/data`.

## Related

- [Getting started](../getting-started.md): one person, the first live task.
- [Agents](../agents.md): adding coding agents to the same repo.
- [Team profiles](../profiles.md): roles and project memberships for the roster.
- [How it works](../how-it-works.md): live rows and landing in git.
