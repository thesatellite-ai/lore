# Troubleshooting

**In short:** each section below starts with an error message you may see. It says what causes it in plain words and gives the fix. The questions people ask most are at the end.

Every example output below is real: it was produced on purpose by `tests/team/docs/run-examples.sh errors`. A few sections have no example; their text is checked against the code instead.

First steps for anything odd:

- `lore whoami` (add `--explain`): which org, which identity, which store, and why.
- `lore explain --id <id>`: every copy of a row, which one won, and its landing decision.
- `lore doctor`: hooks, fetch settings, live rows waiting, clock skew, stores.
- `lore store doctor`: every store this repo and machine use (`.lore/stores.yaml`, the org's stores, your personal store `me`): reachable, unpushed commits and how old the oldest is, a refused push, the clone folders; then every v0.1.17 org store (`orgs[].stores` in `~/.lore/config.yaml`). `lore sync status` prints one line per store: ahead and behind its remote, and the last push. When two people edited the same row, the conflict is listed once, under `refused pushes`, for the store folder that holds the row; the store's other folders are listed under `held pushes` as `held: waiting on <folder>'s conflict (<row>)`, because one clone pushes every folder of a store at once. Settle that one row and run `lore sync`; every folder's commits go out together.
- `--trace` on any command (or `LORE_TRACE=1`): every git command lore runs for live rows, with timing; a fetch, push or ls-remote to a store also names the host it reached (`remote git@github-work` when a setup with a host alias owns the store), so a call that went out as the wrong account shows.

For example, the fetch a `lore sync` makes (the time is shown as `<time>` here):

<!-- example: live-repair#trace -->
```console
# alice, in billing
$ lore sync --trace 2>&1 | grep -e '^trace: git fetch' -e '^trace: git push' -e '^·'
trace: git fetch --quiet --no-tags --no-write-fetch-head --refmap= --prune -- origin +refs/lore/*:refs/lore-remote/origin/* +refs/heads/main:refs/remotes/origin/main (<time>, ok)
trace: git push --porcelain --no-verify -- origin 05513aa29b78:refs/lore/live (<time>, ok)
· already in sync
```
<!-- /example -->

## A teammate's task or `@handle` does not show up

Lore fetches once per command and reuses that fetch for 5 minutes (`live.fetch_ttl: 5m`). `lore sync` always fetches, and it also refreshes the store link index, so a link a teammate just made shows up in `lore whoami` after it. So a row a teammate wrote seconds ago may not be in your view yet. Also, `--assign @alice` fails with `E_NOT_FOUND: no person or team with handle @alice` until Alice (an agency developer) has registered and you have fetched since. Run `lore sync`, which always fetches:

<!-- example: small-team#assign-across-branches only=2,3 -->
```console
# bob, in billing
$ lore sync
lore live: pushed waiting live writes commit cf563f3 to origin refs/lore/live
lore live: 1 row(s) left live (main holds them)
lore live: landed on main; this checkout is on feat/payments, so those rows show here after you merge main
lore live: pushed waiting live writes commit df3a55c to origin refs/lore/live

# alice, in billing
$ git switch -q -c feat/login && lore task list --mine
(no tasks)
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
ERROR: [E_NOT_A_STORE] github.com/client2/docs (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files: AGENTS.md, package.json, src/index.ts)
  hint: point stores: at a repo made with `lore store init --name <name> --url <url>`, or remove the entry from ~/.lore/config.yaml
(exit 19)

# alice, in ~
$ lore store doctor
ERROR: [E_INVALID_INPUT] 1 store(s) need attention: git@github.com:client2/docs: [E_NOT_A_STORE] github.com/client2/docs (branch main) is listed under stores: but is not a lore store (no lore-store.yaml; it holds non-lore files: AGENTS.md, package.json, src/index.ts)
  hint: fix each problem, then run `lore store doctor` again
(exit 20)
```
<!-- /example -->

Fix: remove the code repo from `stores:`. To make a store, create a new, empty repo and run `lore store init --name <name> --url <url>` on it. A code repo can never be a store.

## `E_ORG_AMBIGUOUS`

An **org** is one company setup in your config: its stores, its people and the repos it owns. Here two org entries match the repo at the same rule, so lore will not pick one:

<!-- example: errors#org-ambiguous -->
```console
# alice, in blog
$ lore whoami
org        refused  (the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3))
identity   (not resolved: the org is refused above)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_7a67…9504
remotes    github.com/client/blog
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3)
  hint: choose one with `lore org use --entry <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
$ lore memory add --body 'the site deploys from the release branch'
lore memory add: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/blog/.git/config; hook blocks in ~/work/blog/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: the command was refused, so what it changed first is undone (.git/config, .git/hooks/commit-msg, .git/hooks/post-checkout, .git/hooks/post-commit, .git/hooks/post-merge, .git/hooks/post-rewrite, .git/hooks/pre-commit, .git/hooks/pre-push, .git/lore/git-wiring); the next write that goes through sets it up again
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, client3) (candidates: agency, client3)
  hint: choose one with `lore org use --entry <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
```
<!-- /example -->

