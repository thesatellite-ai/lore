# Troubleshooting

Real error messages, what causes them and how to fix them, then the questions people ask most. Every error below was produced on purpose by `tests/team/docs/run-examples.sh errors`.

First steps for anything odd:

- `lore whoami` (add `--explain`): which org, which identity, which store, and why.
- `lore explain <id>`: every copy of a row, which one won, and its landing decision.
- `lore doctor`: hooks, fetch settings, live rows waiting, clock skew, stores.
- `lore store doctor`: every configured store, its marker, its clone, unpushed work.
- `--trace` on any command (or `LORE_TRACE=1`): every git command lore runs, with timing.

## A teammate's task or `@handle` does not show up

Each command fetches at most once a minute (`live.fetch_ttl: 60s`). A row a teammate wrote seconds ago may not be in your view yet, and `--assign @dev1` fails with `E_NOT_FOUND: no person or team with handle @dev1` until dev1 has registered and you have fetched since. Run `lore sync`, which always fetches:

<!-- example: small-team#assign-across-branches only=2,3 -->
```console
# dev1, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_162d…9666 review the retry logic [medium]
```
<!-- /example -->

## `E_CONFIG_INVALID`

Every command refuses to run with a broken `~/.lore/config.yaml`; `lore config check` lists every problem with its place:

<!-- example: errors#config-invalid -->
```console
# alice, in ~
$ lore config check
✗ /home/alice/.lore/config.yaml
  - orgs[0] "northwind": stores[0].branch "release/2026": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
ERROR: [E_CONFIG_INVALID] /home/alice/.lore/config.yaml is not valid
  hint: fix the problems listed above; nothing else in lore reads an invalid config
(exit 1)

# alice, in acme-app
$ lore whoami
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: lorecfg: load /home/alice/.lore/config.yaml: cfgkit: 1 problem(s):
orgs[0] "northwind": stores[0].branch "release/2026": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
  hint: run `lore config check` for every problem with its location
(exit 1)
```
<!-- /example -->

Fix the listed key (here: a store branch may not contain `/`) and run `lore config check` again.

## `E_NOT_A_STORE`

A repo listed under `stores:` is not a lore store: it has no `lore-store.yaml`, or it holds code. Lore refuses to clone or write to it:

<!-- example: errors#not-a-store -->
```console
# alice, in acme-app
$ lore whoami
ERROR: [E_NOT_A_STORE] <redacted> (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files such as .gitignore, package.json, src/)
  hint: create one with `lore store init <url>` in a new empty repo
(exit 19)

# alice, in ~
$ lore store doctor
! <redacted>  (entries northwind; branches main)
    <redacted> (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files such as .gitignore, package.json, src/)
✓ <redacted>  (entries northwind; branches main)
  direct pushes to a store branch are confirmed on the next write; a refusal is reported there
ERROR: [E_INVALID_INPUT] 1 store(s) need attention
(exit 1)
```
<!-- /example -->

Fix: remove the code repo from `stores:`. To make a store, create a new, empty repo and run `lore store init <url>` on it. A code repo can never be a store.

## `E_ORG_AMBIGUOUS`

Two org entries match the repo at the same rule, so lore will not pick one:

<!-- example: errors#org-ambiguous -->
```console
# alice, in acme-site
$ lore whoami
org        refused  (the patterns of 2 org entries match this repo (initech, northwind) (candidates: initech, northwind))
identity   (not resolved: the org is refused above)
project    prj_7a67…9504
remotes    github.com/acme/site
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: everywhere/
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (initech, northwind) (candidates: initech, northwind)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
$ lore memory add --body 'the site deploys from the release branch'
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (initech, northwind) (candidates: initech, northwind)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
```
<!-- /example -->

Fix: `lore org use <entry>` for this repo on this machine, `LORE_ORG=<entry>` for one command, or narrow the `own:` / `repos:` patterns so only one entry matches. A related error, `E_ORG_DISAGREES`, means `LORE_ORG` or `lore org use` names a different org than the store link of this repo; org-scoped writes are refused unless you pass `--force-org`.

## `E_LINK_UNKNOWN`

In a repo that matches a client pattern, lore must know whether it is linked before it decides where a row goes. If the store cannot be read and there is no cached link index (a new clone, a new machine, the store unreachable), writes without `--to` are refused:

<!-- example: errors#link-unknown -->
```console
# dev2, in acme-app
$ lore memory add --body 'the client wants weekly demos'
ERROR: [E_LINK_UNKNOWN] lore cannot tell whether this repo is a client repo: cannot read northwind: github.com/northwind/lore (never fetched)
  hint: retry when the store is reachable, or pass --to repo|<org>|me to choose the target yourself
(exit 18)
```
<!-- /example -->

Fix: retry when the store is reachable (check access with `lore store doctor`), or pass `--to repo`, `--to <org>` or `--to me` to choose the target yourself. Reads still work.

## Store branch does not exist

