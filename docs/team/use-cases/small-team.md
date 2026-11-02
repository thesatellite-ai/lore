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
✓ act_7a67…9504 dev1 (@dev1) already registered (refreshed) in this repo
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched

# dev2, in billing
$ lore actor register
✓ act_2ad3…f891 dev2 (@dev2) registered in this repo
lore live: (live, pushed)
lore live: landed on main (86dbae4); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
```
<!-- /example -->

dev2, on `feat/payments`, gives dev1 a task; dev1, on `feat/login`, sees it. dev1's first command ran within a minute of their previous fetch, so it used the cached view; `lore sync` fetches at once:

<!-- example: small-team#assign-across-branches -->
```console
# dev2, in billing
$ git switch -q -c feat/payments && lore task add 'review the retry logic' --assign @dev1
✓ tsk_162d…9666 tsk_162d…9666 — review the retry logic [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)

# dev1, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_162d…9666 review the retry logic [medium]
$ lore inbox
-  assigned              tsk_162d…9666  review the retry logic
```
<!-- /example -->

dev1 finishes it and names the commit that did the work. The task lands on `main` straight away (this repo's `main` is not protected yet), as a commit of its own:

<!-- example: small-team#done-lands-on-main -->
```console
# dev1, in billing
$ lore task start tsk_162d…9666
✓ tsk_162d…9666 started
lore live: (live, pushed)
$ echo retry > retry.txt && git add retry.txt && git commit -qm 'tighten retry' && lore task done tsk_162d…9666 --commit $(git rev-parse --short HEAD)
✓ tsk_162d…9666 done (commit 9b40ce7); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (05513aa); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3
05513aa lore: land 2 rows (_root)
86dbae4 lore: land 1 row (_root)
601d285 lore: land 1 row (_root)
dfd8dc8 lore: init
cf563f3 init
 .../tlt_54fe…28f2.json      | 11 ++++++
 .../tsk_162d…9666.json      | 40 ++++++++++++++++++++++
 2 files changed, 51 insertions(+)
$ git log --oneline -1 && git status --short
9b40ce7 tighten retry
```
<!-- /example -->

dev1's branch and working folder are untouched: the landing was pushed to the remote's `main` directly.

An urgent rule, from any branch, with `--live`: everyone sees it at once, and because a rule is finished on every write, a copy lands on `main` immediately:

<!-- example: small-team#urgent-rule -->
```console
# dev2, in billing
$ lore rule add --body 'never run migrations from the app container' --live
✓ rul_05e9…4603 rul_05e9…4603 
  scope: [master] severity: must
lore live: (live, pushed)
lore live: landed on main (df3a55c); leaves live once main is fetched
$ git fetch -q origin && git log --oneline -1 origin/main
df3a55c lore: land 1 row (_root)
```
<!-- /example -->

## When main is protected

Most teams protect `main`. Then the landing waits and rides on the developer's next push, as a separate commit:

<!-- example: small-team#protected-main -->
```console
# dev2, in billing
$ lore task add 'rotate the webhook secret' --assign @dev1
✓ tsk_c0f3…b5b8 tsk_c0f3…b5b8 — rotate the webhook secret [medium]
lore live: (live, pushed)
lore live: 3 row(s) left live (main holds them)

# dev1, in billing
$ lore sync >/dev/null && lore task done tsk_c0f3…b5b8
✓ tsk_c0f3…b5b8 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: main is protected: the rows ride on your next push (lore: land N rows)
$ lore explain tsk_c0f3…b5b8
tsk_c0f3…b5b8 in /home/dev1/work/billing (folder _root)
  fetched 5s ago
  live (local 34ee210): row, live since 2026-11-02T10:01:10Z, hlc 2026-11-02T10:01:15.000Z/0000/act_7a67…9504
  this branch (HEAD): not present
  origin/main: not present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: waits: main is protected; rides on your next push
$ echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'
lore: added "lore: land 1 row (_root)" to feat/login (tsk_c0f3…b5b8)
[feat/login 7898daa] fix login timeout
 1 file changed, 1 insertion(+)
 create mode 100644 login.txt
$ git log --oneline -3
ccfb0bb lore: land 1 row (_root)
7898daa fix login timeout
9b40ce7 tighten retry
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