Fix, any one of:

- `lore org use --entry <entry>` for this repo on this machine.
- `LORE_ORG=<entry>` for one command.
- Narrow the `own:` / `repos:` patterns so only one entry matches.

A related error, `E_ORG_DISAGREES`, means `LORE_ORG` or `lore org use` names a different org than the store link of this repo. Org-scoped writes are then refused unless you pass `--force-org`.

## `E_LINK_UNKNOWN`

A **link** tells lore that a store folder holds the agency's private notes for this client repo. In a repo that matches a client pattern, lore must know whether it is linked before it decides where a row goes. Sometimes the store cannot be read and there is no saved list of links: a new clone, a new machine, or the store is unreachable. Then writes without `--to` are refused:

<!-- example: errors#link-unknown -->
```console
# bob, in website
$ lore memory add --body 'the client wants weekly demos'
lore memory add: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/website/.git/config; hook blocks in ~/work/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: the command was refused, so what it changed first is undone (.git/config, .git/hooks/commit-msg, .git/hooks/post-checkout, .git/hooks/post-commit, .git/hooks/post-merge, .git/hooks/post-rewrite, .git/hooks/pre-commit, .git/hooks/pre-push, .git/lore/git-wiring); the next write that goes through sets it up again
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
$ LORE_ORG=agency-stag lore org link --org agency-stag --project client --yes
ERROR: [E_INVALID_INPUT] orgcfg: store branch does not exist: github.com/agency/notes has no branch staging
  hint: a store branch is made only on purpose; 0.1.18 makes stores with `lore store init --name <name> --url <url>` and ties repos to an org with `lore org create` / `lore org link` (v0.1.17 stores: lore fix)
(exit 20)
```
<!-- /example -->

Fix: if you meant it, create the branch once with `lore store init --name <name> --url '<url>?branch=<branch>'` (or attach an existing one with `lore store add --name <name> --url <url> --branch <branch>`); otherwise fix `branch:` in the config.

## `E_OFFLINE` on a claim, heartbeat or done

A **claim** is an agent putting its name on a task, like writing your name on a whiteboard job. A **heartbeat** says "still working" so the claim does not run out. These coordination writes need the remote to confirm them. They are never queued:

<!-- example: errors#offline-claim only=1 -->
```console
# alice, in website
$ lore task claim --id tsk_2ad3…f891
ERROR: [E_OFFLINE] could not confirm the coordination write (remote unreachable): git fetch: '/srv/git/github.com/agency/notes' does not appear to be a git repository
  hint: coordination writes (claims, releases, holds) must reach the remote so two people never hold the same work, and are never queued; run the command again once the remote is reachable (`lore whoami` shows the store's status)
(exit 15)
```
<!-- /example -->

Fix: reconnect and run the command again. Ordinary writes behave differently:

- A memory or task written while the store is unreachable is committed in the local clone and pushed by a later command ("1 file(s) offline: committed in the local clone, pushed by the next command").
- Live writes in a code repo wait as "(live, not pushed yet: N waiting)".

`E_CONTENDED` (exit 11) on the same commands is different: the remote answered every time, but another writer got in first on every retry. Nothing was written; wait a few seconds and run the command again.

## `E_LOST_CLAIM`, `E_CANCELLED`, `E_REASSIGNED`