A staging entry (or a typo in `branch:`) names a store branch that does not exist. Lore never creates one by accident:

<!-- example: errors#store-branch-missing -->
```console
# alice, in acme-app
$ LORE_ORG=northwind-stag lore org link northwind-stag --project acme --yes
This marks github.com/acme/app as a CLIENT repo of northwind-stag: internal notes default to github.com/northwind/lore acme/,
and the leak guards turn on for everyone in northwind-stag.
  link: project prj_54fe…28f2 at github.com/acme/app → github.com/northwind/lore@staging:acme/ (default=false)
ERROR: [E_INVALID_INPUT] stores: store branch does not exist: github.com/northwind/lore has no branch staging; create it with `lore store init git@github.com:northwind/lore --branch staging`
  hint: a store branch is created only on purpose: `lore store init <url> --branch <branch>`
(exit 1)
```
<!-- /example -->

Fix: if you meant it, `lore store init <url> --branch <branch>` once; otherwise fix `branch:` in the config.

## `E_OFFLINE` on a claim, heartbeat or done

Coordination writes need the remote to confirm them; they are never queued:

<!-- example: errors#offline-claim only=1 -->
```console
# alice, in acme-app
$ lore task claim tsk_2ad3…f891
ERROR: [E_OFFLINE] could not confirm the coordination write (remote unreachable): gitremote: fetch: gitremote: remote did not confirm the push: fatal: '/srv/git/github.com/northwind/lore' does not appear to be a git repository
fatal: Could not read from remote repository.
Please make sure you have the correct access rights
and the repository exists.
  hint: coordination writes are never queued; reconnect (or turn live.autopush on) and run the command again
(exit 15)
```
<!-- /example -->

Fix: reconnect and run the command again. Ordinary writes behave differently: a memory or task written while the store is unreachable is committed in the local clone and pushed by a later command ("1 file(s) offline: committed in the local clone, pushed by the next command"), and live writes in a code repo wait as "(live, not pushed yet: N waiting)".

## `E_LOST_CLAIM`, `E_CANCELLED`, `E_REASSIGNED`

