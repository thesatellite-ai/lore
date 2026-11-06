# Troubleshooting

**In short:** each section below starts with an error message you may see. It says what causes it in plain words and gives the fix. The questions people ask most are at the end.

Every example output below is real: it was produced on purpose by `tests/team/docs/run-examples.sh errors`. A few sections have no example; their text is checked against the code instead.

First steps for anything odd:

- `lore whoami` (add `--explain`): which org, which identity, which store, and why.
- `lore explain <id>`: every copy of a row, which one won, and its landing decision.
- `lore doctor`: hooks, fetch settings, live rows waiting, clock skew, stores.
- `lore store doctor`: every configured store, its marker, its clone, unpushed work.
- `--trace` on any command (or `LORE_TRACE=1`): every git command lore runs, with timing.

For example, the fetch a `lore sync` makes (the time is shown as `<time>` here):

<!-- example: live-repair#trace -->
```console
# alice, in billing
$ lore sync --trace 2>&1 | grep -e '^trace: git fetch' -e '^trace: git push' -e '^·'
trace: git fetch --quiet --no-tags --no-write-fetch-head --refmap= --prune -- origin +refs/lore/*:refs/lore-remote/origin/* +refs/heads/main:refs/remotes/origin/main (<time>, ok)
· already in sync
```
<!-- /example -->

## A teammate's task or `@handle` does not show up

Lore fetches once per command and reuses that fetch for 60 seconds (`live.fetch_ttl: 60s`). `lore sync` always fetches, and it also refreshes the store link index, so a link a teammate just made shows up in `lore whoami` after it. So a row a teammate wrote seconds ago may not be in your view yet. Also, `--assign @alice` fails with `E_NOT_FOUND: no person or team with handle @alice` until Alice (an agency developer) has registered and you have fetched since. Run `lore sync`, which always fetches:

<!-- example: small-team#assign-across-branches only=2,3 -->
```console
# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
$ lore sync && lore task list --mine
· already in sync
○ tsk_162d…9666 check the checkout page on a phone [medium]
```
<!-- /example -->

## `E_CONFIG_INVALID`

Every command refuses to run while `~/.lore/config.yaml` is broken. `lore config check` lists every problem and where it is:

<!-- example: errors#config-invalid -->
```console
# alice, in ~
$ lore config check
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: orgs[0] "agency": stores[0].branch "release/2026": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
  hint: fix the problems listed; nothing else in lore reads an invalid config
(exit 20)

# alice, in website
$ lore whoami
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: orgs[0] "agency": stores[0].branch "release/2026": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
  hint: run `lore config check` for every problem with its location
(exit 20)
```
<!-- /example -->

Fix the listed key (here: a store branch may not contain `/`) and run `lore config check` again.

The same check refuses an org name, a store's project folder name or a personal-store folder name that Windows cannot check out: a device name in any case, with or without an extension (`con`, `prn`, `aux`, `nul`, `com1` to `com9`, `lpt1` to `lpt9`, so `NUL.txt` too), or a name ending in `.` (Windows drops the dot, so `web.` and `web` would be one folder). It is refused on every system, because a store is shared and one teammate's folder would break every Windows clone of it. Pick another name. A folder of such a name that already exists in a store stays readable where it is. Names that differ only in upper and lower case (`App` and `app`) are not refused; avoid them yourself, because Windows and macOS see them as one folder.

## `E_NOT_A_STORE`

A **store** is a separate private repo that holds only lore notes, like a locked filing cabinet next to the code. A repo listed under `stores:` is not a lore store: it has no `lore-store.yaml`, or it holds code. Lore refuses to clone or write to it:

<!-- example: errors#not-a-store -->
```console
# alice, in website
$ lore whoami
ERROR: [E_NOT_A_STORE] <redacted> (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files such as .gitignore, AGENTS.md, package.json, src/)
  hint: point stores: at a repo made with `lore store init <name> <url>`, or remove the entry from ~/.lore/config.yaml
(exit 19)

# alice, in ~
$ lore store doctor
ERROR: [E_INVALID_INPUT] 1 store(s) need attention: <redacted>: [E_NOT_A_STORE] <redacted> (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files such as .gitignore, AGENTS.md, package.json, src/)
  hint: fix each problem, then run `lore store doctor` again
(exit 20)
```
<!-- /example -->

Fix: remove the code repo from `stores:`. To make a store, create a new, empty repo and run `lore store init <name> <url>` on it. A code repo can never be a store.

## `E_ORG_AMBIGUOUS`

An **org** is one company setup in your config: its stores, its people and the repos it owns. Here two org entries match the repo at the same rule, so lore will not pick one:

<!-- example: errors#org-ambiguous -->
```console
# alice, in blog
$ lore whoami
org        refused  (the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3))
identity   (not resolved: the org is refused above)
machine    mch_fac0df3a
agent      none  (no coding agent detected)
project    prj_7a67…9504
remotes    github.com/client/blog
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
$ lore memory add --body 'the site deploys from the release branch'
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
```
<!-- /example -->

Fix, any one of:

- `lore org use <entry>` for this repo on this machine.
- `LORE_ORG=<entry>` for one command.
- Narrow the `own:` / `repos:` patterns so only one entry matches.

A related error, `E_ORG_DISAGREES`, means `LORE_ORG` or `lore org use` names a different org than the store link of this repo. Org-scoped writes are then refused unless you pass `--force-org`.

## `E_LINK_UNKNOWN`

A **link** tells lore that a store folder holds the agency's private notes for this client repo. In a repo that matches a client pattern, lore must know whether it is linked before it decides where a row goes. Sometimes the store cannot be read and there is no saved list of links: a new clone, a new machine, or the store is unreachable. Then writes without `--to` are refused:

<!-- example: errors#link-unknown -->
```console
# bob, in website
$ lore memory add --body 'the client wants weekly demos'
ERROR: [E_LINK_UNKNOWN] lore cannot tell whether this repo is a client repo: cannot read agency: github.com/agency/notes (never fetched)
  hint: retry when the store is reachable, or pass --to repo|<org>|me to choose the target yourself
(exit 18)
```
<!-- /example -->

Fix: retry when the store is reachable (check access with `lore store doctor`), or pass `--to repo`, `--to <org>` or `--to me` to choose the target yourself. Reads still work.

## Store branch does not exist

A staging entry (or a typo in `branch:`) names a store branch that does not exist. Lore never creates one by accident:

<!-- example: errors#store-branch-missing -->
```console
# alice, in website
$ LORE_ORG=agency-stag lore org link agency-stag --project client --yes
ERROR: [E_INVALID_INPUT] orgcfg: store branch does not exist: github.com/agency/notes has no branch staging
  hint: a store branch is made only on purpose; 0.1.18 makes stores with `lore store init <name> <url>` and ties repos to an org with `lore org create` / `lore org link` (v0.1.17 stores: `lore migrate run`)
(exit 20)
```
<!-- /example -->

Fix: if you meant it, create the branch once with `lore store init <name> '<url>?branch=<branch>'` (or attach an existing one with `lore store add <name> <url> --branch <branch>`); otherwise fix `branch:` in the config.

## `E_OFFLINE` on a claim, heartbeat or done

A **claim** is an agent putting its name on a task, like writing your name on a whiteboard job. A **heartbeat** says "still working" so the claim does not run out. These coordination writes need the remote to confirm them. They are never queued:

<!-- example: errors#offline-claim only=1 -->
```console
# alice, in website
$ lore task claim tsk_2ad3…f891
ERROR: [E_LINK_UNKNOWN] cannot tell whether this repo is a client repo; refusing to write coordination data
  hint: `lore whoami` shows the link status; fetch the store and retry
(exit 18)
```
<!-- /example -->

