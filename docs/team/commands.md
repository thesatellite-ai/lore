# Commands reference

**In short:** this page lists every team command with its flags, the settings in `~/.lore/config.yaml`, the environment variables, and what each error code and exit status means. Use it to look things up. For step-by-step guides, start at the [team docs index](README.md).

**How this page is made.** The usage and flag blocks below are the real `--help` output of the lore binary. The error table is `lore errors list` joined with the exit statuses in `saas/pkg/aicoder/errcodes/registry.go`. `tests/team/docs/paste.py` regenerates them (`LORE_BIN=<lore> python3 tests/team/docs/paste.py`; with `--check` it fails when a block is stale). The environment variable and config key tables are written by hand and cited to the code that defines them, so `ds check` flags them when that code changes. Global flags every command accepts (`--color`, `--trace`, and on most commands `--db`, `--project`, `--read-only`, `--repo`, `--json`) are left out of the blocks.

Contents: [who you are](#who-you-are-and-which-org) · [stores](#stores) · [live rows](#live-rows-and-landing) · [sharing](#moving-rows-between-audiences) · [leak checks](#leak-checks-context-and-caches) · [tasks and missions](#tasks-and-missions-team-flags) · [agents](#agent-coordination) · [other kinds](#other-row-kinds-team-flags) · [environment variables](#environment-variables) · [config keys](#config-keys) · [error codes and exit statuses](#error-codes-and-exit-statuses)

## Who you are and which org

### `lore whoami`

<!-- help: whoami -->
Show the resolved org, what decided it, your identity and what loads here

```text
Usage:
  lore whoami

Examples:
  lore whoami
  lore whoami --explain        # every org rule with hit or miss
  LORE_ORG=agency-stag lore whoami
  lore whoami --json
  lore whoami --machine-label "tomas desktop"   # synced label of this machine ("" clears)

Flags:
      --explain                print every org rule with hit or miss and why
      --json                   machine-readable output
      --machine-label string   set this machine's label, synced with its profile to our own stores (MACHINE LABEL)
      --share                  on a terminal, also print store URLs and internal folders (hidden without a TTY or with CI set)
```
<!-- /help -->

### `lore config check`

<!-- help: config check -->
Validate ~/.lore/config.yaml (orgs, stores, live settings) and list every problem

```text
Usage:
  lore config check

Examples:
  lore config check
  HOME=/tmp/test-home lore config check

Flags:
      --json   JSON output
```
<!-- /help -->

### `lore identity set`

<!-- help: identity set -->
Persist an identity. Your identity key is always your email with human: in front: lore tells people apart by it, so a bare name is refused (the same person spelling it differently on two machines would become two people).

```text
Usage:
  lore identity set <email> | <name> --key human:<email> | --org <entry> --key human:<email> [--name …] [--handle …]

Examples:
  lore identity set alice@example.com
  lore identity set "Alice Doe <alice@example.com>"
  lore identity set "Alice Doe" --key human:alice@example.com
  lore identity set --org agency --key human:alice@agency.example --name "Alice Doe" --handle alice

Flags:
      --handle string   roster handle, without @
      --json            JSON output
      --key string      human:<your email> (with --org: your email in that org, TEAM-029)
      --name string     with --org: display name
      --org string      set your identity inside this org entry of ~/.lore/config.yaml
```
<!-- /help -->

### `lore org link`

<!-- help: org link -->
Link this repo to an internal project in the org's store

```text
Usage:
  lore org link <org> --project <folder>

Examples:
  lore org link agency --project client
  lore org link agency --project lore-tools --own
  lore org link agency --project client2 --default --dry-run

Flags:
      --default --to <org>   the link --to <org> uses when this repo has several
      --dry-run              validate and show the link, write nothing
      --own                  this is one of the org's own repos (only for own: patterns)
      --project string       the internal project folder in the store (required)
      --remote string        which remote, when the repo has several
      --store string         which of the org's stores (default: the first)
      --yes                  confirm without a prompt
```
<!-- /help -->

### `lore org unlink`

<!-- help: org unlink -->
Remove this repo's link to an internal project in the org's store

```text
Usage:
  lore org unlink <org> --project <folder>

Examples:
  lore org unlink agency --project client
  lore org unlink agency --project client --yes

Flags:
      --dry-run          show what would change, write nothing
      --project string   the internal project folder in the store (required)
      --remote string    which remote, when the repo has several
      --store string     which of the org's stores (default: the one holding the folder)
      --yes              confirm without a prompt
```
<!-- /help -->

### `lore org use`

<!-- help: org use -->
Choose the org entry for this project on this machine (not committed)

```text
Usage:
  lore org use <entry>

Examples:
  lore org use agency-stag     # work against the staging store branch here
  lore org use --clear             # back to automatic resolution

Flags:
      --clear   forget the choice for this project
```
<!-- /help -->

### `lore org set`

<!-- help: org set -->
Writes .lore/org.yaml so every teammate's lore picks the org in this repo with no config of their own. Names only, never URLs. Accepted only when the repo's remote matches the org's own: patterns; refused in client repos. Commit the file with git afterwards.

```text
Usage:
  lore org set <org> --commit

Examples:
  lore org set agency --commit
  lore org set agency --commit --project lore-tools

Flags:
      --commit           confirm writing the committed .lore/org.yaml
      --project string   the internal project folder (optional)
```
<!-- /help -->

### `lore org import`

<!-- help: org import -->
Adopt the org entry a team suggests in its store, with your identity, and join its roster

```text
Usage:
  lore org import <store-url>

Examples:
  lore org import git@github.com:agency/notes --key human:me@agency.example --name "Me" --handle me
  lore org import git@github.com:agency/notes --as agency-2

Flags:
      --as string       entry name, when the suggested one is taken
      --branch string   store branch to read the suggestion from (default: the store's default branch)
      --handle string   your @handle on the roster
      --key string      your identity key in this org: human:<your email there>
      --name string     your display name
      --yes             confirm without a prompt
```
<!-- /help -->

### `lore actor register`

<!-- help: actor register -->
Put yourself (or, with --kind agent, an agent you own) on the org roster

```text
Usage:
  lore actor register

Examples:
  lore actor register
  lore actor register --kind agent --label agent-frontend --owner @alice
  lore agent start --label agent-frontend --owner @alice   # same, and prints the session env

Flags:
      --kind string    human (default) or agent
      --label string   with --kind agent: the session label
      --owner string   with --kind agent: the owning human as @handle (default: you)
```
<!-- /help -->

### `lore actor deactivate`

<!-- help: actor deactivate -->
Mark someone as gone (active=false, left_at); actors are never deleted

```text
Usage:
  lore actor deactivate <@handle>

Examples:
  lore actor deactivate @carol
```
<!-- /help -->

### `lore team`

<!-- help: team -->
Teams on the org roster (@handle resolves to a person or a team)

```text
Usage:
  lore team <command>

Available Commands:
  add         Create or update a team; @handle then resolves to the team
  list        List the roster's teams
```
<!-- /help -->

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
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --reason string            why the detected text is not a secret (with --not-a-secret)
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --visibility string        internal (default) or shareable (CFG4)
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore store show`

<!-- help: store show -->
List the configured stores, their clones and (with --links) the link index

```text
Usage:
  lore store show

Examples:
  lore store show
  lore store show --links --json

Flags:
      --json    machine-readable output
      --links   also print the cached link index of every entry
```
<!-- /help -->

### `lore store open`

<!-- help: store open -->
Lore never needs a checkout of a store. This makes one for reading: a detached worktree, so lore can keep writing the branch itself (a ref checked out in any worktree is never written by lore).

```text
Usage:
  lore store open <url>

Examples:
  lore store open git@github.com:agency/notes

Flags:
      --branch string   store branch (default: the entry's branch)
```
<!-- /help -->

### `lore store forget`

<!-- help: store forget -->
Delete this machine's clone of a store (refused while it holds unpushed work)

```text
Usage:
  lore store forget <url>

Examples:
  lore store forget git@github.com:agency/notes
```
<!-- /help -->

### `lore store doctor`

<!-- help: store doctor -->
Check every configured store: reachable, marker and files, unpushed work, entries sharing the clone

```text
Usage:
  lore store doctor

Examples:
  lore store doctor
  lore store doctor --json

Flags:
      --json   machine-readable output
```
<!-- /help -->

## Live rows and landing

### `lore live show`

<!-- help: live show -->
List this project's live rows, or print one live file

```text
Usage:
  lore live show [id]
```
<!-- /help -->

### `lore live diff`

<!-- help: live diff -->
Local, tracking and remote live ref, and the writes not pushed yet

```text
Usage:
  lore live diff
```
<!-- /help -->

### `lore live log`

<!-- help: live log -->
History of the live ref with its epoch

```text
Usage:
  lore live log

Flags:
  -n, --number int   commits to show (default 20)
```
<!-- /help -->

### `lore live compact`

<!-- help: live compact -->
The live ref's history grows with every live write. compact replaces it with one parentless commit holding the same rows, raises the epoch in _live/epoch and keeps the old local copy under refs/lore-backup/live/ (never pushed). Teammates' clones re-apply only their own unpushed writes on top (F8). It also runs by itself once the ref has more than live.compact_after commits.

```text
Usage:
  lore live compact

Flags:
      --dry-run   print what would happen, change nothing
```
<!-- /help -->

### `lore live restore`

<!-- help: live restore -->
When the remote live ref was deleted or replaced (a mirror push, a mistaken delete), lore stops writing to it and keeps the local copy under refs/lore-backup/live/. restore pushes that copy back, leaving out rows that already landed on main and ids purged with `lore live purge` (§6.6).

```text
Usage:
  lore live restore
```
<!-- /help -->

Someone deleted `refs/lore/live` on the server. From then on every live write fails with `E_READ_ONLY` and `lore doctor` says why; `lore live restore` pushes the local copy back:

<!-- example: live-repair#live-ref-deleted only=3,4,5 -->
```console
# alice, in billing
$ git push -q origin :refs/lore/live && git ls-remote origin
601d2850ffd0	HEAD
601d2850ffd0	refs/heads/main
$ lore task add 'close the books for Q1'
ERROR: [E_READ_ONLY] the remote live ref was deleted or rewritten; lore stopped writing to it: live: the remote live ref was deleted or rewritten (local copy kept as refs/lore-backup/live/20261102T100020)
  hint: your local copy is kept under refs/lore-backup/live/; run `lore live restore` to put it back
(exit 23)
$ lore doctor | grep 'remote live ref'
  - live: the remote live ref was deleted or rewritten; writes are stopped until `lore live restore`
```
<!-- /example -->

<!-- example: live-repair#live-restore -->
```console
# alice, in billing
$ lore live restore
✓ restored refs/lore/live: 2 rows (left out 0 already landed or purged)
$ git ls-remote origin refs/lore/live
86dbae476bd6	refs/lore/live
$ lore task add 'close the books for Q1'
✓ tsk_162d…9666 — close the books for Q1 [medium]
lore live: (live, pushed)
$ lore task list
○ tsk_7a67…9504 send the March invoices [medium]
○ tsk_2ad3…f891 chase the unpaid February invoice [medium]
○ tsk_162d…9666 close the books for Q1 [medium]
```
<!-- /example -->

### `lore live purge`

<!-- help: live purge -->
purge removes the rows from the live ref and lists their ids in _live/purged.json, so a stale clone holding them never pushes them back and every clone drops them. Their old versions stay in the live ref's history until `lore live compact` rewrites it (§6.6). A copy that already landed on main must be removed from main separately.

```text
Usage:
  lore live purge <id>...
```
<!-- /help -->

### `lore land`

<!-- help: land -->
Live rows land on main by themselves when they are finished, and open rows are copied at live.max_age. land does it now for one row or a selection. --dry-run prints the plan with the reason for every row.

```text
Usage:
  lore land [id]

Examples:
  lore land tsk_02
  lore land --mission mis_12 --dry-run
  lore land --all --kind rules

Flags:
      --all              every live row of this project
      --dry-run          print the plan, change nothing
      --kind string      only rows of this kind (tasks, rules, …)
      --mission string   only the mission and its tasks
```
<!-- /help -->

### `lore explain`

<!-- help: explain -->
Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision

```text
Usage:
  lore explain <id>
```
<!-- /help -->

## Moving rows between audiences

### `lore move`

<!-- help: move -->
Move a row to another store (same id for rows on the core; asks first)

```text
Usage:
  lore move <id> --to <target>

Examples:
  lore move mem_0b77 --to agency
  lore move tsk_0201 --to repo --drop-internal

Flags:
      --drop-internal      into a client repo: remove the row's internal metadata values instead of refusing (MQ11)
      --json               print the result as JSON (rows on the 0.1.18 core)
      --to string          the target: repo | <org> | <org>/<folder> | me (required)
      --with-children      a task: move its whole subtree too; every sub-task keeps its id and parent (TASK 6.14)
      --with-time string   totals | none (default none): time totals for a task moved into a client repo (TQ3; totals not built yet)
      --yes                confirm without a prompt
```
<!-- /help -->

## Leak checks, context and caches

### `lore check leaks`

<!-- help: check leaks -->
Without flags, checks every staged file (not only .lore/) against the internal and personal rows loaded for this repo: a row whose id belongs elsewhere, a pointer to an internal row, 8 or more consecutive words of an internal note, or a secret. It fails closed when it cannot read the store.

```text
Usage:
  lore check leaks

Examples:
  lore check leaks                 # staged files, as the pre-commit hook does
  lore check leaks --commit-msg .git/COMMIT_EDITMSG
  lore check leaks --ci            # in the client's CI
  lore check leaks --as-client     # before handing an own repo to the client
  lore check leaks --explain

Flags:
      --as-client           check the whole repo as the client would see it (before a hand-over)
      --ci                  CI mode: ids and structure only, no store access, names nothing internal
      --commit-msg string   also check this commit message file (the commit-msg hook's $1)
      --explain             print the rule, the source and the matched words of each finding
      --staged              check the staged files (default unless --commit-msg is given alone)
```
<!-- /help -->

### `lore check pr-body`

<!-- help: check pr-body -->
Check a pull request body or comment before posting it (agents: always)

```text
Usage:
  lore check pr-body <file|->

Examples:
  lore check pr-body body.md
  gh pr view 12 --json body -q .body | lore check pr-body -

Flags:
      --explain   print the rule, the source and the matched words of each finding
```
<!-- /help -->

### `lore context`

<!-- help: context -->
Print the rules, hotfixes and notes for a file, topic or prompt (the agent's fetch command)

```text
Usage:
  lore context

Examples:
  lore context --for-file services/billing/invoice.go
  lore context --for-task tsk_01jf8e5w…
  lore context --topic billing
  lore context --topics
  lore context --for-prompt "invoice rounding"
  cat hook.json | lore context --hook claude_code --hook-stdin
  lore context --internal --table rules --json

Flags:
      --for-file string     rules, hotfixes and notes for this path
      --for-prompt string   rows matching this free text
      --for-task string     the task, its plan and links, then what applies to the files it names
      --hook string         answer a tool hook (claude_code, codex)
      --hook-stdin          with --hook: read the hook input JSON from stdin
      --internal            internal, global and personal rows (outside this checkout)
      --json                machine-readable output
      --max-bytes int       cap the output in bytes (default 16000; hooks: context.hook_cap)
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
  lore cache prune --dry-run
  lore cache prune
  lore cache prune --yes

Flags:
      --dry-run   list the cache files prune would delete, delete nothing
      --yes       delete without asking
```
<!-- /help -->

### `lore render`

<!-- help: render -->
render assembles the project's knowledge into a generated file (default .lore/LORE.md) and stitches an idempotent pointer block into the file each AI tool reads, so hand-written content there is never clobbered

```text
Usage:
  lore render

Flags:
      --check            CI check: write nothing, fail when LORE.md would be over the warn level or the budget
      --db string        override DB path (default: cwd's .lore/lore.db)
      --dry-run          print to stdout instead of writing file
      --json             with --report or --trend: machine-readable output
      --no-pointer       do not stitch the pointer into the agent file
      --out string       generated knowledge file path (default ".lore/LORE.md")
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
      --report           print the budget report (what is full, what is a line, what moved out) and write nothing
      --scopes           with --report: one row per generated file per tool, plus the Codex and Claude Code chain sums
      --target strings   tool profile name or pointer file to wire (repeatable; default: the repo's tool files, else AGENTS.md)
      --trend            bytes per render over the last 14 days, growth per day and when LORE.md reaches its budget; writes nothing
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
      --confirm                  v0.1.17 spelling: refused, use --yes
      --created-by string        actor_id (act_*); default: the row's created_by is the current identity
      --db string                v0.1.17 lore.db path: not used by this command (refused)
      --defer-until string       snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM); hides from active list until then
      --dry-run                  with --template: show the tasks that would be added, write nothing
      --due string               due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --estimate string          estimate: a number >= 0 (2 decimals) in the project's unit (tasks.estimate_unit); hours also take 90m, days 2d
      --json                     print the result (or the error) as JSON
      --key string               idempotency key: a re-run with the same key (in the same mission) adds nothing
      --lead string              the lead (@handle or actor); added to the assignees if missing
      --meta stringArray         set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray    set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string        read the value of this key from stdin (keeps it out of shell history)
      --mission string           mission_id (msn_*) to attach to
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
      --to string                store to write to (a name from .lore/stores.yaml; default repo)
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
      --commitment string             accepted | proposed | someday
      --confirm                       v0.1.17 spelling: refused, use --yes
      --db string                     v0.1.17 lore.db path: not used by this command (refused)
      --defer-until string            snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --dry-run                       with --where: list the tasks that would change, write nothing
      --due string                    due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)
      --estimate string               estimate in the project's unit (tasks.estimate_unit)
      --first                         place first among its siblings
      --json                          print the result (or the error) as JSON
      --last                          place last among its siblings
      --lead string                   make this person the lead (added to the assignees if missing)
      --meta stringArray              set metadata: key=value, key:type=value, project:key=value (repeatable)
      --meta-json stringArray         set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)
      --meta-stdin string             read the value of this key from stdin (keeps it out of shell history)
      --mission string                attach to mission_id (msn_*)
      --no-parent                     detach: make it a top-level task
      --not-a-secret                  keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --parent string                 move under this task (tsk_…), with its subtree
      --plan string                   attach to plan_id (pln_*)
      --priority string               low | medium | high | urgent
      --project string                accepted for v0.1.17 scripts; a row id already names its project
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
      --db string             v0.1.17 lore.db path: not used by this command (refused)
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
      --repo string           accepted for v0.1.17 scripts (task list shows every repo's tasks)
      --series string         only the instances of this recurrence series (rcs_…); add --all for closed ones
      --sort stringArray      sort by a task field, urgency, time_spent or elapsed: <key>, <key>:asc or <key>:desc (repeatable; ties keep the default order)
      --status string         filter: todo | in_progress | done | cancelled | blocked (empty = default active set, see --all)
      --store string          only tasks of this store (default every attached store)
      --top                   only top-level tasks
      --under string          only the whole subtree under this task (tsk_…)
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

Flags:
      --db string        v0.1.17 lore.db path: not used by this command (refused)
      --expand           fill in ${var:…}, ${res:…}, ${row:…} and ${env:…} references; secrets stay masked (SECRETS 2.7, 12)
      --from string      the source of the id: repo | <org> | <org>/<folder>
      --json             print the result (or the error) as JSON
      --project string   accepted for v0.1.17 scripts; a row id already names its project
      --read-only        refuse writes (also LORE_READ_ONLY=1)
      --relations        show every relation, both directions
      --tree             also print the sub-task tree
      --who              who created and last changed it (and, on the writing machine, with which session)
      --why              explain the urgency score term by term
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
      --confirm                  v0.1.17 spelling: refused, use --yes
      --db string                v0.1.17 lore.db path: not used by this command (refused)
      --force                    close even with unticked checklist items
      --json                     print the result (or the error) as JSON
      --lesson string            save a lesson learned (a memory pointing at the task)
      --no-lesson                skip the lesson reminder
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --pr int                   the pull request that finished the task
      --project string           accepted for v0.1.17 scripts; a row id already names its project
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
Merged records merged_at for a done task whose merge lore cannot detect (a squash template that drops the Lore-Task trailer, no recorded commit, no host adapter). Dependents waiting for :merged become ready.

```text
Usage:
  lore task merged <id>

Flags:
      --db string        override DB path (default: cwd's .lore/lore.db)
      --from string      the source of the id: repo | <org> | <org>/<folder>
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
```
<!-- /help -->

### `lore task cancel`

<!-- help: task cancel -->
Cancel a task

```text
Usage:
  lore task cancel <id>

Flags:
      --children string          open sub-tasks: cancel | done them in the same write (required when there are any)
      --confirm                  v0.1.17 spelling: refused, use --yes
      --db string                v0.1.17 lore.db path: not used by this command (refused)
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --project string           accepted for v0.1.17 scripts; a row id already names its project
      --read-only                refuse writes (also LORE_READ_ONLY=1)
      --reason string            close reason: cancelled (default) | duplicate | wont_do | obsolete | stale
      --secret-ref stringArray   replace detected secrets with this vault reference (<manager>://<locator>; repeat once per finding)
      --secret-unstored          replace every detected secret with ${secret:unstored} and add a store-then-rotate task
      --yes                      answer yes to the confirmation without a terminal
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
      --confirm                  v0.1.17 spelling: refused, use --yes
      --created-by string        actor_id (act_*); default: the row's created_by is the current identity
      --db string                v0.1.17 lore.db path: not used by this command (refused)
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
      --to string                store to write to (a name from .lore/stores.yaml; default repo)
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore mission status`

<!-- help: mission status -->
The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done

```text
Usage:
  lore mission status <mission-id>

Flags:
      --json   JSON output
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
      --confirm                  v0.1.17 spelling: refused, use --yes
      --db string                v0.1.17 lore.db path: not used by this command (refused)
      --dry-run                  show what would be written; write nothing
      --json                     print the result (or the error) as JSON
      --not-a-secret             keep a detected secret as a false positive (needs --reason; --yes without a terminal)
      --project string           accepted for v0.1.17 scripts; a row id already names its project
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
Start registers an agent actor (kind agent, the label, owned by a person) on the org roster and prints LORE_ACTOR and LORE_SESSION for the agent's shell. Every claim, heartbeat and done by the agent then carries that actor and session (F18).

```text
Usage:
  lore agent start

Flags:
      --hook-stdin       session hook: read the tool's hook JSON from stdin
      --label string     the agent's label (one actor per label), e.g. agent-frontend
      --model string     session hook: the model the tool reports (never guessed)
      --owner string     the owning person (@handle); default: you
      --session string   session hook: the tool's own session id
      --tool string      session hook: the tool's slug (claude_code, codex, …); records details only
```
<!-- /help -->

### `lore task claim`

<!-- help: task claim -->
Claim takes a lease on a task with a unique token, pushed with a compare-and-swap on refs/lore/signals. Exactly one session holds a task at a time; renew it with 'lore task heartbeat'. Fails closed (exit 15, E_OFFLINE) when the push cannot be confirmed.

```text
Usage:
  lore task claim <task-or-mission-id>

Flags:
      --coordinate     take a coordinator lease on a parent with open sub-tasks (TASK 6.9)
      --from string    the source of the id: repo | <org> | <org>/<folder>
      --owns strings   paths this claim owns (directory prefixes ending in / or exact files)
      --strict         refuse instead of warning when --owns overlaps another claim
```
<!-- /help -->

### `lore task next`

<!-- help: task next -->
Next returns the task you already hold, or claims the most urgent ready leaf task: open, no open sub-tasks, not gated, no live claim, every --after dependency (its own and its ancestors') satisfied. Order is the urgency score (TASK 13): --why explains every point, --dry-run ranks and explains without claiming. Exit 10 (E_NO_WORK) lists why each task waits; exit 11 (E_CONTENDED) means every claim lost a race.

```text
Usage:
  lore task next

Flags:
      --dry-run          rank and explain without claiming (nothing written)
      --json             with --dry-run: the ranking as JSON (urgency + terms)
      --mission string   only tasks of this mission
      --owns strings     paths the claim owns
      --repo string      only tasks of this repo (a repo name of the group)
      --role string      plan role (e.g. coordinator)
      --why              explain the urgency score term by term
```
<!-- /help -->

### `lore task heartbeat`

<!-- help: task heartbeat -->
Heartbeat renews the lease of a claim you hold: signals only, the task row is never touched. Without an id it renews every claim of this session, at most once per lease.renew_every (safe to call from an agent tool hook). --daemon keeps renewing until the claim ends or the parent process exits; if renewals keep failing it stops at expires − grace with exit 12 (E_LOST_CLAIM).

```text
Usage:
  lore task heartbeat [id]

Flags:
      --daemon   keep renewing until the claim ends or the parent exits
```
<!-- /help -->

### `lore task release`

<!-- help: task release -->
Give back a claim you hold

```text
Usage:
  lore task release <id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder>
```
<!-- /help -->

### `lore task note`

<!-- help: task note -->
Note saves a checkpoint a replacement agent can resume from. When you hold the task it is fenced (exit 12–14 if your claim is gone) and pushed with your claim, and your current work branch is pushed to the remote and recorded on the task.

```text
Usage:
  lore task note <id> <text>

Flags:
      --everywhere    with --to me: file the note under everywhere/ (loads in every repo)
      --force-org     write even though LORE_ORG or lore org use disagrees with a store link
      --from string   the source of the id: repo | <org> | <org>/<folder>
      --to string     where the row goes: repo | <org> | <org>/<folder> | me | me/<org>
```
<!-- /help -->

### `lore task revoke`

<!-- help: task revoke -->
Drop the current claim on a task (human override; the holder is fenced out)

```text
Usage:
  lore task revoke <id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder>
```
<!-- /help -->

### `lore task reassign`

<!-- help: task reassign -->
Reassign sets the assignee and raises claim_epoch, so a holder's next write fails with exit 14 (E_REASSIGNED). With --from @handle instead of an id it reassigns every open task assigned to that person (offboarding, cookbook 9).

```text
Usage:
  lore task reassign [id]

Flags:
      --assign string   the new assignee (@handle)
      --from string     reassign every open task of this person
```
<!-- /help -->

### `lore gate request`

<!-- help: gate request -->
Request sets the task's gate to requested. Run by the agent holding the task, it is fenced like any holder write (exit 12–14 when the claim is gone).

```text
Usage:
  lore gate request <id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder>
```
<!-- /help -->

### `lore gate approve`

<!-- help: gate approve -->
Approve records approved_by, approved_at and a hash of title + body: editing either voids the approval. Only a human identity from ~/.lore/config.yaml (or the identity file) approves — never an agent, never LORE_ACTOR — and only a listed approver when the mission names approvers. Fails closed (exit 15) when the push is not confirmed.

```text
Usage:
  lore gate approve <id>

Flags:
      --from string   the source of the id: repo | <org> | <org>/<folder>
```
<!-- /help -->

Who may approve: any person (human) on the org's roster, never an agent. A mission's `approvers:` list narrows it to the people listed.

### `lore hold claim`

<!-- help: hold claim -->
Claim holds a named resource on the project's signals ref, exclusively, until --ttl passes or you release it; running claim again renews your hold. Fails closed (exit 15, E_OFFLINE) when the push cannot be confirmed.

```text
Usage:
  lore hold claim <name>

Flags:
      --on string      project (the project's signals ref) or machine (default "project")
      --ttl duration   how long the hold lasts (default: lease.ttl)
```
<!-- /help -->

### `lore hold release`

<!-- help: hold release -->
Release a resource you hold

```text
Usage:
  lore hold release <name>
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
      --confirm                  v0.1.17 spelling: refused, use --yes
      --db string                v0.1.17 lore.db path: not used by this command (refused)
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
      --store string             store to write to (a name from .lore/stores.yaml; default repo)
      --task string              the task it is about (its mission is recorded too)
      --to string                deprecated alias of --for
      --yes                      answer yes to the confirmation without a terminal
```
<!-- /help -->

### `lore finding add`

<!-- help: finding add -->
Finding saves a memory pinned to a mission. It is written live (shared at once), like the mission, and shows up in the inbox of everyone working the mission.

```text
Usage:
  lore finding add --mission <id> <text>

Flags:
      --mission string   the mission the finding belongs to
```
<!-- /help -->

### `lore inbox`

<!-- help: inbox -->
Inbox lists, grouped by mission: tasks assigned to you, handoffs for you, gates you can approve, expired claims and poison tasks of missions you own, and findings. --ack marks everything seen (local to this checkout); --since limits to recent items.

```text
Usage:
  lore inbox

Flags:
      --ack              mark everything shown as seen
      --json             JSON output
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

<!-- ds:block id=team-env-live-hc3w5qby -->
<!-- ds:block id=team-env-actor-53pv3fuc -->
<!-- ds:block id=team-env-org-m38knvc4 -->
| Variable | Effect |
|---|---|
| `LORE_ORG` | choose the org entry for this shell or one command (rule 1 of org resolution); the only way, with `lore org use`, to select a staging entry |
| `LORE_ACTOR` | the actor for this session; accepted only for **agent** actors on the org's roster (a human key here is ignored with a warning, and gate approvals never come from it). `lore agent start` prints it |
| `LORE_SESSION` | the agent session id recorded on claims; printed by `lore agent start` |
| `LORE_LIVE_AUTOPUSH` | `0` = lore never touches the network on its own (same as `live.autopush: false`); live writes wait locally, coordination writes fail with `E_OFFLINE` |
| `LORE_LIVE` | `0` = no live rows at all in this shell: every row behaves as before live rows existed (an escape hatch for a repo that cannot use them yet) |
| `LORE_LIVE_REMOTE` | the remote that carries `refs/lore/*` (default `origin`; for forks and tests) |
| `LORE_TRACE` | `1` = like `--trace`: print every git command lore runs for live rows, with timing |
| `LORE_RIDE_ACTIVE` | set by lore itself while it makes a ride-along commit, so its own hooks do not run again; never set it by hand |
| `LORE_NOW` | a fixed clock (RFC 3339), read only by test builds (`-tags loretest`); release binaries ignore it |

## Config keys

All keys live in `~/.lore/config.yaml` (mode 0600; `lore config check` validates it and names every problem). Keys under `live`, `lease`, `agents` and `merge_check` may also be set inside one `orgs[]` entry to override the top level for that org. A team can suggest org defaults in its store's `<org_folder>/.lore/lore-org.yaml`; for `defaults.client_repos` the store's value wins over a looser personal one.

<!-- ds:block id=team-config-defaults-vcrgzmhf -->
<!-- ds:block id=team-live-kinds-62s7pcxa -->
| Key | Default | Meaning |
|---|---|---|
| `identity.key`, `identity.name` | — | your identity in repos that match no org, once orgs exist |
| `personal.store` | `local` | `local` (`~/.lore/personal`) or the git URL of your own private store |
| `orgs[].name` | — | the entry's name; also the `--to <name>` target |
| `orgs[].identity.key`, `.name`, `.handle` | — | who you are in this org (`human:<email there>`), required when the entry resolves |
| `orgs[].stores[].url` | — | a store repo (must hold `lore-store.yaml`) |
| `orgs[].stores[].branch` | `main` | the store branch this entry uses; no `/`. Not the store's default branch = a staging entry |
| `orgs[].stores[].org_folder` | — | on exactly one store: the folder with the roster, teams and company rules (`.` = the store root) |
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

## Error codes and exit statuses

Agents and scripts can branch on the exit status without parsing text. These numbers are a contract: they are never renumbered. Every other error exits 1.

<!-- ds:block id=team-exit-codes-vt2j5m98 -->
<!-- errors -->
| Code | Exit status | Meaning |
|---|---|---|
| `E_NO_WORK` | 10 | `lore task next` found no ready task; the message lists why each candidate is blocked |
| `E_CONTENDED` | 11 | ready work exists but every claim attempt lost a race; retry after a short wait |
| `E_LOST_CLAIM` | 12 | your claim token is no longer on the signals ref (lease expired or reclaimed); stop work on this task |
| `E_CANCELLED` | 13 | a human cancelled the task while you held it; stop work on this task |
| `E_REASSIGNED` | 14 | a human reassigned the task while you held it; stop work on this task |
| `E_OFFLINE` | 15 | a coordination write (claim, renew, release, done, approve, hold) could not be confirmed by the remote; nothing was queued |
| `E_AMBIGUOUS_ID` | 16 | the id exists in two loaded sources; name one with --from |
| `E_ORG_AMBIGUOUS` | 17 | two org entries match this repo at the same rule; choose one with `lore org use <entry>` |
| `E_ORG_DISAGREES` | 17 | LORE_ORG or `lore org use` names a different org than the store link; name the target with --to <org> |
| `E_LINK_UNKNOWN` | 18 | lore cannot tell whether this repo is a client repo (store unreachable, no cached link index); pass --to or retry online |
| `E_NOT_A_STORE` | 19 | the repo is not a lore store (no lore-store.yaml marker, or it holds non-lore files) |
| `E_LEAK_BLOCKED` | 19 | the write would put internal content where the client can read it; see `lore check leaks --explain` |
| `E_CYCLE` | 19 | the dependency would create a cycle between tasks |
| `E_OLD_LORE` | 19 | this lore binary is below the live ref's min_lore_version; run lore upgrade (reads still work) |
| `E_CONFIG_INVALID` | 1 | ~/.lore/config.yaml failed validation; run `lore config check` |
| `E_REFUSED` | 23 | a fixed lore rule refuses this; the message names the rule and what to do instead |
<!-- /errors -->
