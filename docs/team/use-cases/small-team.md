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

# bob, in billing
$ lore actor register
✓ act_2ad3…f891 bob (@bob) registered in this repo
```
<!-- /example -->

Bob, on `feat/payments`, gives Alice a task. Alice, on `feat/login`, sees it. Her first command ran within 60 seconds of her previous fetch, so it used the cached view. `lore sync` fetches at once:

<!-- example: small-team#assign-across-branches -->
```console
# bob, in billing
$ git switch -q -c feat/payments && lore task add 'check the checkout page on a phone' --assign @alice
lore sync: imported 1
ERROR: [E_NOT_FOUND] no person or team with handle @alice
  hint: people join with `lore actor register`; teams with `lore team add`

# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
(no tasks)
$ lore inbox
(inbox empty)
```
<!-- /example -->

Alice finishes it and names the commit that did the work. The task lands on `main` straight away, as a commit of its own (this repo's `main` is not protected yet):

<!-- example: small-team#done-lands-on-main -->
```console
# alice, in billing
$ lore task start tsk_missing
ERROR: [E_NOT_FOUND] task: "tsk_missing" is not a task id (tsk_…)
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main tasks`
$ echo ok > checkout.txt && git add checkout.txt && git commit -qm 'fix the checkout page on phones' && lore task done tsk_missing --commit $(git rev-parse --short HEAD)
ERROR: [E_NOT_FOUND] task: "tsk_missing" is not a task id (tsk_…)
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main tasks`
$ git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3
601d285 lore: init
86dbae4 init
 .../rep_162d…9666.json      | 33 ++++++++++++++++++++++
 AGENTS.md                                          |  3 ++
 8 files changed, 135 insertions(+)
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
ERROR: [E_NOT_FOUND] no person or team with handle @alice
  hint: people join with `lore actor register`; teams with `lore team add`

# alice, in billing
$ lore sync >/dev/null && lore task done tsk_missing
ERROR: [E_NOT_FOUND] task: "tsk_missing" is not a task id (tsk_…)
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main tasks`
$ lore explain tsk_missing
tsk_missing in ~/work/billing (folder _root)
  fetched 5s ago
  live: not present
  not found in live or on this branch; it may have landed on main while this branch is older: `lore sync peek origin/main`
$ echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'
[feat/login dfd8dc8] fix login timeout
 1 file changed, 1 insertion(+)
 create mode 100644 login.txt
$ git log --oneline -3
dfd8dc8 fix login timeout
9b40ce7 fix the checkout page on phones
601d285 lore: init
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
- **Register first.** `--assign @alice` fails with `E_NOT_FOUND: no person or team with handle @alice` until Alice has run `lore actor register` and your clone has fetched since.
- **Rules, decisions and memories travel with the code.** That is on purpose: most rules belong with the change that needs them. Use `lore sync promote <id> --to main` for the one that cannot wait.
- **`git push --no-verify`** skips every local check lore runs at push: the ride-along (the rows simply wait and ride on a later push) and, in a client repo, the push leak check (see [Agency with a client repo](agency-client-repo.md)).
- **Unprotected `main`** receives small `lore: land N rows` commits directly. They change only files under `.lore/data`.