Fix: reconnect and run the command again. Ordinary writes behave differently:

- A memory or task written while the store is unreachable is committed in the local clone and pushed by a later command ("1 file(s) offline: committed in the local clone, pushed by the next command").
- Live writes in a code repo wait as "(live, not pushed yet: N waiting)".

`E_CONTENDED` (exit 11) on the same commands is different: the remote answered every time, but another writer got in first on every retry. Nothing was written; wait a few seconds and run the command again.

## `E_LOST_CLAIM`, `E_CANCELLED`, `E_REASSIGNED`

Your claim on a task is gone. Either the lease (the time a claim lasts without a heartbeat) ran out and another agent took the task over, or a person revoked, cancelled or reassigned it. The message says who holds it now. Stop working on that task; see [Agents](agents.md#fail-closed-errors-and-what-to-do).

`E_REFUSED` (exit 23) on a claim, cancel, reassign, revoke or done is not about the network: a fixed rule or the task's state says no. An agent tried something only a person may do (cancelling, reassigning or revoking, or finishing someone else's claim; the `--json` error has `"needs_person": true`), the WIP limit is reached, the task is poisoned (its claim ran out too many times, so it needs a person), or it is no longer open. Retrying will not help: hand it to a person, or pick other work.

## `E_LEAK_BLOCKED`

A **leak** is internal or personal content reaching something the client can read. A commit, a live write to a client repo or a pull request text contains internal content: a row id of another project, a pointer to an internal row, or a run of 8 or more words that also appears in an internal or personal note. The message names the file and line, the matching note, and the two ways out:

<!-- example: agency#leak-blocked only=2 -->
```console
# alice, in website
$ git commit -m 'add notes'
ERROR: [E_LEAK_BLOCKED] blocked, this commit would share internal content with the client repo:
  commit src/notes.ts                      matches internal note mem_05e9…4603 (10 words)
  hint: Remove the internal text from src/notes.ts
(exit 1)
```
<!-- /example -->

- The text should not be there: remove it, or `lore move <id> --to <org>` if a note was filed in the wrong place.
- When the leaking file is a lore row of this repo (a rule, memory or task file), the hint names that row: `lore move <row> --to <org>` to keep it internal, or `lore <kind> delete <row> --confirm` to remove it.
- When a `lore <kind> add --to repo` is refused, the hint says "Nothing written": nothing was saved; write it with `--to <org>` instead.
- It really should be shared with the client: `lore move <id> --to repo` (it keeps the id; see [Sharing an internal note](use-cases/sharing-a-note.md)).
- `lore check leaks --explain` shows the rule and the matched words.

The same check runs again at push (the `pre-push` hook), on every commit the push would send; there the fix is to rewrite the commits that carry the text (`git commit --amend`, or `git rebase`). Never bypass either check with `git commit --no-verify` or `git push --no-verify` in a client repo: they skip every local check, and only `lore check leaks --ci` in the client's CI (ids, not sentences) is left. If something already reached the client, follow [Cleaning up after a leak](use-cases/leak-cleanup.md).

## `E_META_STORE_UNREADABLE`

In a client repo, a write that carries metadata (`--meta` on an add or edit, `lore meta set`, `lore meta unset`) is refused while an org store has no local copy and cannot be reached. Lore has to check the metadata against the store's own keys, so an internal key never ends up in the client repo, and with the store unreadable it cannot. Nothing is written. Go online once (or run `lore sync`) so the store is kept locally, then write again. The same write without metadata answers `E_LEAK_BLOCKED` ("cannot check") for the same reason.

## "live, not pushed yet": the host refuses `refs/lore/*`

Lore shares live rows through a hidden list on the remote that is not a branch (git calls it a ref): `refs/lore/live`. Some git hosts or server hooks accept only branches and tags. Live rows then stay on your machine, and `lore doctor` says why:

<!-- example: errors#refs-refused-by-the-host -->
```console
# bob, in portal
$ lore task add 'set up the portal CI'
✓ tsk_54fe…28f2 — set up the portal CI [medium]
lore live: (live, not pushed yet: 4 waiting; the remote refused refs/lore/live: [remote rejected] (pre-receive hook declined))
$ lore live diff
local     601d2850ffd0
tracking  (none)
seen      (none)
pending   4 write(s) not confirmed by the remote
  _root/actors/act_05e9…4603.json
  _root/projects/prj_c0f3…b5b8.json
  _root/task_lists/tlt_76bd…3f10.json
  _root/tasks/tsk_54fe…28f2.json
lore live: (live, not pushed yet: 4 waiting; the remote refused refs/lore/live: [remote rejected] (pre-receive hook declined))
$ lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 15s ago
  - live: 4 live write(s) not pushed yet
  - live: 4 row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach main at live.max_age)
  - live: 3 row(s) waiting to land; the next live write or `lore sync` lands them
  - live: live ref: 1 commit(s) since the last compaction (epoch 1; automatic past 1000)
  - live: post-commit and pre-push hook blocks installed
  - live: the remote refused a push to refs/lore/live, so live rows stay on this machine (the host may allow only branches and tags): [remote rejected] (pre-receive hook declined)
lore live: (live, not pushed yet: 4 waiting; the remote refused refs/lore/live: [remote rejected] (pre-receive hook declined))
```
<!-- /example -->

Fix: allow `refs/lore/*` on the host (or in its pre-receive hook). Until then, live rows only reach teammates when they land on `main`. `lore land --all` lands them now. Setting `live.autopush: false` stops the retries.

## `whoami` prints `<redacted>`