Your claim on a task is gone. Either the lease (the time a claim lasts without a heartbeat) ran out and another agent took the task over, or a person revoked, cancelled or reassigned it. The message says who holds it now. Stop working on that task; see [Agents](agents.md#fail-closed-errors-and-what-to-do).

Before you see `E_LOST_CLAIM`, lore has already checked it is not stale. Lore reuses a fetch for `live.fetch_ttl` (5 minutes by default), so the run may not have fetched. When a coordination command (claim, heartbeat, merged, note, release, revoke, reassign, gate request, gate approve) gets `E_LOST_CLAIM` in such a run, lore fetches once, takes in the teammates' changes and runs the command again. The answer then comes from the remote's task row: `E_CANCELLED` after a person's cancel, `E_REASSIGNED` after a reassign, `E_LOST_CLAIM` again after a revoke (with the revoke's detail). The retry is safe because a refused command wrote nothing.

## `E_NOT_FOUND` on a task id a teammate just added

A task a teammate added after your clone's last fetch is not missing for long. When a command on one task or gate id (claim, heartbeat, merged, note, release, revoke, reassign, gate request and approve, `task show`, `task done`, `task cancel`, checklist, time and recur commands, and every other task or gate command whose first argument is the id) answers `E_NOT_FOUND` in a run that did not fetch, lore fetches once, takes in the teammates' changes and runs the command again. It happens only on that failure path and only once, and never after the command has written anything (the id is looked up before any write). A well-formed id that no row has stays `E_NOT_FOUND` (exit 27) after that one fetch: check the id, or run `lore sync`.

`E_REFUSED` (exit 23) on a claim, cancel, reassign, revoke or done is not about the network: a fixed rule or the task's state says no. An agent with no person behind it (an agent actor whose roster row names no owner) tried a human override (cancelling, reassigning or revoking, or finishing someone else's claim; the `--json` error has `"needs_person": true`; give it an owner with `lore agent start --label <l> --owner @<person>`, or a person runs it; a person's own coding agent acts for them and passes), the WIP limit is reached, the task is poisoned (its claim ran out too many times, so it needs a person), or it is no longer open. Retrying will not help: hand it to a person, or pick other work.

## A row I just wrote is gone on my feature branch

