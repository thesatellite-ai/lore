# Small team in one repo

**In short:** Alice and Bob (agency developers) work in one repo on different branches. When Bob gives Alice a task, she sees it on her next command, without waiting for a merge. When she finishes it, the task is saved on `main` by itself.

## The situation

Alice and Bob work in one repo, `github.com/agency/billing`, each on their own feature branch. When Bob gives Alice a task, Alice must see it now, not after Bob's branch merges.

## The config

None. Tasks are live by default: they go to a hidden list on GitHub that is not a branch (`refs/lore/live`), so every teammate sees them at once. With no org (company setup), each developer registers on the repo's own roster (the list of people and agents) so `@handles` work.

## The commands

Each developer registers once. With no org, the roster entry itself is live. So `@alice` works on every clone after the next fetch:

<!-- example: small-team#register -->
```console
# alice, in billing
$ lore actor register
✓ act_7a67…9504 alice (@alice) already registered (refreshed) in this repo
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched

# bob, in billing
$ lore actor register
✓ act_2ad3…f891 bob (@bob) registered in this repo
lore live: (live, pushed)
lore live: landed on main (86dbae4); leaves live once main is fetched
lore live: 1 row(s) left live (main holds them)
```
<!-- /example -->

Bob, on `feat/payments`, gives Alice a task. Alice, on `feat/login`, sees it. Her first command ran within 60 seconds of her previous fetch, so it used the cached view. `lore sync` fetches at once:

<!-- example: small-team#assign-across-branches -->
```console
# bob, in billing
$ git pull -q && git switch -q -c feat/payments && lore task add 'check the checkout page on a phone' --assign @alice
✓ tsk_162d…9666 — check the checkout page on a phone [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)

# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_162d…9666 check the checkout page on a phone [medium]
$ lore inbox
-  assigned              tsk_162d…9666  check the checkout page on a phone (lead)
```
<!-- /example -->

Alice finishes it and names the commit that did the work. The task lands on `main` straight away, as a commit of its own (this repo's `main` is not protected yet):

<!-- example: small-team#done-lands-on-main -->
```console
# alice, in billing
$ lore task start tsk_162d…9666
✓ tsk_162d…9666 started
lore live: (live, pushed)
$ echo ok > checkout.txt && git add checkout.txt && git commit -qm 'fix the checkout page on phones' && lore task done tsk_162d…9666 --commit $(git rev-parse --short HEAD)
✓ tsk_162d…9666 done (commit 9b40ce7); waiting for merge
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (05513aa); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3
05513aa lore: land 1 row (_root)
86dbae4 lore: land 1 row (_root)
601d285 lore: land 1 row (_root)
dfd8dc8 lore: init
cf563f3 init
 .../tsk_162d…9666.json      | 87 ++++++++++++++++++++++
 1 file changed, 87 insertions(+)
$ git log --oneline -1 && git status --short
9b40ce7 fix the checkout page on phones
```
<!-- /example -->

Alice's branch and working folder are untouched. The landing was pushed to the remote's `main` directly.

An urgent rule learned on a feature branch can reach `main` without switching branches. Add it as usual, copy it onto `main` with `lore sync promote`, then push `main` (or open a pull request for it):

```console
$ lore rule add --body 'Never deploy on a Friday'
$ lore sync promote rul_05e9…4603 --to main --dry-run
$ lore sync promote rul_05e9…4603 --to main
$ git push origin main
```

## When main is protected

Most teams protect `main` (only pull requests may change it). Then the landing waits and rides on the developer's next push, as a separate commit:

<!-- example: small-team#protected-main -->
```console
# bob, in billing
$ lore task add 'ask the client for the new logo' --assign @alice
✓ tsk_05e9…4603 — ask the client for the new logo [medium]
lore live: (live, pushed)
lore live: 1 row(s) left live (main holds them)

# alice, in billing
$ lore sync >/dev/null && lore task done tsk_05e9…4603
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: main is protected: the rows ride on your next push (lore: land N rows)
$ lore explain tsk_05e9…4603
tsk_05e9…4603 in ~/work/billing (folder _root)
  fetched 5s ago
  live (local 34ee210): row, live since 2026-11-02T10:01:20Z, hlc 2026-11-02T10:01:25.000Z/0000/act_7a67…9504
  this branch (HEAD): not present
  origin/main: not present
  wins: the live copy (rule 4: if a row id is in live, live wins)
  land: waits: main is protected; rides on your next push
$ echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'
lore: added "lore: land 1 row (_root)" to feat/login (tsk_05e9…4603)
[feat/login 7898daa] fix login timeout
 1 file changed, 1 insertion(+)
 create mode 100644 login.txt
$ git log --oneline -3
ccfb0bb lore: land 1 row (_root)
7898daa fix login timeout
9b40ce7 fix the checkout page on phones
$ git push -q -u origin feat/login
$ git status --short
```
<!-- /example -->

`lore explain` says why the row has not landed yet ("waits: main is protected; rides on your next push"). The commit hook (a small script git runs after each commit) added `lore: land 1 row (_root)` on top of Alice's own commit. The pull request for `feat/login` now carries the task's file, and merging it lands the task. `git status` stays clean.

## What happened

```text
 bob (feat/payments)            remote                          alice (feat/login)
 task add --assign @alice ──push──▶ refs/lore/live ──fetch (≤60s, or lore sync)──▶ task list --mine
                                                                  task done
                                  refs/heads/main ◀──push "lore: land 2 rows"──┘   (unprotected)
                                  feat/login PR   ◀──"lore: land 1 row" rides on git push   (protected)
```

## Pitfalls

- **The 60-second cache.** Lore fetches once per command and reuses that fetch for 60 seconds (`live.fetch_ttl`). If you need a teammate's new row right now, run `lore sync`.
- **Register first, then branch from a fresh main.** `--assign @alice` fails with `E_NOT_FOUND: no person or team with handle @alice` until Alice has run `lore actor register` and your branch holds her roster entry. A roster entry lands on main at once and then leaves the live list, so a branch cut before it landed does not see it: `git pull` main before `git switch -c`, or `git merge origin/main` into an existing branch (the same rule as any row that left live, see `lore sync peek`).
- **Rules, decisions and memories travel with the code.** That is on purpose: most rules belong with the change that needs them. Use `lore sync promote <id> --to main` for the one that cannot wait.
- **`git push --no-verify`** skips every local check lore runs at push: the ride-along (the rows simply wait and ride on a later push) and, in a client repo, the push leak check (see [Agency with a client repo](agency-client-repo.md)).
- **Unprotected `main`** receives small `lore: land N rows` commits directly. They change only files under `.lore/data`.