<!-- ds:block id=team-ci-markers-t4gm77bb -->
Without a terminal (a script, CI, an agent's tool call) and whenever a CI variable is set (`CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`, `JENKINS_URL`, `TF_BUILD`, `BITBUCKET_BUILD_NUMBER`), `whoami`, `doctor` and `explain` hide store URLs and internal folder names, so they never end up in a log the client can read:

<!-- example: agency#link only=7 -->
```console
# alice, in website
$ lore whoami
org        agency  (matched by: link in <redacted>, project id + remote)
identity   Alice Doe <human:alice@agency.example>   act_2ad3…f891  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
machine    mch_fac0df3a
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
stores     <redacted> on branch main  (org folder <redacted>)
internal   <redacted> <redacted>/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
```
<!-- /example -->

On your own terminal they are printed. `--share` prints them on a terminal on purpose.

## `E_INVALID_ID`

The id you typed does not have the shape of a lore id (a prefix, `_`, then 32 hex characters, such as `tsk_…`): usually a typo or an id cut short. Lore says so (exit 20) instead of answering "not found", so a script can tell a bad id from a row that is really gone (`E_NOT_FOUND`). Copy the id again from `list` output.

## `E_SYMLINK_DB`: a lore file is a symbolic link

Lore never writes its own files through a symbolic link, because a repository can carry a link that points anywhere on your disk. It refuses (exit 23) when any of these is a link: the local cache (`.lore/lore-cache.db` and its side files), the machine store and local state under `~/.lore`, the store lock `.lore/lock`, and the audit files (`.lore/state/audit-v2.jsonl` and the per-store and batch logs). Delete the link; lore rebuilds the cache from `.lore/data` on the next command. Two related files answer `E_REFUSED` instead: `.lore/LORE.md` may be a link only while it stays inside the repository, and `.gitignore` is never written through a link that leads out of it. A folder above these files that is a link (your own layout, such as a repo under a linked `~/work`) is fine.

## "Database is locked" and slow commands

Lore keeps a local cache (`.lore/lore.db`) and takes a short lock while it writes. Two lore commands at once in one checkout (a hook and your own command, say) wait for each other. `E_LOCK_HELD` (another lore process holds the project lock), `E_DB_LOCKED` or `E_BUSY_TIMEOUT` means the wait ran out. These three are in the [error reference](../reference/cli-commands.md#errors); unlike the other errors on this page, the example script does not reproduce them. Run the command again; `lore doctor` checks the cache. A claim or a live write is a trip to the remote and back, about one to two seconds. `--trace` shows where the time goes.

## FAQ

**Alice creates a task for Bob (another agency developer) on her branch. Does Bob get it only after a merge to `main`?** No. Tasks are live: they are pushed to `refs/lore/live` at once. Every clone sees them on its next command (cached up to 60 seconds), whatever branch anyone is on. They land on `main` (are copied into it) when finished.

**Is `refs/lore/live` a branch?** No. It is a git ref outside `refs/heads/`: fetched and pushed like a branch, but `git branch` does not list it and nobody checks it out. It is hidden, not private.

**How can lore push a commit without changing my branch?** A commit does not need a branch; a branch is only a bookmark. Lore builds the commit with git's low-level commands (its own scratch staging area, `commit-tree`) and moves `refs/lore/live` to it. Your branch, staged files and working folder are never touched.

**Will tasks no longer be stored in `.lore/data`?** They will. `main`'s `.lore/data` is the lasting home of every row, tasks included. Live is temporary sharing until the task is finished and landed.

**Can I keep rules and memories only for me?** Yes: `--to me` writes to your personal store, filed by the org you are in. See [Personal notes](use-cases/personal-notes.md).

**The client can read the client repo. How do internal notes stay hidden but still tied to the project?** They live in your company's store, in an internal project linked to the client repo. The link is stored only in the store, and the internal render and cache live outside the checkout. See [Agency with a client repo](use-cases/agency-client-repo.md).

**One store, or one per client?** Both are plain config. One store per client is the way to keep a contractor away from other clients' notes, because GitHub cannot restrict read access to a folder. See [Contractor limited to one client](use-cases/contractor.md).

**How do several repos work as one product?** Link them all to one internal project (a group); each task says which repo it is for. See [Several repos as one product](use-cases/group-project.md).

**Where does the roster live, and how does each person get an id?** In the org folder of the org's store (`actors/`, `teams/`). `lore actor register` adds you; your id comes from your identity key (your email address), so it is the same on every machine.

**I work for two companies. Can their notes mix?** No. A repo loads exactly one org; your personal notes are filed per org. See [One person in two companies](use-cases/two-companies.md).

**Is `lore.db` per repo?** Yes, one per checkout, holding this branch's and this repo's live rows only. Store and personal rows are cached outside the checkout in `~/.lore/cache/<project-id>.db`. Both can be rebuilt from the files at any time.

**What if GitHub's "Download ZIP", a fork or a shallow clone is used?** They do not carry `refs/lore/*`. That is why live rows are temporary and open rows are copied to `main` at their max age (7 days). `lore doctor` warns when rows exist only in live.

**What do hooks not protect against?** `--no-verify`, edits in the GitHub web interface, teammates without lore, reworded text, text pasted into PR bodies or chat without `lore check pr-body`, client-owned machines with your stores configured, and backups of `~/.lore`. The guards catch copy-paste mistakes; they are not a wall.

**Can two agents end up working on the same task?** No. A claim is a compare-and-swap push: git accepts it only if nobody changed the list since lore read it, so exactly one agent wins. A holder whose lease ran out is fenced out (lore refuses its writes with `E_LOST_CLAIM`) and cannot finish the task.

**Do heartbeats fill the repo with commits?** No. Signals live on `refs/lore/signals`, which always holds exactly one commit; each heartbeat replaces it.

## Pitfalls

- **Read the hint line.** Every lore error prints its code, a message, and a `hint:` with the next step.
- **Exit statuses mean something.** Scripts and agents should branch on them (see [Commands › error codes](commands.md#error-codes-and-exit-statuses)), not on the message text.