Your claim on a task is gone: the lease expired and another agent took it over, a person revoked or cancelled it, or reassigned it. The message says who holds it now. Stop working on that task; see [Agents](agents.md#fail-closed-errors-and-what-to-do).

## `E_LEAK_BLOCKED`

A commit, a live write to a client repo or a pull request text contains internal content: a row id of another project, a pointer to an internal row, or a run of 8 or more words that also appears in an internal or personal note. The message names the file and line, the matching note, and the two ways out:

<!-- example: agency#leak-blocked only=2 -->
```console
# alice, in acme-app
$ git commit -m 'add notes'
ERROR: [E_LEAK_BLOCKED] lore: blocked, this commit would share internal content with the client repo:
  src/notes.ts:1                           matches internal note mem_54fe…28f2 (10 words)
Move it:  lore move mem_54fe…28f2 --to <org>
Share it on purpose: lore share mem_54fe…28f2 --to repo
(a copy-paste check, not a wall: it cannot see paraphrase; see `lore check leaks --explain`)
(exit 1)
```
<!-- /example -->

- The text should not be there: remove it, or `lore move <id> --to <org>` if a note was filed in the wrong place.
- It really should be shared with the client: `lore share <id> --to repo` (see [Sharing an internal note](use-cases/sharing-a-note.md)).
- `lore check leaks --explain` shows the rule and the matched words.

Never bypass the check with `git commit --no-verify` in a client repo. If something already reached the client, follow [Cleaning up after a leak](use-cases/leak-cleanup.md).

## "live, not pushed yet": the host refuses `refs/lore/*`

Some git hosts or server hooks accept only branches and tags. Live rows then stay on your machine, and `lore doctor` says why:

<!-- example: errors#refs-refused-by-the-host -->
```console
# dev2, in portal
$ lore task add 'set up the portal CI'
✓ tsk_05e9…4603 tsk_05e9…4603 — set up the portal CI [medium]
lore live: (live, not pushed yet: 2 waiting; the remote refused refs/lore/live: [remote rejected] (pre-receive hook declined))
$ lore live diff
local     601d2850ffd0
tracking  (none)
seen      (none)
pending   2 write(s) not confirmed by the remote
  _root/task_lists/tlt_c0f3…b5b8.json
  _root/tasks/tsk_05e9…4603.json
lore live: (live, not pushed yet: 2 waiting; the remote refused refs/lore/live: [remote rejected] (pre-receive hook declined))
$ lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 15s ago
  - live: 2 live write(s) not pushed yet
  - live: 2 row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach main at live.max_age)
  - live: 1 row(s) waiting to land; the next live write or `lore sync` lands them
  - live: live ref: 1 commit(s) since the last compaction (epoch 1; automatic past 1000)
  - live: post-commit and pre-push hook blocks installed
  - live: the remote refused a push to refs/lore/live, so live rows stay on this machine (the host may allow only branches and tags): [remote rejected] (pre-receive hook declined)
```
<!-- /example -->

Fix: allow `refs/lore/*` on the host (or in its pre-receive hook). Until then, live rows only reach teammates when they land on `main`; `lore land --all` lands them now. Setting `live.autopush: false` stops the retries.

## `whoami` prints `<redacted>`

<!-- ds:block id=team-ci-markers-t4gm77bb -->
Without a terminal (a script, CI, an agent's tool call) and whenever a CI variable is set (`CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`, `JENKINS_URL`, `TF_BUILD`, `BITBUCKET_BUILD_NUMBER`), `whoami`, `doctor` and `explain` hide store URLs and internal folder names, so they never end up in a log the client can read:

<!-- example: agency#link only=7 -->
```console
# alice, in acme-app
$ lore whoami
org        northwind  (matched by: link in <redacted>, project id + remote)
identity   Alice Doe <human:alice@northwind.example>   act_7a67…9504  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
project    prj_2ad3…f891
remotes    github.com/acme/app
stores     <redacted> on branch main  (org folder <redacted>)
internal   <redacted> <redacted>/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   local: northwind/, everywhere/
```
<!-- /example -->

On your own terminal they are printed. `--share` prints them on a terminal on purpose.

## "Database is locked" and slow commands

Lore keeps a local cache (`.lore/lore.db`) and takes a short lock while it writes. Two lore commands at once in one checkout (a hook and your own command, say) wait for each other; `E_LOCK_HELD` (another lore process holds the project lock), `E_DB_LOCKED` or `E_BUSY_TIMEOUT` means the wait ran out (these three are listed by `lore errors list`; unlike the other errors on this page they are not reproduced by the example script). Run the command again; `lore doctor` checks the cache. A claim or a live write is a git round trip, about one to two seconds; `--trace` shows where the time goes.

## FAQ

**A dev creates a task for another dev on their branch. Does the other dev get it only after a merge to `main`?** No. Tasks are live: they are pushed to `refs/lore/live` at once and every clone sees them on its next command (cached up to 60 seconds), whatever branch anyone is on. They land on `main` when finished.

**Is `refs/lore/live` a branch?** It is a git ref outside `refs/heads/`: fetched and pushed like a branch, but `git branch` does not list it and nobody checks it out. It is hidden, not private.

**How can lore push a commit without changing my branch?** A commit does not need a branch; a branch is only a bookmark. Lore builds the commit with git's low-level commands (a private index, `commit-tree`) and moves `refs/lore/live` to it. Your branch, index and working folder are never touched.

**Will tasks no longer be stored in `.lore/data`?** They will. `main`'s `.lore/data` is the durable home of every row, tasks included; live is temporary sharing until the task is finished and landed.

**Can I keep rules and memories only for me?** Yes: `--to me` writes to your personal store, filed by the org you are in. See [Personal notes](use-cases/personal-notes.md).

**The client can read the client repo. How do internal notes stay hidden but still tied to the project?** They live in your company's store, in an internal project linked to the client repo. The link is stored only in the store, and the internal render and cache live outside the checkout. See [Agency with a client repo](use-cases/agency-client-repo.md).

**One store, or one per client?** Both are plain config. One store per client is the way to keep a contractor away from other clients' notes, because GitHub cannot restrict read access to a folder. See [Contractor limited to one client](use-cases/contractor.md).

**How do several repos work as one product?** Link them all to one internal project (a group); each task says which repo it is for. See [Several repos as one product](use-cases/group-project.md).

**Where does the roster live, and how does each person get an id?** In the org folder of the org's store (`actors/`, `teams/`). `lore actor register` adds you; your id comes from your identity key, so it is the same on every machine.

**I work for two companies. Can their notes mix?** No. A repo loads exactly one org; your personal notes are filed per org. See [One person in two companies](use-cases/two-companies.md).

**Is `lore.db` per repo?** Yes, one per checkout, holding this branch's and this repo's live rows only. Store and personal rows are cached outside the checkout in `~/.lore/cache/<project-id>.db`. Both can be rebuilt from the files at any time.

**What if GitHub's "Download ZIP", a fork or a shallow clone is used?** They do not carry `refs/lore/*`. That is why live rows are temporary and open rows are copied to `main` at their max age (7 days). `lore doctor` warns when rows exist only in live.

**What do hooks not protect against?** `--no-verify`, edits in the GitHub web interface, teammates without lore, paraphrased text, text pasted into PR bodies or chat without `lore check pr-body`, client-owned machines with your stores configured, and backups of `~/.lore`. The guards catch copy-paste mistakes; they are not a wall.

**Can two agents end up working on the same task?** No: a claim is a compare-and-swap push, so exactly one wins. A holder whose lease expired is fenced out (`E_LOST_CLAIM`) and cannot finish the task.

**Do heartbeats fill the repo with commits?** No. Signals live on `refs/lore/signals`, which always holds exactly one commit; each heartbeat replaces it.
