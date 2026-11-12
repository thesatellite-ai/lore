# Commands reference

**In short:** this page lists every team command with its flags, the settings in `~/.lore/config.yaml`, the environment variables, and what each error code and exit status means. Use it to look things up. For step-by-step guides, start at the [team docs index](README.md).

**How this page is made.** The usage and flag blocks below are the real `--help` output of the lore binary. The error table is copied from the generated errors block of [cli-commands.md](../reference/cli-commands.md#errors), which `lore spec docs` writes from the code. `tests/team/docs/paste.py` regenerates them (`LORE_BIN=<lore> python3 tests/team/docs/paste.py`; with `--check` it fails when a block is stale). The environment variable and config key tables are written by hand and cited to the code that defines them, so `ds check` flags them when that code changes. Global flags every command accepts (`--color`, `--trace`, and on most commands `--db`, `--project`, `--read-only`, `--repo`, `--json`) are left out of the blocks.

Contents: [who you are](#who-you-are-and-which-org) · [people and projects](#people-profiles-and-projects) · [stores](#stores) · [live rows](#live-rows-and-landing) · [sharing](#moving-rows-between-audiences) · [leak checks](#leak-checks-context-and-caches) · [tasks and missions](#tasks-and-missions-team-flags) · [agents](#agent-coordination) · [other kinds](#other-row-kinds-team-flags) · [environment variables](#environment-variables) · [config keys](#config-keys) · [error codes and exit statuses](#error-codes-and-exit-statuses)

## Who you are and which org

### `lore whoami`

<!-- help: whoami -->
Show the resolved org, what decided it, your identity and what loads here

```text
Usage:
  lore whoami
  lore whoami <command>

Examples:
  lore whoami
  # every org rule with hit or miss
  lore whoami --explain
  # as if the org were agency-stag
  LORE_ORG=agency-stag lore whoami
  lore whoami --json
  # synced label of this machine ("" clears)
  lore whoami --machine-label "tomas desktop"

Available Commands:
  show        Show the resolved org, what decided it, your identity and what loads here

Flags:
      --explain                print every org rule with hit or miss and why
      --json                   print the result (or the error) as JSON
      --machine-label string   set this machine's label, synced with its profile to our own stores ("" clears)
      --share                  kept for older scripts: whoami always prints store URLs and folders (only a credential inside a URL is masked)
```
<!-- /help -->

### `lore config check`

<!-- help: config check -->
Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem

```text
Usage:
  lore config check

Examples:
  # validate
  lore config check

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

### `lore identity set`

<!-- help: identity set -->
Persist an identity: the active setup's in ~/.lore/config.yaml (top-level identity: with no setup), or one org's (--org)

```text
Usage:
  lore identity set [identity]

Examples:
  # your email
  lore identity set alice@example.com
  # inside one org
  lore identity set --org agency --key human:alice@agency.example --name "Alice Doe" --handle alice

Flags:
      --handle string   roster handle, without @
      --json            print the result (or the error) as JSON
      --key string      human:<your email> (with --org: your email in that org)
      --name string     with --org: display name
      --org string      set your identity inside this org entry of ~/.lore/config.yaml
      --yes             answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore org link`

<!-- help: org link -->
Link this repo to an internal project in the org's store

```text
Usage:
  lore org link [org]

Examples:
  # link to an internal project
  lore org link agency --project client
  # one of the org's own repos
  lore org link agency --project lore-tools --own
  # the default link of a repo with several, checked first
  lore org link agency --project client2 --default --dry-run

Flags:
      --default          the link --to <org> uses when this repo has several
      --dry-run          validate and show the link, write nothing
      --json             print the result (or the error) as JSON
      --own              this is one of the org's own repos (only for own: patterns)
      --project string   the internal project folder in the store (required)
      --remote string    which remote, when the repo has several
      --store string     which of the org's stores (default: the first)
      --yes              answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore org use`

<!-- help: org use -->
Choose the org entry for this project on this machine (not committed)

```text
Usage:
  lore org use [entry]

Examples:
  # work against the staging store branch here
  lore org use agency-stag
  # back to automatic resolution
  lore org use --clear

Flags:
      --clear   forget the choice for this project
      --json    print the result (or the error) as JSON
      --yes     answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore org set`

<!-- help: org set -->
Write the committed .lore/org.yaml (only in the org's own repos)

```text
Usage:
  lore org set <org>

Examples:
  # name the org for every teammate
  lore org set agency --commit
  # the org and its internal project
  lore org set agency --commit --project lore-tools

Flags:
      --commit           confirm writing the committed .lore/org.yaml
      --json             print the result (or the error) as JSON
      --project string   the internal project folder (optional)
      --yes              answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore org import`

<!-- help: org import -->
Adopt the org entry a team suggests in its store, with your identity, and join its roster

```text
Usage:
  lore org import <url>

Examples:
  # join an org
  lore org import git@github.com:agency/notes --key human:me@agency.example --name Me --handle me
  # the suggested entry name is taken here
  lore org import git@github.com:agency/notes --key human:me@agency.example --name Me --as agency-2

Flags:
      --as string       entry name, when the suggested one is taken
      --branch string   store branch to read the suggestion from (default: the store's default branch)
      --handle string   your @handle on the roster
      --json            print the result (or the error) as JSON
      --key string      your identity key in this org: human:<your email there> (required)
      --name string     your display name (required)
      --yes             answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore actor register`

<!-- help: actor register -->
Put yourself (or, with --kind agent, an agent you own) on the org roster

```text
Usage:
  lore actor register

Examples:
  # register yourself
  lore actor register
  # register an agent you own
  lore actor register --kind agent --label agent-frontend --owner @alice

Flags:
      --json           print the result (or the error) as JSON
      --kind string    human (default) or agent
      --label string   with --kind agent: the session label
      --owner string   with --kind agent: the owning human as @handle (default: you)
      --read-only      refuse writes (also LORE_READ_ONLY=1)
      --yes            answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore actor deactivate`

<!-- help: actor deactivate -->
Mark someone as gone (active=false, left_at); actors are never deleted

```text
Usage:
  lore actor deactivate <handle>

Examples:
  # someone left the team
  lore actor deactivate @carol

Flags:
      --json        print the result (or the error) as JSON
      --read-only   refuse writes (also LORE_READ_ONLY=1)
      --yes         answer yes to the confirmation without a terminal
```
<!-- /help -->

## People, profiles and projects

Team profiles are in development for lore 0.1.18 (branch `feat/team-profiles`) and not released yet. Until they are built, the usage lines below are written by hand from the design, not pasted from `--help`; the guide with examples is [Team profiles](profiles.md). `<actor>` is any actor reference: `@handle`, an `act_` id, an identity key (`human:<email>`), an identity contact as `<type>:<value>` (`email:…`, `github:…`) or a bare email. Agents may run every write too (the policy flag `agents.may_manage_people` is true by default); a team that sets it false leaves the writes to people. Reads are open to agents. Writes go to the org's roster store and are refused in a repo without an org (`E_REFUSED`).

| Command | Usage | What it does |
|---|---|---|
| `lore actor show` | `lore actor show <actor> [--store s] [--json]` | the profile, contacts, company periods and project memberships |
| `lore actor list` | `lore actor list [--store s] [--status s] [--archived] [--json]` | the roster; archived people only with `--archived` |
| `lore actor edit` | `lore actor edit <actor> [--display-name t] [--title t] [--timezone tz] [--location t] [--photo-url u] [--bio t] [--birthday d] [--clear f,f]` | change profile fields |
| `lore actor contact add` | `lore actor contact add <actor> <type> <value> [--label l] [--primary] [--identity] [--note t] [--merge] [--yes]` | add a contact; `--merge` folds a duplicate actor of that email into this one |
| `lore actor contact edit` | `lore actor contact edit <acn_id> [--value v] [--label l] [--primary] [--no-primary] [--identity] [--no-identity] [--note t]` | change a contact |
| `lore actor contact remove` | `lore actor contact remove <acn_id> [--reason t]` | remove a contact (restorable from the trash) |
| `lore actor contact list` | `lore actor contact list [<actor>] [--type t] [--identity] [--json]` | one person's contacts, or everyone's |
| `lore contact-type list` | `lore contact-type list [--retired] [--json]` | built-in and added contact types |
| `lore contact-type add` | `lore contact-type add <name> --kind email\|handle\|phone\|url\|text [--label t] [--identity] [--url-template t]` | add a contact type |
| `lore contact-type edit` | `lore contact-type edit <name> [--label t] [--url-template t] [--retire] [--unretire]` | change or retire a contact type |
| `lore actor status` | `lore actor status <actor> active\|on_leave\|archived\|left [--from d] [--until d]` | change a person's status (leave, back, archive) |
| `lore actor offboard` | `lore actor offboard <actor> [--on d] [--reason t] [--note t] [--archive] [--yes] [--json]` | someone leaves: preview, then confirm |
| `lore actor rejoin` | `lore actor rejoin <actor> --type employee\|contractor\|intern\|client_contact [--on d] [--org-role t] [--note t] [--yes] [--json]` | someone comes back: preview, then confirm |
| `lore actor period list` | `lore actor period list <actor> [--json]` | company periods (stints) |
| `lore actor period add` | `lore actor period add <actor> --type t --started-on d [--ended-on d] [--org-role t]` | record a stint by hand |
| `lore actor period edit` | `lore actor period edit <apd_id> [--type t] [--started-on d] [--ended-on d] [--org-role t] [--end-reason t] [--note t]` | fix a stint |
| `lore project member add` | `lore project member add <project> <actor> --role a,b [--on d] [--note t]` | add someone to a project with roles |
| `lore project member edit` | `lore project member edit <project> <actor> --role a,b` | set the current membership's roles |
| `lore project member remove` | `lore project member remove <project> <actor> [--on d] [--reason t]` | end a membership (kept, with its dates) |
| `lore project member list` | `lore project member list [<project>] [--actor a] [--role r] [--history] [--json]` | members; `--history` adds ended memberships |
| `lore role list` | `lore role list [--retired] [--json]` | built-in and added roles |
| `lore role add` | `lore role add <name> [--label t] [--description t]` | add a role |
| `lore role edit` | `lore role edit <name> [--label t] [--description t] [--retire] [--unretire]` | change or retire a role |

New error codes: `E_CONTACT_TAKEN`, `E_CONTACT_TYPE`, `E_ROLE_UNKNOWN`, `E_STATUS_CHANGE` and `E_PERIOD_OVERLAP`; each has a section in [Troubleshooting](troubleshooting.md#e_contact_taken).

## Stores

### `lore store init`

<!-- help: store init -->
Make a git repo or folder a lore store and attach it (me = your personal store)

```text
Usage:
  lore store init <name> [url]

Examples:
  # your personal store, a local folder
  lore store init me
  # a team store on git
  lore store init agency git@github.com:agency/notes

Flags:
      --json                print the result (or the error) as JSON
      --visibility string   internal (default) or shareable: whether its rows may reach client repos
      --yes                 answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore store show`

<!-- help: store show -->
List the configured org stores, their clones and (with --links) the link index; with a store name, its push mode and compaction

```text
Usage:
  lore store show [store]

Examples:
  # every store
  lore store show
  # the personal store's push mode
  lore store show me

Flags:
      --json    print the result (or the error) as JSON
      --links   also print the cached link index of every entry
```
<!-- /help -->

### `lore store set`

<!-- help: store set -->
Set a store's push mode and history compaction (me, an org store as its admin, or a .lore/stores.yaml store)

```text
Usage:
  lore store set <store> <settings...>

Examples:
  # batch the personal store's pushes
  lore store set me push=batch push_every=10m
  # compact an org store's history older than 30 days
  lore store set team compact=on compact_after=30d

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

How writes to the personal store, an org store or a `.lore/stores.yaml` store reach the remote: see [How writes reach the remote](identity-and-stores.md#how-writes-reach-the-remote).

### `lore store compact`

<!-- help: store compact -->
Squash a store's history older than an age into one snapshot commit (every row is kept; asks first)

```text
Usage:
  lore store compact <store>

Examples:
  # see what would be squashed
  lore store compact me --before 30d --dry-run
  # compact the personal store
  lore store compact me --yes

Flags:
      --before string            squash history older than this age (e.g. 30d; default: the store's compact_after, else 30d)
      --dry-run                  show what would be squashed; change nothing
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore store open`

<!-- help: store open -->
Print the store's own clone of a branch to browse (~/.lore/stores/<owner>/<repo>, or <repo>@<branch>), fetched first

```text
Usage:
  lore store open <url>

Examples:
  # browse a store
  lore store open git@github.com:agency/notes

Flags:
      --branch string   store branch (default: the entry's branch)
      --json            print the result (or the error) as JSON
```
<!-- /help -->

### `lore store forget`

<!-- help: store forget -->
Delete this machine's clone of a store (refused while it holds unpushed work)

```text
Usage:
  lore store forget <url>

Examples:
  # drop a clone
  lore store forget git@github.com:agency/notes

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

### `lore store doctor`

<!-- help: store doctor -->
Check every store this repo and machine use (stores.yaml, the org, the personal store) and the v0.1.17 org stores: reachable, unpushed commits and their age, refused pushes, the clone folders

```text
Usage:
  lore store doctor

Examples:
  # check
  lore store doctor

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

A store whose remote does not answer fails the check with `E_STORE_UNREACHABLE` (exit 15), not `E_INVALID_INPUT`. The line names the store's url (credentials masked) and git's own reason, and tells "cannot reach" (no network: lore keeps working and the commits go out once it answers) from "no git repository at" (a wrong or deleted address: `lore store set-url` fixes it), "cannot read … (permission denied)" (a local folder that exists but this user may not read: the address is right, fix the permissions) and "refused this machine's credentials".

## Live rows and landing

### `lore live show`

<!-- help: live show -->
List this project's live rows, or print one live file

```text
Usage:
  lore live show [id]

Examples:
  # every live row
  lore live show
  # one live row's file
  lore live show tsk_31

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

### `lore live diff`

<!-- help: live diff -->
Local, tracking and remote live ref, and the writes not pushed yet

```text
Usage:
  lore live diff

Examples:
  # what this clone has not pushed yet
  lore live diff

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

### `lore live log`

<!-- help: live log -->
History of the live ref with its epoch

```text
Usage:
  lore live log

Examples:
  # the last 10 live commits
  lore live log --number 10

Flags:
      --json         print the result (or the error) as JSON
  -n, --number int   commits to show (default 20)
```
<!-- /help -->

### `lore live compact`

<!-- help: live compact -->
Rewrite the live ref to one commit with a higher epoch (keeps every row; a local backup is kept)

```text
Usage:
  lore live compact

Examples:
  # see what a compaction would do
  lore live compact --dry-run
  # rewrite the live ref to one commit
  lore live compact

Flags:
      --dry-run   print what would happen, change nothing
      --json      print the result (or the error) as JSON
      --yes       answer yes to the confirmation without a terminal
```
<!-- /help -->

Each compaction raises the epoch by exactly one. A live ref that is already one commit with no purged rows to drop has nothing to compact: the command says `nothing to compact: refs/lore/live is already 1 commit with no purged rows (epoch N, unchanged)`, writes nothing and keeps the epoch, so teammates are not sent through the re-apply path for nothing.

### `lore live restore`

<!-- help: live restore -->
Put the live rows back on the remote from the local copy after it was deleted or rewritten

```text
Usage:
  lore live restore

Examples:
  # put the live rows back after the remote ref was deleted
  lore live restore

Flags:
      --json   print the result (or the error) as JSON
      --yes    answer yes to the confirmation without a terminal
```
<!-- /help -->

Someone deleted `refs/lore/live` on the server. From then on every live write, `lore sync` and `lore land` fail with `E_READ_ONLY` and `lore doctor` says why; `lore live restore` pushes the local copy back:

<!-- example: live-repair#live-ref-deleted only=3,4,5 -->
```console
# alice, in billing
$ git push -q origin :refs/lore/live && git ls-remote origin
9b40ce71af28	HEAD
9b40ce71af28	refs/heads/main
$ lore task add 'close the books for Q1'
ERROR: [E_READ_ONLY] the remote live ref was deleted or rewritten; lore stopped writing to it (your local copy is kept as refs/lore-backup/live/20261102T100020)
  hint: run `lore live restore` to put your live rows back on the remote
(exit 23)
$ lore doctor | grep 'remote live ref'
  - live: the remote live ref was deleted or rewritten; writes are stopped until `lore live restore`
```
<!-- /example -->

<!-- example: live-repair#live-restore -->
```console
# alice, in billing
$ lore live restore
✓ restored refs/lore/live: 3 rows (left out 0 already landed or purged)
$ git ls-remote origin refs/lore/live
05513aa29b78	refs/lore/live
$ lore task add 'close the books for Q1'
lore: wrote ~/work/billing/.lore/data/tasks/tsk_54fe…28f2.json
✓ tsk_54fe…28f2 — close the books for Q1 [medium]
lore live: (live, pushed) commit dfd8dc8 to origin refs/lore/live
$ lore task list
○ tsk_2ad3…f891 send the March invoices [medium]
○ tsk_162d…9666 chase the unpaid February invoice [medium]
○ tsk_54fe…28f2 close the books for Q1 [medium]
```
<!-- /example -->

### `lore live purge`

<!-- help: live purge -->
Remove rows from live for good (leak cleanup): no clone can push them back

```text
Usage:
  lore live purge <ids...>

Examples:
  # see what a purge would remove
  lore live purge mem_0c3a --dry-run
  # remove a leaked row from live for good
  lore live purge mem_0c3a --confirm

Flags:
      --confirm   required: purge removes rows from live for good
      --dry-run   show what would be purged; write nothing
      --json      print the result (or the error) as JSON
      --yes       answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore land`

<!-- help: land -->
Write live rows to main now (finished rows land; open rows are copied and stay live)

```text
Usage:
  lore land [id]
  lore land <command>

Examples:
  # see what would land
  lore land run --dry-run
  # land one finished task
  lore land run tsk_31

Available Commands:
  run         Write live rows to main now (finished rows land; open rows are copied and stay live)

Flags:
      --all              every live row of this project
      --dry-run          print the plan, change nothing
      --json             print the result (or the error) as JSON
      --kind string      only rows of this kind (tasks, rules, …)
      --mission string   only the mission and its tasks
      --yes              answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore explain`

<!-- help: explain -->
Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision

```text
Usage:
  lore explain <id>
  lore explain <command>

Examples:
  # why does this row look the way it does
  lore explain mem_<id>

Available Commands:
  run         Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision

Flags:
      --json   print the result (or the error) as JSON
```
<!-- /help -->

## Moving rows between audiences

### `lore move`

<!-- help: move -->
Move any row to another store, keeping its id (the old copy is tombstoned)

```text
Usage:
  lore move <id>

Examples:
  # move a row to your personal store
  lore row move mem_0c3a --to me

Flags:
      --drop-internal      into a client repo: remove the row's internal metadata values instead of refusing
      --json               print the result (or the error) as JSON
      --to string          the target store: repo | me | <store>[/<folder>] (required)
      --with-children      a task: move its whole subtree too; every sub-task keeps its id and parent
      --with-time string   totals | none: per-worker time totals for a task moved into a client repo (default: the time.client_share policy, none)
      --yes                answer yes to the confirmation without a terminal
```
<!-- /help -->

## Leak checks, context and caches

### `lore check leaks`

<!-- help: check leaks -->
Check staged files and the commit message (hooks), or the tree structure (--ci), for internal or personal content

```text
Usage:
  lore check leaks

Examples:
  # staged files, as the pre-commit hook does
  lore check leaks
  # the commit message
  lore check leaks --commit-msg .git/COMMIT_EDITMSG
  # in the client's CI
  lore check leaks --ci
  # before handing an own repo to the client
  lore check leaks --as-client
  # every stored row with today's secret rules
  lore check leaks --rows

Flags:
      --as-client           check the whole repo as the client would see it (before a hand-over)
      --ci                  CI mode: ids and structure only, no store access, names nothing internal
      --commit-msg string   also check this commit message file (the commit-msg hook's $1)
      --explain             print the rule, the source and the matched words of each finding
      --json                print the result (or the error) as JSON
      --rows                re-scan every stored row of every attached store with today's secret rules
      --staged              check the staged files (default unless --commit-msg is given alone)
```
<!-- /help -->

### `lore check pr-body`

<!-- help: check pr-body -->
Check a pull request body or comment before posting it (agents: always)

```text
Usage:
  lore check pr-body <file>

Examples:
  # a body file
  lore check pr-body body.md
  # from stdin
  gh pr view 12 --json body -q .body | lore check pr-body -

Flags:
      --explain   print the rule, the source and the matched words of each finding
      --json      print the result (or the error) as JSON
```
<!-- /help -->

### `lore context`

<!-- help: context -->
Print the rules, hotfixes and notes for a file, task, topic or prompt (the agent's fetch command)

```text
Usage:
  lore context
  lore context <command>

Examples:
  # what applies to a file
  lore context --for-file services/billing/invoice.go
  # the topic index
  lore context --topics

Available Commands:
  fetch       Print the rules, hotfixes and notes for a file, task, topic or prompt (the agent's fetch command)

Flags:
      --for-file string     rules, hotfixes and notes for this path
      --for-prompt string   rows matching this free text
      --for-task string     the task, its plan and links, then what applies to the files it names
      --hook string         answer a tool hook (claude_code, codex, gemini_cli)
      --hook-stdin          with --hook: read the hook input JSON from stdin
      --internal            internal, global and personal rows (outside this checkout)
      --json                print the result (or the error) as JSON
      --max-bytes int       cap the output in bytes (default 16000; hooks: context.hook_cap)
      --meta                also print each returned row's metadata (sensitive values masked)
      --search string       with --internal: only rows whose title or body contains this text
      --session-start       with --hook: answer SessionStart (in-flight tasks, rows changed since your last session)
      --table string        with --internal: only this table (rules, memories, decisions, tasks, …)
      --topic string        every row tagged with this topic
      --topics              list the topics with their counts
```
<!-- /help -->

### `lore cache prune`

<!-- help: cache prune -->
Remove cache files of stores no repo on this machine attaches any more (lists them and asks first)

```text
Usage:
  lore cache prune

Examples:
  # list what prune would delete
  lore cache prune --dry-run

Flags:
      --dry-run   list the cache files prune would delete, with their sizes; delete nothing
      --json      print the result (or the error) as JSON
      --yes       answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore render`

<!-- help: render -->
Render .lore/LORE.md from scoped knowledge and point your AI tools at it

```text
Usage:
  lore render
  lore render <command>

Examples:
  # render and wire the repo's tools
  lore render
  # wire one more tool and keep it in context.targets
  lore render --add-target claude_code
  # CI: fail when LORE.md is over its budget
  lore render --check

Available Commands:
  run         Render .lore/LORE.md from scoped knowledge and point your AI tools at it

Flags:
      --add-target stringArray   add tool profiles to the repo's context.targets (keeping the ones it has) and wire them (repeatable, comma-separated)
      --check                    CI check: write nothing, fail when LORE.md would be over the warn level or the budget
      --dry-run                  print the generated body instead of writing it
      --json                     print the result (or the error) as JSON
      --no-pointer               do not stitch the pointer into the agent file
      --out string               generated knowledge file path (default .lore/LORE.md)
      --report                   print the budget report (what is full, what is a line, what moved out) and write nothing
      --scopes                   with --report: one row per generated file per tool, plus the Codex and Claude Code chain sums
      --target stringArray       tool profile name or pointer file to wire (repeatable, comma-separated; default: the repo's tool files, else AGENTS.md)
      --trend                    bytes per render over the last 14 days, growth per day and when LORE.md reaches its budget; writes nothing
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

## Tasks and missions (team flags)

### `lore task add`

<!-- help: task add -->
Add a new task

```text
Usage:
  lore task add [title...]

Examples:
  # add a task
  lore task add "Fix the login redirect" --priority high
  # a proposed task, as JSON
  lore task add "Try a cache" --commitment proposed --json

Flags:
      --after stringArray        dependencies: <id>[:done|:merged][,…] (default: done in the same repo, merged across repos)
      --arg stringArray          a template argument name=value (repeatable)
      --assign stringArray       assign to @handle or an actor; repeatable: the assignees, the first is the lead (a team sets the assigned team)
      --assigned-to string       actor_id (act_*) the task is assigned to
      --body string              longer description
      --commitment string        accepted | proposed | someday (default accepted)
      --defer-until string       snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM); hides from active list until then
      --dry-run                  with --template: show the tasks that would be added, write nothing
      --due string               due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --estimate string          estimate: a number >= 0 (2 decimals) in the project's unit (tasks.estimate_unit: points or hours); hours also take 90m; no day unit
      --json                     print the result (or the error) as JSON
      --key string               idempotency key: a re-run with the same key (in the same mission) adds nothing
      --lead string              the lead (@handle or actor); added to the assignees if missing
      --live                     share this row with everyone at once on the live ref (refs/lore/live)
      --meta stringArray         set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray    set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string        read the value of this key from stdin (keeps it out of shell history)
      --mission string           mission_id (msn_*) to attach to
      --no-live                  keep this row with the code (this branch's .lore/data)
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --parent string            the parent task (tsk_…): add as a sub-task, inheriting mission, tasklist, plan, repo, commitment, priority
      --plan string              plan_id (pln_*) to attach to
      --priority string          low | medium | high | urgent (default medium)
      --project string           the project (prj_ id or name; default the repo's one project)
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --recur string             recurrence: every day|weekday|week|month|quarter|year, every N days|weeks|months, every mon[,thu], every month on N|last, every! N days|weeks (from completion); optional until <date> | for N
      --ref stringArray          a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable
      --relation string          the relation each --ref records (snake_case; default mentions)
      --reopen-parent            the parent is done or cancelled: reopen it to in_progress in the same write
      --repo string              scope to a repo of the project (mount name or rep_ id)
      --role string              plan role matched by task next --role (e.g. coordinator)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --tasklist string          tasklist_id (tlt_*); default: the project's default list (created on demand)
      --template string          apply this task template (its name or ttp_ id); the title comes from the template
      --timebox duration         time box, e.g. 60m (active lease time summed across holders)
      --to string                store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo) (also --store)
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

In a linked client repo, `--assign` takes a person, never a whole team: `--assign @frontend` there is refused, because team names are internal to the agency. Assign a person, or keep the task internal with `--to <org>`.

### `lore task edit`

<!-- help: task edit -->
Edit task fields (only flags you pass are applied)

```text
Usage:
  lore task edit [id]

Examples:
  # raise the priority
  lore task edit tsk_<id> --priority urgent

Flags:
      --add-assignee stringArray      add an assignee (repeatable); the first one on a task with no lead leads
      --add-watcher stringArray       add a watcher (repeatable)
      --after stringArray             dependencies: <id>[:done|:merged][,…] (replaces the list)
      --after-sibling string          place just after this sibling (tsk_…)
      --assign stringArray            set the assignees: @handle or actor, repeatable; the first is the lead (a team sets the assigned team)
      --assigned-to string            actor_id (act_*) to assign to
      --before string                 place just before this sibling (tsk_…)
      --body string                   new body
      --clear-assignee                unassign
      --clear-defer                   un-defer (clear deferred_until)
      --clear-due                     remove due date
      --clear-estimate                remove the estimate
      --clear-mission                 detach from mission
      --clear-plan                    detach from plan
      --clear-tasklist                take the task off its task list (clear tasklist_id)
      --commitment string             accepted | proposed | someday
      --defer-until string            snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --dry-run                       with --where: list the tasks that would change, write nothing
      --due string                    due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --estimate string               estimate in the project's unit (tasks.estimate_unit)
      --first                         place first among its siblings
      --json                          print the result (or the error) as JSON
      --last                          place last among its siblings
      --lead string                   make this person the lead (added to the assignees if missing)
      --live                          share this row with everyone at once on the live ref (refs/lore/live)
      --meta stringArray              set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray         set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string             read the value of this key from stdin (keeps it out of shell history)
      --mission string                attach to mission_id (msn_*)
      --no-live                       keep this row with the code (this branch's .lore/data)
      --no-parent                     detach: make it a top-level task
      --not-a-secret                  keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --parent string                 move under this task (tsk_…), with its subtree
      --plan string                   attach to plan_id (pln_*)
      --priority string               low | medium | high | urgent
      --read-only                     refuse writes (also LORE_READ_ONLY=1)
      --reason string                 why the detected text is not a secret (with --not-a-secret)
      --recur string                  set or change the recurrence rule (applies to this and later instances)
      --ref stringArray               a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable
      --relation string               the relation each --ref records (snake_case; default mentions)
      --remove-assignee stringArray   remove an assignee (repeatable); removing the lead needs --lead
      --remove-watcher stringArray    remove a watcher (repeatable)
      --reopen-parent                 the parent is closed: reopen it to in_progress in the same write
      --role string                   plan role matched by task next --role (e.g. coordinator)
      --secret-ref stringArray        replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored               replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --status string                 todo | in_progress | done | cancelled | blocked
      --tasklist string               move to tasklist_id (tlt_*)
      --timebox duration              time box, e.g. 60m
      --title string                  new title
      --where stringArray             bulk edit: every task matching this filter (the task list --where grammar; repeatable, all must match)
      --yes                           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task list`

<!-- help: task list -->
List tasks

```text
Usage:
  lore task list

Examples:
  # list as JSON
  lore task list --json

Flags:
      --all                   show everything: done/cancelled + proposed/someday + deferred
      --all-repos             every repo of the project (the default)
      --assignee string       filter by assignee: actor_id (act_*)
      --claimed-by string     only tasks claimed right now by @handle (or actor id)
      --columns stringArray   columns of a table: id,title,estimate,tracked,… (any --where key, time|tracked, elapsed, progress, store); +time / +elapsed alone extend the usual line
      --commitment string     filter: accepted | proposed | someday
      --created-by string     only rows this actor created (act_ id, or unknown for rows from before audit fields)
      --deleted-only          list only rows in the trash
      --include-deferred      also include deferred (deferred_until in the future)
      --include-deleted       also list rows in the trash (marked in the trash)
      --include-proposed      also include commitment=proposed
      --include-someday       also include commitment=someday
      --json                  print the result (or the error) as JSON
      --lead string           only tasks led by this actor (@me, act_ id, stable key or name)
      --leaves                only tasks with no open sub-tasks (what can be worked on)
      --max-time string       only tasks with at most this effort (e.g. 30m)
      --meta stringArray      filter by metadata: key=value, key!=value, key~=text, key>n, key?, key!? (repeatable, all must match)
      --min-time string       only tasks with at least this effort (e.g. 2h)
      --mine                  assigned to me or my team, and unclaimed or claimed by me
      --mission string        filter by mission_id
      --parent string         only the direct sub-tasks of this task (tsk_…)
      --place string          narrow --meta names to one place: repo, project, org or a scope label (project:acme-hub)
      --project string        only tasks of this project (prj_ id or name)
      --read-only             refuse writes (also LORE_READ_ONLY=1)
      --series string         only the instances of this recurrence series (rcs_…); add --all for closed ones
      --sort stringArray      sort by a task field, urgency, time_spent or elapsed: <key>, <key>:asc or <key>:desc (repeatable; ties keep the default order)
      --status string         filter: todo | in_progress | done | cancelled | blocked (empty = default active set, see --all)
      --store string          only tasks of this store (default every attached store) (also --to)
      --top                   only top-level tasks
      --under string          only the whole subtree under this task (tsk_…)
      --updated-by string     only rows this actor changed last (act_ id, or unknown)
      --view string           apply a saved task view (name or tvw_ id; lore task view list)
      --where stringArray     filter on a task field: status:todo,in_progress priority>=high due_at<2026-12-01 (= != ~= > >= < <= ? !?; repeatable, all must match)
      --worked-by string      only tasks with time by this actor (@me, act_ id, key or name); add +agents for their agents too
      --worked-since string   only tasks with time on or after this day (YYYY-MM-DD or a date phrase); TIME shows that range
      --worked-until string   only tasks with time on or before this day; TIME shows that range
```
<!-- /help -->

### `lore task show`

<!-- help: task show -->
Show task details

```text
Usage:
  lore task show <id>

Examples:
  # show as JSON
  lore task show tsk_<id> --json

Flags:
      --expand        fill in ${var:…}, ${res:…}, ${row:…} and ${env:…} references; secrets stay masked
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --read-only     refuse writes (also LORE_READ_ONLY=1)
      --relations     show every relation, both directions
      --signals       show the claim as seen now (holder, token prefix, expires, renewed_seq)
      --tree          also print the sub-task tree
      --who           who created and last changed it (and, on the writing machine, with which session)
      --why           explain the urgency score term by term
```
<!-- /help -->

### `lore task done`

<!-- help: task done -->
Mark a task done (with the code that finished it)

```text
Usage:
  lore task done <id>

Examples:
  # done with a lesson
  lore task done tsk_<id> --pr 412 --lesson "invite tokens expire in 48h"

Flags:
      --children string          open sub-tasks: done | cancel them in the same write (required when there are any)
      --commit string            the commit that finished the task
      --force                    close even with unticked checklist items
      --from string              the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json                     print the result (or the error) as JSON
      --lesson string            save a lesson learned (a memory pointing at the task)
      --no-lesson                skip the lesson reminder
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --pr int                   the pull request that finished the task
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --tick-all                 unticked checklist items: tick them in the same write
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task merged`

<!-- help: task merged -->
Record that a done task's work reached the default branch

```text
Usage:
  lore task merged <id>

Examples:
  # mark merged
  lore task merged tsk_<id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --read-only     refuse writes (also LORE_READ_ONLY=1)
      --repo string   scope to a specific repo (mount_name or rep_id)
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task cancel`

<!-- help: task cancel -->
Cancel a task

```text
Usage:
  lore task cancel <id>

Examples:
  # cancel with a reason
  lore task cancel tsk_<id> --reason obsolete

Flags:
      --children string           open sub-tasks: cancel | done them in the same write (required when there are any)
      --json                      print the result (or the error) as JSON
      --not-a-secret              keep a detected secret as a false positive (needs --not-a-secret-why; --yes without a terminal)
      --not-a-secret-why string   why the detected text is not a secret (with --not-a-secret; this command's --reason means something else)
      --read-only                 refuse writes (also LORE_READ_ONLY=1)
      --reason string             close reason: cancelled (default) | duplicate | wont_do | obsolete | stale
      --secret-ref stringArray    replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored           replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --yes                       answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore mission add`

<!-- help: mission add -->
Add a new mission

```text
Usage:
  lore mission add <title...>

Examples:
  # add a mission
  lore mission add "Ship the invite flow" --target 2026-12-31

Flags:
      --approvers stringArray    only these people (@handle or act_…, comma separated) may approve the mission's gates
      --body string              longer description
      --json                     print the result (or the error) as JSON
      --key string               idempotency key: a re-run with the same key adds nothing
      --meta stringArray         set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray    set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string        read the value of this key from stdin (keeps it out of shell history)
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --plan string              plan_id (pln_*) this mission carries out
      --project string           the project (prj_ id or name; default the repo's one project)
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --starts-at string         when the mission's work starts (YYYY-MM-DD or a date phrase)
      --target string            target completion date (YYYY-MM-DD or a date phrase: eom, next fri, in 2w, …)
      --to string                store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo) (also --store)
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore mission status`

<!-- help: mission status -->
The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done

```text
Usage:
  lore mission status <id>

Examples:
  # as JSON
  lore mission status msn_<id> --json

Flags:
      --json        print the result (or the error) as JSON
      --read-only   refuse writes (also LORE_READ_ONLY=1)
```
<!-- /help -->

### `lore mission apply`

<!-- help: mission apply -->
Create a mission and its tasks from a YAML plan; a re-run adds nothing

```text
Usage:
  lore mission apply <file>

Examples:
  # apply a plan file
  lore mission apply invite.yaml

Flags:
      --dry-run                  show what would be written; write nothing
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

## Agent coordination

### `lore agent start`

<!-- help: agent start -->
Register an agent actor for this session and print its environment; --tool: record a tool session's details (session hook)

```text
Usage:
  lore agent start

Examples:
  # start a labelled agent
  eval "$(lore agent start --label agent-frontend --owner @alice | tail -1)"
  # from the tool's SessionStart hook
  lore agent start --tool claude_code --hook-stdin

Flags:
      --hook-stdin       session hook: read the tool's hook JSON from stdin
      --json             print the result (or the error) as JSON
      --label string     the agent's label (one actor per label), e.g. agent-frontend
      --model string     session hook: the model the tool reports (never guessed)
      --owner string     the owning person (@handle); default: you
      --session string   session hook: the tool's own session id
      --tool string      session hook: the tool's slug (claude_code, codex, …); records details only
      --yes              answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task claim`

<!-- help: task claim -->
Claim a task (or coordinate a mission) on the signals ref, with a lease

```text
Usage:
  lore task claim <id>

Examples:
  # claim with owned paths
  lore task claim tsk_<id> --owns src/routes/invite/

Flags:
      --coordinate         take a coordinator lease on a parent with open sub-tasks
      --from string        the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json               print the result (or the error) as JSON
      --owns stringArray   paths this claim owns (directory prefixes ending in / or exact files)
      --read-only          refuse writes (also LORE_READ_ONLY=1)
      --strict             refuse instead of warning when --owns overlaps another claim
      --yes                answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task next`

<!-- help: task next -->
Claim the next ready task (resume-first, leaves only, dependencies, gates, urgency)

```text
Usage:
  lore task next

Examples:
  # rank without claiming
  lore task next --mission msn_<id> --why --dry-run

Flags:
      --dry-run            rank and explain without claiming (nothing written)
      --json               print the result (or the error) as JSON
      --mission string     only tasks of this mission
      --owns stringArray   paths the claim owns
      --read-only          refuse writes (also LORE_READ_ONLY=1)
      --repo string        only tasks of this repo (a repo name of the group)
      --role string        plan role (e.g. coordinator)
      --why                explain the urgency score term by term
      --yes                answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task heartbeat`

<!-- help: task heartbeat -->
Renew your claim's lease (alive, not progress)

```text
Usage:
  lore task heartbeat [id]

Examples:
  # renew one claim
  lore task heartbeat tsk_<id>

Flags:
      --daemon      keep renewing until the claim ends or the parent exits
      --json        print the result (or the error) as JSON
      --read-only   refuse writes (also LORE_READ_ONLY=1)
      --yes         answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task release`

<!-- help: task release -->
Give back a claim you hold

```text
Usage:
  lore task release <id>

Examples:
  # release
  lore task release tsk_<id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --read-only     refuse writes (also LORE_READ_ONLY=1)
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task note`

<!-- help: task note -->
Record a progress note on a task (pushes your work branch)

```text
Usage:
  lore task note <id> <text...>

Examples:
  # a checkpoint note
  lore task note tsk_<id> "route + loader done, form next"

Flags:
      --everywhere    with --to me: file the note under everywhere/ (loads in every repo)
      --force-org     write even though LORE_ORG or lore org use disagrees with a store link
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --read-only     refuse writes (also LORE_READ_ONLY=1)
      --to string     where the row goes: repo | <org> | <org>/<folder> | me | me/<org>
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task revoke`

<!-- help: task revoke -->
Drop the current claim on a task (human override; the holder is fenced out)

```text
Usage:
  lore task revoke <id>

Examples:
  # revoke
  lore task revoke tsk_<id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --read-only     refuse writes (also LORE_READ_ONLY=1)
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore task reassign`

<!-- help: task reassign -->
Assign a task to someone else, dropping any claim (human override); --from reassigns every open task of a person as one batch

```text
Usage:
  lore task reassign [id]

Examples:
  # offboarding
  lore task reassign --from @bob --assign @alice --yes

Flags:
      --assign string            the new assignee (@handle)
      --from string              reassign every open task of this person
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore gate request`

<!-- help: gate request -->
Put a gate on a task (agents stop at it until a person approves)

```text
Usage:
  lore gate request <id>

Examples:
  # request a gate
  lore gate request tsk_<id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore gate approve`

<!-- help: gate approve -->
Approve a gate (a person on the roster, from the identity file, never an agent)

```text
Usage:
  lore gate approve <id>

Examples:
  # approve
  lore gate approve tsk_<id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder> (also --store)
      --json          print the result (or the error) as JSON
      --yes           answer yes to the confirmation without a terminal
```
<!-- /help -->

Who may approve: any person (human) on the org's roster, never an agent. A mission's `approvers:` list narrows it to the people listed.

### `lore hold claim`

<!-- help: hold claim -->
Hold a resource exclusively (same lease, token and renew as a task claim)

```text
Usage:
  lore hold claim <name>

Examples:
  # hold a test suite
  lore hold claim full-suite --ttl 30m

Flags:
      --json           print the result (or the error) as JSON
      --on string      project (the project's signals ref, the default) or machine
      --ttl duration   how long the hold lasts (default: lease.ttl)
      --yes            answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore hold release`

<!-- help: hold release -->
Release a resource you hold

```text
Usage:
  lore hold release <name>

Examples:
  # release a hold
  lore hold release full-suite

Flags:
      --json   print the result (or the error) as JSON
      --yes    answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore handoff add`

<!-- help: handoff add -->
Leave a handoff for someone (shows in their inbox)

```text
Usage:
  lore handoff add [text...]

Examples:
  # hand work over
  lore handoff add --for @agent-frontend --task tsk_31 "form is half done"

Flags:
      --body string              the handoff text (instead of the positional text)
      --for string               who the handoff is for (@handle, stable key, display name or act_ id)
      --json                     print the result (or the error) as JSON
      --meta stringArray         set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray    set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string        read the value of this key from stdin (keeps it out of shell history)
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --project string           the project (prj_ id or name; default the repo's one project)
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --store string             store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)
      --task string              the task it is about (its mission is recorded too)
      --to string                deprecated alias of --for
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore inbox`

<!-- help: inbox -->
What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings

```text
Usage:
  lore inbox
  lore inbox <command>

Examples:
  # recent items
  lore inbox --since 2h

Available Commands:
  show        What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings

Flags:
      --ack              mark everything shown as seen
      --json             print the result (or the error) as JSON
      --since duration   only items newer than this (e.g. 2h)
```
<!-- /help -->

## Other row kinds (team flags)

`lore memory add`, `lore rule add`, `lore decision add` (and the other `add` commands of shared kinds) accept the same team flag:

| Flag | Meaning |
|---|---|
| `--to <store>\|<store>/<folder>\|me` | which store the row is written to: a name from `.lore/stores.yaml`, one folder of it, or your personal store (default: this repo) |

Whether a row is live (seen by everyone at once, whatever branch they are on) depends on its kind, not on a flag: tasks, missions, handoffs and comments are live; rules, decisions and memories travel with the code (`live.defaults.<kind>` below). To send one rule to `main` now, use `lore sync promote <id> --to main`.

## Environment variables

<!-- ds:block id=sync-env-pe9vvf3f -->
<!-- ds:block id=team-env-actor-6rnbrdhr -->
<!-- ds:block id=team-env-org-z6mz7n3q -->
| Variable | Effect |
|---|---|
| `LORE_ORG` | choose the org entry for this shell or one command (rule 1 of org resolution); the only way, with `lore org use`, to select a staging entry |
| `LORE_ACTOR` | the actor for this session; accepted only for **agent** actors on the org's roster (a human key here is ignored with a warning, and gate approvals never come from it). `lore agent start` prints it |
| `LORE_SESSION` | the agent session id recorded on claims; printed by `lore agent start` |
| `LORE_LIVE_AUTOPUSH` | `0` = lore never touches the network on its own (same as `live.autopush: false`); live writes wait locally, coordination writes fail with `E_OFFLINE` |
| `LORE_LIVE` | `0` = no live rows at all in this shell: every row behaves as before live rows existed (an escape hatch for a repo that cannot use them yet) |
| `LORE_LIVE_REMOTE` | the remote that carries `refs/lore/*` (default `origin`; for forks and tests) |
| `LORE_TRACE` | `1` = like `--trace`: print every git command lore runs for live rows and every fetch, push and ls-remote to a store, with timing and the host reached (the setup's ssh alias for a store it owns) |
| `LORE_RIDE_ACTIVE` | set by lore itself while it makes a ride-along commit, so its own hooks do not run again; never set it by hand |
| `LORE_NOW` | a fixed clock (RFC 3339), read only by test builds (`-tags loretest`); release binaries ignore it |

## Config keys

All keys live in `~/.lore/config.yaml` (mode 0600; `lore config check` validates it and names every problem). Keys under `live`, `lease`, `agents` and `merge_check` may also be set inside one `orgs[]` entry to override the top level for that org. A team can suggest org defaults in its store's `<org_folder>/lore-org.yaml`; for `defaults.client_repos` the store's value wins over a looser personal one.

<!-- ds:block id=team-config-defaults-amp24fsn -->
<!-- ds:block id=team-live-kinds-88j26q7s -->
| Key | Default | Meaning |
|---|---|---|
| `identity.key`, `identity.name` | — | your identity in repos that match no org, once orgs exist |
| `personal.store` | `local` | `local` (`~/.lore/personal`) or the git URL of your own private store |
| `orgs[].name` | — | the entry's name; also the `--to <name>` target |
| `orgs[].identity.key`, `.name`, `.handle` | — | who you are in this org (`human:<email there>`), required when the entry resolves |
| `orgs[].stores[].url` | — | a store repo (must hold `lore-store.yaml`) |
| `orgs[].stores[].branch` | `main` | the store branch this entry uses; no `/`. Not the store's default branch = a staging entry |
| `orgs[].stores[].org_folder` | — | on exactly one store: the folder with the roster, teams and company rules (`.` = the store root) |
| `orgs[].stores[].name` | the url's repo name without `.git` | the store's short name in store list, output, store arguments and `<store>/<folder>` targets; two stores of one org may not share one |
| `orgs[].own` | — | URL patterns of the org's own repos (`*` one path segment, `**` any depth) |
| `orgs[].repos` | — | URL patterns of client repos |
| `orgs[].defaults.client_repos.<kind>` | tasks, memories and other kinds: the linked project | where a row goes in a client repo without `--to`: `repo`, `<org>` or `<org>/<folder>`; rules and decisions by an agent or without a terminal always need `--to` |
| `orgs[].agent_details` | `internal` | `client` keeps agent work notes in the client repo |
| `live.defaults.<kind>` | tasks, missions, handoffs, comments: `true`; rules, decisions, memories: `false` | which kinds are live |
| `live.max_age` | `7d` | an open live row is copied to `main` this often while it differs |
| `live.fetch_ttl` | `60s` | each command fetches at most this often; `lore sync` always fetches |
| `live.autopush` | `true` | push live writes at once |
| `live.compact_after` | `1000` | commits on a live ref before automatic compaction |
| `live.remote` | `origin` | the remote for `refs/lore/*` |
| `lease.ttl` | `20m` | how long a claim lasts without a heartbeat |
| `lease.renew_every` | `5m` | heartbeat interval; also how long a reader must see an unchanged renewal before calling a claim expired |
| `lease.grace` | `2m` | extra time after `expires` before a claim counts as expired |
| `lease.push_retries` | `5` | retries of a rejected push |
| `agents.max_claims_per_agent` | `1` | claims one agent may hold at once |
| `agents.max_attempts` | `3` | holders a task may have before it becomes `blocked` |
| `merge_check.adapter` | `none` | `github` asks `gh` whether a done task's pull request merged |
| `default_branch` | from `origin/HEAD` | override the default branch |
| `repo_orgs.<project-id>` | — | written by `lore org use`: this machine's org choice for one project |
| `link_index.max_age` | `1h` | how old a cached link index may be when the store cannot be read |
| `untrusted_hosts` | — | hostname patterns of client-owned machines; `lore doctor` warns when stores are configured there |
| `access` | — | this machine's way to canonical store urls: items of `url` (the prefix shared config uses) and `via` (yours, such as an ssh alias), applied per git call as `-c url.<via>.insteadOf=<url>` |

## Error codes and exit statuses

Agents and scripts can branch on the exit status without parsing text. These numbers are a contract: they are never renumbered. Every other error exits 1.

<!-- ds:block id=team-exit-codes-eqpyx54h -->
<!-- errors -->
| Code | Exit status | Meaning |
|---|---|---|
| `E_NO_WORK` | 10 | no ready task |
| `E_CONTENDED` | 11 | every claim attempt lost a race; retry after a short wait |
| `E_LOST_CLAIM` | 12 | your claim is no longer held; stop work on this task |
| `E_CANCELLED` | 13 | a person cancelled the task while you held it |
| `E_REASSIGNED` | 14 | a person reassigned the task while you held it |
| `E_OFFLINE` | 15 | the remote could not be reached; nothing was queued |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded sources; name one with --from |
| `E_ORG_AMBIGUOUS` | 17 | two org entries match this repo at the same rule |
| `E_ORG_DISAGREES` | 17 | the selected org differs from the store link |
| `E_LINK_UNKNOWN` | 18 | lore cannot tell whether this repo is a client repo |
| `E_NOT_A_STORE` | 19 | the repo or url is not a lore store |
| `E_LEAK_BLOCKED` | 19 | the write would put internal content where the client can read it |
| `E_CYCLE` | 19 | the write would create a cycle |
| `E_OLD_LORE` | 19 | this lore binary is below the project's min_lore_version or does not know one of its features; run lore upgrade (reads still work) |
| `E_CONFIG_INVALID` | 20 | a config file failed validation |
| `E_REFUSED` | 23 | a fixed lore rule refuses this; the message names the rule and what to do instead |
<!-- /errors -->

## Related

- [Concepts](concepts.md): the words these commands use (live row, landing, store, link).
- [How it works](how-it-works.md): what each command does in git.
- [Agents](agents.md): the claim, renew and done loop for coding agents.
- [Identity and stores](identity-and-stores.md), [Team profiles](profiles.md) and [Setups from the command line](setup-cli.md).
- [Troubleshooting](troubleshooting.md) and [Error codes and exit statuses](../reference/errors.md).
- [Full CLI reference](../reference/cli-commands.md): every command and flag.
