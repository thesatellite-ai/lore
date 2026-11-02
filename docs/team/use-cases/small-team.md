# Small team in one repo

## The situation

dev1 and dev2 work in one repo, `github.com/initech/billing`, each on their own feature branch. When dev2 gives dev1 a task, dev1 must see it now, not after dev2's branch merges.

## The config

None. Tasks are live by default, and with no org each developer registers on the repo's own roster so `@handles` work.

## The commands

Each developer registers once. With no org, the actor row itself is live, so `@dev1` resolves on every clone after the next fetch:

<!-- example: small-team#register -->
```console
# dev1, in billing
$ lore actor register
✓ act_9ede…5b9b dev1 (@dev1) already registered (refreshed) in this repo
lore live: (live, pushed)
lore live: landed on main (b429458); leaves live once main is fetched

# dev2, in billing
$ lore actor register
✓ act_3161…5ae6 dev2 (@dev2) registered in this repo
lore live: (live, pushed)
lore live: landed on main (e3a0f8c); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
```
<!-- /example -->

dev2, on `feat/payments`, gives dev1 a task; dev1, on `feat/login`, sees it. dev1's first command ran within a minute of their previous fetch, so it used the cached view; `lore sync` fetches at once:

<!-- example: small-team#assign-across-branches -->
```console
# dev2, in billing
$ git switch -q -c feat/payments && lore task add 'review the retry logic' --assign @dev1
✓ tsk_01a1…9a35 tsk_01a1…9a35 — review the retry logic [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)

# dev1, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_01a1…9a35 review the retry logic [medium]
$ lore inbox
-  assigned              tsk_01a1…9a35  review the retry logic
```
<!-- /example -->

dev1 finishes it and names the commit that did the work. The task lands on `main` straight away (this repo's `main` is not protected yet), as a commit of its own:

<!-- example: small-team#done-lands-on-main -->
```console
# dev1, in billing
$ lore task start tsk_01a1…9a35
✓ tsk_01a1…9a35 started
lore live: (live, pushed)
$ echo retry > retry.txt && git add retry.txt && git commit -qm 'tighten retry' && lore task done tsk_01a1…9a35 --commit $(git rev-parse --short HEAD)
✓ tsk_01a1…9a35 done (commit aa3962b); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (8b7fc35); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3
8b7fc35 lore: land 2 rows (_root)
e3a0f8c lore: land 1 row (_root)
b429458 lore: land 1 row (_root)
e6678cd lore: init
046c91d init
 .../tlt_01a1…4706.json      | 11 ++++++
 .../tsk_01a1…9a35.json      | 40 ++++++++++++++++++++++
 2 files changed, 51 insertions(+)
$ git log --oneline -1 && git status --short
aa3962b tighten retry
```
<!-- /example -->

dev1's branch and working folder are untouched: the landing was pushed to the remote's `main` directly.

An urgent rule, from any branch, with `--live`: everyone sees it at once, and because a rule is finished on every write, a copy lands on `main` immediately:

<!-- example: small-team#urgent-rule -->
```console
# dev2, in billing
$ lore rule add --body 'never run migrations from the app container' --live
✓ rul_01a1…1bf7 rul_01a1…1bf7 
  scope: [master] severity: must
lore live: (live, pushed)
lore live: landed on main (dafe560); leaves live once main is fetched
$ git fetch -q origin && git log --oneline -1 origin/main
dafe560 lore: land 1 row (_root)
```
<!-- /example -->

## When main is protected

Most teams protect `main`. Then the landing waits and rides on the developer's next push, as a separate commit:

<!-- example: small-team#protected-main -->
```console
# dev2, in billing
$ lore task add 'rotate the webhook secret' --assign @dev1
✓ tsk_01a1…2d49 tsk_01a1…2d49 — rotate the webhook secret [medium]
lore live: (live, pushed)
lore live: 3 row(s) left live (main holds them)

# dev1, in billing
$ lore sync >/dev/null && lore task done tsk_01a1…2d49
✓ tsk_01a1…2d49 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: main is protected: the rows ride on your next push (lore: land N rows)
$ lore explain tsk_01a1…2d49
tsk_01a1…2d49 in /home/dev1/work/billing (folder _root)
  fetched 1s ago
  live (local e922b74): row, live since 2026-11-02T18:41:49Z, hlc 2026-11-02T18:41:50.183Z/0000/act_9ede…5b9b
  this branch (HEAD): not present
  origin/main: not present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: waits: main is protected; rides on your next push
$ echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'
lore: added "lore: land 1 row (_root)" to feat/login (tsk_01a1…2d49)
[feat/login a0d102d] fix login timeout
 1 file changed, 1 insertion(+)
 create mode 100644 login.txt
$ git log --oneline -3
6e5fa3f lore: land 1 row (_root)
a0d102d fix login timeout
aa3962b tighten retry
$ git push -q -u origin feat/login
$ git status --short
```
<!-- /example -->

`lore explain` says why the row has not landed yet ("waits: main is protected; rides on your next push"). The commit hook added `lore: land 1 row (_root)` on top of dev1's own commit; the pull request for `feat/login` now carries the task's file, and merging it lands the task. `git status` stays clean.

## What happened

```text
 dev2 (feat/payments)            remote                          dev1 (feat/login)
 task add --assign @dev1 ──push──▶ refs/lore/live ──fetch (≤60s, or lore sync)──▶ task list --mine
                                                                  task done
                                  refs/heads/main ◀──push "lore: land 2 rows"──┘   (unprotected)
                                  feat/login PR   ◀──"lore: land 1 row" rides on git push   (protected)
```

## Gotchas

- **The 60-second cache.** Each command fetches at most once a minute (`live.fetch_ttl`). Right after a teammate writes, run `lore sync` if you need it now.
- **Register first.** `--assign @dev1` fails with `E_NOT_FOUND: no person or team with handle @dev1` until dev1 has run `lore actor register` and your clone has fetched since.
- **Rules and decisions travel with the code** unless you pass `--live`. That is on purpose: most rules belong with the change that needs them.
- **`git push --no-verify`** skips the ride-along check; the rows simply wait and ride on a later push.
- **Unprotected `main`** receives small `lore: land N rows` commits directly. They change only files under `.lore/data`.