A write printed `lore live: landed on main; this checkout is on <branch>, so those rows show here after you merge main`. The row landed on `main` and left live. Lore reads landed rows from `origin/main` only while the checkout is on the default branch, so on `<branch>` the row shows again once you merge main (`git merge origin/main`). See [How it works › Landing](how-it-works.md#landing).

## `E_LEAK_BLOCKED`

A **leak** is internal or personal content reaching something the client can read. A commit, a live write to a client repo or a pull request text contains internal content: a row id of another project, a pointer to an internal row, or a run of 8 or more words that also appears in an internal or personal note. The message names the file and line, the matching note, and the two ways out:

<!-- example: agency#leak-blocked only=2 -->
```console
# alice, in website
$ git commit -m 'add notes'
ERROR: [E_LEAK_BLOCKED] blocked, this commit would share internal content with the client repo:
  commit src/notes.ts                      matches internal note mem_c0f3…b5b8 (10 words)
  hint: Remove the internal text from src/notes.ts
(exit 1)
```
<!-- /example -->

- The text should not be there: remove it, or `lore move --id <id> --to <org>` if a note was filed in the wrong place.
- When the leaking file is a lore row of this repo (a rule, memory or task file), the hint names that row: `lore move --id <row> --to <org>` to keep it internal, or `lore <kind> delete <row> --confirm` to remove it.
- When a `lore <kind> add --to repo` is refused, the hint says "Nothing written": nothing was saved; write it with `--to <org>` instead.
- It really should be shared with the client: `lore move --id <id> --to repo` (it keeps the id; see [Sharing an internal note](use-cases/sharing-a-note.md)).
- `lore check leaks --explain` shows the rule and the matched words.
- When the file is a team profile row (`actor_contacts`, `actor_periods`, `project_members`, `roles` or `contact_types` under `.lore/data/`) or an actor file with a profile field, the message says "team profile rows live only in the org store; remove the file". Remove the file from the commit: those rows belong only in the org's store (see [Team profiles](profiles.md#client-repos)).

The same check runs again at push (the `pre-push` hook), on every commit the push would send; there the fix is to rewrite the commits that carry the text (`git commit --amend`, or `git rebase`). Never bypass either check with `git commit --no-verify` or `git push --no-verify` in a client repo: they skip every local check, and only `lore check leaks --ci` in the client's CI (ids, not sentences) is left. If something already reached the client, follow [Cleaning up after a leak](use-cases/leak-cleanup.md).

## `E_META_STORE_UNREADABLE`

In a client repo, a write that carries metadata (`--meta` on an add or edit, `lore meta set`, `lore meta unset`) is refused while an org store has no local copy and cannot be reached. Lore has to check the metadata against the store's own keys, so an internal key never ends up in the client repo, and with the store unreadable it cannot. Nothing is written. Go online once (or run `lore sync`) so the store is kept locally, then write again. The same write without metadata answers `E_LEAK_BLOCKED` ("cannot check") for the same reason.

## "live, not pushed yet": the host refuses `refs/lore/*`

Lore shares live rows through a hidden list on the remote that is not a branch (git calls it a ref): `refs/lore/live`. Some git hosts or server hooks accept only branches and tags. Live rows then stay on your machine, and `lore doctor` says why:

<!-- example: errors#refs-refused-by-the-host -->
```console
# bob, in portal
$ lore task add --title 'set up the portal CI'
lore: wrote 2 row file(s) in ~/.lore/personal/everywhere (store me)
lore: wrote 2 row file(s) in ~/work/portal/.lore/data
✓ tsk_54fe…28f2 — set up the portal CI [medium]
  created tasklist tlt_05e9…4603 "default" for tasks added without --tasklist
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore live diff
local     86dbae476bd6
tracking  (none)
seen      (none)
pending   2 write(s) not confirmed by the remote
  _root/task_lists/tlt_05e9…4603.json
  _root/tasks/tsk_54fe…28f2.json
$ lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true
  - live: tracking refspec installed, not forced into refs/lore/*
  - live: last fetch 15s ago
  - live: 2 live write(s) not pushed yet
  - live: 2 row(s) exist only in live (forks, ZIP downloads and shallow clones do not carry refs/lore/*; open rows reach main at live.max_age)
  - live: 1 row(s) waiting to land; the next live write or `lore sync` lands them
  - live: live ref: 1 commit(s) since the last compaction (epoch 1; automatic past 1000)
  - live: post-commit and pre-push hook blocks installed
```
<!-- /example -->

Fix: allow `refs/lore/*` on the host (or in its pre-receive hook). Until then, live rows only reach teammates when they land on `main`. `lore land --all` lands them now. Setting `live.autopush: false` (the default) stops the retries after each write.

## A URL shows `<redacted>`

<!-- ds:block id=team-ci-markers-ec4tqnjs -->
lore prints store URLs, org folders and local paths as they are, on a terminal, in a script, in CI (`CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`, `JENKINS_URL`, `TF_BUILD`, `BITBUCKET_BUILD_NUMBER`) and in an agent's tool call alike: a store's address is not a secret. Only a credential inside a URL is replaced, and only that part: `https://user:<redacted>@host/org/x.git` for a password, `https://<redacted>@host/org/x.git` for a token used as the user name, `?access_token=<redacted>` for a token in the query. If you see `<redacted>` in a URL, the URL in your config holds a credential: remove it and use your git credential helper. The same `whoami` output, run without a terminal:

<!-- example: agency#link only=7 -->
```console
# alice, in website
$ lore whoami
org        agency  (matched by: link in github.com/agency/notes client/, project id + remote)
identity   Alice Doe <human:alice@agency.example>   act_2ad3…f891  @alice  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder org)
internal   github.com/agency/notes client/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
```
<!-- /example -->

`--share` is kept for older scripts and changes nothing.

## `E_INVALID_ID`

The id you typed does not have the shape of a lore id (a prefix, `_`, then 32 hex characters, such as `tsk_…`): usually a typo or an id cut short. Lore says so (exit 20) instead of answering "not found", so a script can tell a bad id from a row that is really gone (`E_NOT_FOUND`). Copy the id again from `list` output.

## `E_SYMLINK_DB`: a lore file is a symbolic link

Lore never writes its own files through a symbolic link, because a repository can carry a link that points anywhere on your disk. It refuses (exit 23) when any of these is a link: the local cache (under `~/.lore/cache/`, with its side files), the machine store and local state under `~/.lore`, the store lock `.lore/lock`, and the audit files (`.lore/state/audit-v2.jsonl` and the per-store and batch logs). Delete the link; lore rebuilds the cache from `.lore/data` on the next command. Two related files answer `E_REFUSED` instead: `.lore/LORE.md` may be a link only while it stays inside the repository, and `.gitignore` is never written through a link that leads out of it. A folder above these files that is a link (your own layout, such as a repo under a linked `~/work`) is fine.

## Team profile errors

The sections below are about [team profiles](profiles.md), which are in development for lore 0.1.18 and not released yet; their text follows the design, and none has an example produced by the script yet.

### `E_CONTACT_TAKEN`

An identity contact can belong to only one person. `lore actor contact add --actor … --identity` answers `E_CONTACT_TAKEN` when that value (after normalising: `Mira.Patel@Gmail.com` and `mira.patel@gmail.com` are the same) is already an identity contact of another person, or when that email already has its own actor on the roster, which happens when someone committed from it before it was added to their profile.

- Run `lore actor show --id email:mira.patel@gmail.com` (or `github:<login>`, …) to see who holds it.
- It is the same person, recorded twice: rerun the add with `--merge`. Lore shows how many rows the other actor has and asks; without a terminal, rerun with `--merge --yes`. The other actor is marked as merged into this one; no row is rewritten.
- It is the wrong person: remove the contact from them with `lore actor contact remove --id <acn_id>`, then add it again.

`lore doctor` reports "identity contacts held by two actors" when two clones each added the same value to different people. Until that is fixed, lore matches neither of them for that value: it never guesses.

### `E_CONTACT_TYPE`

The contact type is unknown or retired, the value fails the type's check (an email without `@`, a phone with fewer than 7 digits, a URL that is not `http` or `https`), or `--identity` was given on a type that is not an identity type (only `email`, `github`, `gitlab` and types added with `--identity` are). Run `lore contact-type list` to see the types; add one with `lore contact-type add --name <name> --kind email|handle|phone|url|text`, or bring a retired one back with `lore contact-type edit --name <name> --unretire`.

### `E_ROLE_UNKNOWN`

A `--role` name on `lore project member add` or `edit` is not in the role list, or is retired. Run `lore role list`; add the role with `lore role add --name <name>`, or bring a retired one back with `lore role edit --name <name> --unretire`. Role names are compared as written, in snake_case.

### `E_STATUS_CHANGE`

The status change is not one `lore actor status` makes, or the person is in the wrong status for the command. The message names the command to use instead:

| You ran | Use instead |
|---|---|
| `lore actor status --actor <a> --to left` or `archived` on an active or on-leave person | `lore actor offboard --actor <a>` (add `--archive` to archive) |
| `lore actor status --actor <a> --to active` on a left or archived person | `lore actor rejoin --actor <a> --type …` |
| `lore actor offboard` on someone who already left ("already left on <date>") | nothing to do; `lore actor status --actor <a> --to archived` to archive |
| `lore actor rejoin` on someone who is active | nothing to do |
| `lore project member add` for a left or archived person | `lore actor rejoin --actor <a>` first |
| `lore actor register` or `lore agent start` by a person who left or was archived | ask an admin to run `lore actor rejoin --actor <a> --type …` |

### `E_PERIOD_OVERLAP`

A date would make two company periods overlap, end a period before it starts, or (offboarding) end the current stint before the day it started; a person also has at most one open stint. Run `lore actor period list --actor <actor>` and pick a date after the last stint ended, or fix a mistyped date with `lore actor period edit --period <apd_id>`.

### `E_NEEDS_CONFIRM` on offboard, rejoin or `--merge`

`lore actor offboard`, `lore actor rejoin` and `lore actor contact add --merge` show what they will change and ask first. Without a terminal (a script, an agent, CI) they write nothing and answer `E_NEEDS_CONFIRM` with that preview. Show it to the person, then rerun the same command with `--yes`. With `--json`, the preview is in the error's message.

### `E_POLICY_DENIED` naming `agents.may_manage_people`

An agent ran a command that changes profiles, contacts, statuses, stints, memberships, roles or contact types, and a team set the policy flag `agents.may_manage_people` to false. The flag is true by default (an agent acts for its person), so this refusal only comes from a team's choice; `lore policy show --id agents.may_manage_people` names the level that set it. Hand the change to a person, or have a person unset the flag at that level (`lore policy unset --key agents.may_manage_people --to <store>/org`) if agents should manage people. Reading (`show`, `list`) is always allowed. Creating an org or a team is never refused by this flag: the new store has no policy yet.

### "has no person behind it"

A coding agent acts for its person: what it changes on the roster (the team step of an admin setup, `lore actor register`, `lore agent start --label`) is made as that person, and the rows record the agent as the writer. lore refuses only an agent with no person behind it: an agent actor (`LORE_ACTOR=agent:<label>`) whose roster row names no owner. Its person runs the command in their own terminal, or registers the agent with an owner: `lore agent start --label <label> --owner @<person>`. A team that wants roster changes left to people sets `agents.may_manage_people` to false (see above).

### `E_REFUSED` "team profiles live in an org store"

The repo has no org, so its roster is the repo's own committed `.lore/data`, and contacts written there would be published with the code. Profile writes are refused there; reads still work. Create an org with `lore org create --org <name>` or join one (see [Identity and stores](identity-and-stores.md)), then run the command again.

## Where a store's clone lives

[Stores as plain folders](../sync/store-clones.md) walks through a clone step by step; this section is the short version.

Each git store has one clone on your machine, a normal git clone you can open and browse: `~/.lore/stores/<owner>/<repo>/` (the host is not in the path; a git repository on this machine, such as `/srv/git/notes.git`, is cloned at `~/.lore/stores/_local/notes/`, and a second one named `notes` at `notes~2`; a clone an earlier build made at the repository's whole path moves there on the next command). Its files are the store's files; `.git/` inside is git's own, and `.git/lore/` holds what lore keeps for itself: the store lock (`lock`), the pending audit journal (`state/`), and the fetch and push stamps. Nothing of lore's sits among the store's files. A store pinned to a branch is its own clone at `<repo>@<branch>/`. Two stores whose paths agree on different hosts get `<repo>/` and `<repo>~2/`; `~/.lore/state/store-clones.json` records the second one. To use a clone you made yourself (`git clone <url> ~/work/notes`), run `lore store use --path ~/work/notes`; `lore store use --default <store>` goes back to lore's own clone and leaves yours alone.

Inside the clone a folder's rows sit right under it: `client/tasks/tsk_….json`, `client/_meta.json` (the folder's id and repo links), `org/lore-org.yaml` (the org's suggested settings), and the store's own rows (its root folder) in `_store/` (`_store/tasks/`, `_store/memories/`, …, `_store/_meta.json`), so every other top-level folder is a project or the org folder. There is no `.lore/data` inside a store; a code repo keeps its own rows in `.lore/data`, apart from its code. A store folder may not be named like a kind of row, nor start with `_` or `.` (`_store` is lore's): `lore store folder add --store <store> --folder tasks` is refused with the list of reserved names. Lore writes a `README.md` into each store once, with its next commit there, explaining the folders and how to edit a row by hand (`lore edit --id <id>`, `lore path --id <id>`); it never writes it again, and `lore doctor` notes a store without one (`store_readme`). You may edit, add, delete or move row files there by hand, or use plain git: the next lore command checks each change, commits the valid ones as you and refuses a broken file with its line and reason ([Editing by hand](../sync/editing-by-hand.md)); `lore doctor` names files there that are not rows and row files still waiting to be committed (`store_files`). A store that is a plain folder (`file://…`) keeps lore's lock and pending journal in `.lore-state/` at its top.

`lore doctor` names a clone only when lore cannot use it as it is (check `store_clone`), with the command that fixes it: a clone whose git data is broken (`rm -rf` the folder after copying out anything you still need; the next command clones again), a first clone that was cut short before its files were checked out (lore finishes it on the next open, or `lore store forget --url <url>`), or your own clone recorded by `lore store use` whose folder is gone (`lore store use --default <url>`). Doctor never deletes a clone folder itself, `--fix` included.

## "Database is locked" and slow commands

Lore keeps a local cache (under `~/.lore/cache/`) and takes a short lock while it writes. Two lore commands at once in one checkout (a hook and your own command, say) wait for each other. `E_LOCK_HELD` (another lore process holds the project lock), `E_DB_LOCKED` or `E_BUSY_TIMEOUT` means the wait ran out. These three are in the [error reference](../reference/cli-commands.md#errors); unlike the other errors on this page, the example script does not reproduce them. Run the command again; `lore doctor` checks the cache. A claim or a live write is a trip to the remote and back, about one to two seconds. `--trace` shows where the time goes.

## FAQ

**Alice creates a task for Bob (another agency developer) on her branch. Does Bob get it only after a merge to `main`?** No. Tasks are live: Alice's `lore sync` or your next `git push` pushes them to `refs/lore/live` (lore never pushes by itself unless `live.autopush` is on). Every clone then sees them after its next fetch (`lore sync` fetches at once), whatever branch anyone is on. A finished task lands on `main` (is copied into it) on the next `lore sync`.

**Is `refs/lore/live` a branch?** No. It is a git ref outside `refs/heads/`: fetched and pushed like a branch, but `git branch` does not list it and nobody checks it out. It is hidden, not private.

**How can lore push a commit without changing my branch?** A commit does not need a branch; a branch is only a bookmark. Lore builds the commit with git's low-level commands (its own scratch staging area, `commit-tree`) and moves `refs/lore/live` to it. Your branch, staged files and working folder are never touched.

**Will tasks no longer be stored in `.lore/data`?** They will. `main`'s `.lore/data` is the lasting home of every row, tasks included. Live is temporary sharing until the task is finished and landed.

**Can I keep rules and memories only for me?** Yes: `--to me` writes to your personal store, filed by the org you are in. See [Personal notes](use-cases/personal-notes.md).

**The client can read the client repo. How do internal notes stay hidden but still tied to the project?** They live in your company's store, in an internal project linked to the client repo. The link is stored only in the store, and the internal render and cache live outside the checkout. See [Agency with a client repo](use-cases/agency-client-repo.md).

**One store, or one per client?** Both are plain config. One store per client is the way to keep a contractor away from other clients' notes, because GitHub cannot restrict read access to a folder. See [Contractor limited to one client](use-cases/contractor.md).

**How do several repos work as one product?** Link them all to one internal project (a group); each task says which repo it is for. See [Several repos as one product](use-cases/group-project.md).

**Where does the roster live, and how does each person get an id?** In the org folder of the org's store (`actors/`, `teams/`). `lore actor register` adds you; your id comes from your identity key (your email address), so it is the same on every machine.

**I work for two companies. Can their notes mix?** No. A repo loads exactly one org; your personal notes are filed per org. See [One person in two companies](use-cases/two-companies.md).

**Is the cache per repo?** Yes, one file per checkout, `~/.lore/cache/repo/<repo folder>-<hash>.db`, holding this branch's and this repo's live rows only; nothing of it is in the checkout. Store rows are cached per store folder in `~/.lore/cache/stores/<store id>/<branch>/<folder>.db`. Both can be rebuilt from the files at any time.

**What if GitHub's "Download ZIP", a fork or a shallow clone is used?** They do not carry `refs/lore/*`. That is why live rows are temporary and open rows are copied to `main` at their max age (7 days). `lore doctor` warns when rows exist only in live.

**What do hooks not protect against?** `--no-verify`, edits in the GitHub web interface, teammates without lore, reworded text, text pasted into PR bodies or chat without `lore check pr-body`, client-owned machines with your stores configured, and backups of `~/.lore`. The guards catch copy-paste mistakes; they are not a wall.

**Can two agents end up working on the same task?** No. A claim is a compare-and-swap push: git accepts it only if nobody changed the list since lore read it, so exactly one agent wins. A holder whose lease ran out is fenced out (lore refuses its writes with `E_LOST_CLAIM`) and cannot finish the task.

**Do heartbeats fill the repo with commits?** No. Signals live on `refs/lore/signals`, which always holds exactly one commit; each heartbeat replaces it.

## Pitfalls

- **Read the hint line.** Every lore error prints its code, a message, and a `hint:` with the next step.
- **Exit statuses mean something.** Scripts and agents should branch on them (see [Commands › error codes](commands.md#error-codes-and-exit-statuses)), not on the message text.

## Related

- [Error codes and exit statuses](../reference/errors.md): every code lore prints.
- [Setup and invite errors](setup-errors.md): `E_SETUP_*` and `E_INVITE_*`.
- [How it works](how-it-works.md): what lore does in git, for reading a `--trace`.
- [Commands reference](commands.md) and [Checking every setup](../operations/doctor-all.md).
