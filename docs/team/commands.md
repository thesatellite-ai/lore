# Commands reference

Every command, flag, environment variable, config key and exit status that lore for teams adds or changes.

**How this page is made.** The usage and flag blocks below are the real `--help` output of the lore binary, and the error table is `lore errors list` joined with the exit statuses in `saas/pkg/aicoder/errcodes/registry.go`. `tests/team/docs/paste.py` regenerates them (`LORE_BIN=<lore> python3 tests/team/docs/paste.py`; with `--check` it fails when a block is stale). The environment variable and config key tables are written by hand and cited to the code that defines them, so `ds check` flags them when that code changes. Global flags every command accepts (`--color`, `--trace`, and on most commands `--db`, `--project`, `--read-only`, `--repo`, `--json`) are left out of the blocks.

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
  LORE_ORG=northwind-stag lore whoami
  lore whoami --json

Flags:
      --explain   print every org rule with hit or miss and why
      --json      machine-readable output
      --share     on a terminal, also print store URLs and internal folders (hidden without a TTY or with CI set)
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
```
<!-- /help -->

### `lore identity set`

<!-- help: identity set -->
Persist an identity: the fallback (~/.lore/identity.yaml) or one org's (--org)

```text
Usage:
  lore identity set <name> | --org <entry> --key human:<email> [--name …] [--handle …]

Examples:
  lore identity set alice
  lore identity set --org northwind --key human:alice@northwind.example --name "Alice Doe" --handle alice

Flags:
      --handle string   with --org: roster handle, without @
      --key string      with --org: human:<your email in that org> (TEAM-029)
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
  lore org link northwind --project acme
  lore org link northwind --project lore-tools --own
  lore org link northwind --project globex --default --dry-run

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
  lore org unlink northwind --project acme
  lore org unlink northwind --project acme --yes

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
  lore org use northwind-stag     # work against the staging store branch here
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
  lore org set northwind --commit
  lore org set northwind --commit --project lore-tools

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
  lore org import git@github.com:northwind/lore --key human:me@northwind.example --name "Me" --handle me
  lore org import git@github.com:northwind/lore --as northwind-2

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
  lore actor register --kind agent --label frontend-2 --owner @alice
  lore agent start --label frontend-2 --owner @alice   # same, and prints the session env

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
  lore actor deactivate @dev3

Flags:
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

Flags:
```
<!-- /help -->

## Stores

### `lore store init`

<!-- help: store init -->
Make an empty repo a lore store (writes lore-store.yaml and pushes)

```text
Usage:
  lore store init <url>

Examples:
  lore store init git@github.com:northwind/lore
  lore store init git@github.com:northwind/lore --branch staging

Flags:
      --branch string       the store branch to make a lore (default: the entry's branch, else main); may not contain /
      --org-folder string   org folder recorded in lore-store.yaml (default: the entry's org_folder, else org)
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
  lore store open git@github.com:northwind/lore

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
  lore store forget git@github.com:northwind/lore

Flags:
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

Flags:
```
<!-- /help -->

### `lore live diff`

<!-- help: live diff -->
Local, tracking and remote live ref, and the writes not pushed yet

```text
Usage:
  lore live diff

Flags:
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

Flags:
```
<!-- /help -->

### `lore live purge`

<!-- help: live purge -->
purge removes the rows from the live ref and lists their ids in _live/purged.json, so a stale clone holding them never pushes them back and every clone drops them. Their old versions stay in the live ref's history until `lore live compact` rewrites it (§6.6). A copy that already landed on main must be removed from main separately.

```text
Usage:
  lore live purge <id>...

Flags:
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
Show every copy of a row (live, this branch, main), which one wins and why, and its land decision

```text
Usage:
  lore explain <id>

Flags:
```
<!-- /help -->

## Moving rows between audiences

### `lore share`

<!-- help: share -->
Copy a row to a wider audience (new id; shows the text and asks)

```text
Usage:
  lore share <id> --to <target>

Examples:
  lore share mem_0c12 --to repo
  lore share mem_77 --to northwind

Flags:
      --to string   the target: repo | <org> | <org>/<folder> | me (required)
      --yes         confirm without a prompt (never accepted from agent actors)
```
<!-- /help -->

### `lore move`

<!-- help: move -->
Move a row to another project (new id; the original is deleted; asks first)

```text
Usage:
  lore move <id> --to <target>

Examples:
  lore move mem_0b77 --to northwind

Flags:
      --to string   the target: repo | <org> | <org>/<folder> | me (required)
      --yes         confirm without a prompt (never accepted from agent actors)
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
  lore check leaks --explain

Flags:
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
Print internal, global and personal context for tools that cannot follow an import

```text
Usage:
  lore context --internal

Examples:
  lore context --internal
  lore context --internal --table rules --json

Flags:
      --internal        internal, global and personal rows (outside this checkout)
      --json            machine-readable output
      --search string   only rows whose title or body contains this text
      --table string    only this table (rules, memories, decisions, tasks, …)
```
<!-- /help -->

### `lore cache prune`

<!-- help: cache prune -->
Remove caches and internal renders of projects not used for --older-than (default 90d)

```text
Usage:
  lore cache prune

Examples:
  lore cache prune
  lore cache prune --older-than 30d

Flags:
      --older-than string   idle time before a cache is removed (e.g. 30d, 2160h); default 90d
```
<!-- /help -->

### `lore render`

<!-- help: render -->
render assembles the project's knowledge into a generated file (default .lore/LORE.md) and stitches an idempotent @import pointer into the agent file (default CLAUDE.md) so hand-written CLAUDE.md content is never clobbered

```text
Usage:
  lore render

Flags:
      --db string        override DB path (default: cwd's .lore/lore.db)
      --dry-run          print to stdout instead of writing file
      --no-pointer       do not stitch the pointer into the agent file
      --out string       generated knowledge file path (default ".lore/LORE.md")
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
      --target string    agent file to stitch the @import pointer into (default "CLAUDE.md")
```
<!-- /help -->

## Tasks and missions (team flags)

### `lore task add`

<!-- help: task add -->
Add a new task

```text
Usage:
  lore task add <title>

Flags:
      --after strings            dependencies: <id>[:done|:merged][,…] (default: done in the same repo, merged across repos)
      --assign string            assign to @handle (a person sets the assignee, a team the assigned team)
      --assigned-to string       actor_id (act_*) the task is assigned to
      --body string              longer description
      --commitment string        accepted | proposed | someday (REQUIRED for agent callers)
      --created-by string        actor_id (act_*); defaults to current identity
      --db string                override DB path (default: cwd's .lore/lore.db)
      --defer-until string       snooze until date (YYYY-MM-DD); hides from active list until then
      --due string               due date (YYYY-MM-DD)
      --everywhere               with --to me: file the note under everywhere/ (loads in every repo)
      --force-org lore org use   write even though LORE_ORG / lore org use disagrees with a store link
      --json                     print the new task as JSON (incl. its id)
      --key string               idempotency key: a re-run with the same key (in the same mission) adds nothing
      --live                     share this row with everyone at once on the live ref (refs/lore/live)
      --mission string           mission_id (msn_*) to attach to
      --no-live                  keep this row with the code (this branch's .lore/data)
      --plan string              plan_id (pln_*) to attach to
      --priority string          low | medium | high | urgent (default "medium")
      --project string           override project (id or name)
      --read-only                skip lock acquisition; refuse writes
      --ref stringArray          a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable
      --repo string              scope to a specific repo (mount_name or rep_id)
      --role task next --role    plan role matched by task next --role (e.g. coordinator)
      --tasklist string          tasklist_id (tlt_*); default: the project's default list (created on demand)
      --timebox duration         time box, e.g. 60m (active lease time summed across holders)
      --to string                where the row goes: repo | <org> | <org>/<folder> | me | me/<org>
```
<!-- /help -->

### `lore task edit`

<!-- help: task edit -->
Edit task fields (only flags you pass are applied)

```text
Usage:
  lore task edit <id>

Flags:
      --after strings           dependencies: <id>[:done|:merged][,…] (default: done in the same repo, merged across repos)
      --assign string           assign to @handle (a person sets the assignee, a team the assigned team)
      --assigned-to string      actor_id (act_*) to assign to
      --body string             new body
      --clear-assignee          unassign
      --clear-defer             un-defer (clear deferred_until)
      --clear-due               remove due date
      --clear-mission           detach from mission
      --clear-plan              detach from plan
      --commitment string       accepted | proposed | someday
      --db string               override DB path (default: cwd's .lore/lore.db)
      --defer-until string      snooze until date YYYY-MM-DD
      --due string              due date YYYY-MM-DD
      --from string             the source of the id: repo | <org> | <org>/<folder>
      --live                    share this row with everyone at once on the live ref (refs/lore/live)
      --mission string          attach to mission_id (msn_*)
      --no-live                 keep this row with the code (this branch's .lore/data)
      --plan string             attach to plan_id (pln_*)
      --priority string         low | medium | high | urgent
      --project string          override project (id or name)
      --read-only               skip lock acquisition; refuse writes
      --ref stringArray         a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable
      --repo string             scope to a specific repo (mount_name or rep_id)
      --role task next --role   plan role matched by task next --role (e.g. coordinator)
      --status string           todo | in_progress | done | cancelled | blocked
      --tasklist string         move to tasklist_id (tlt_*)
      --timebox duration        time box, e.g. 60m (active lease time summed across holders)
      --title string            new title
```
<!-- /help -->

### `lore task list`

<!-- help: task list -->
List tasks

```text
Usage:
  lore task list

Flags:
      --all                 show everything: done/cancelled + proposed/someday + deferred
      --all-repos           every repo of the project (the default for team views)
      --assignee string     filter by assignee: actor_id (act_*), @me, stable_key or display name
      --claimed-by string   only tasks claimed right now by @handle
      --commitment string   filter: accepted | proposed | someday
      --db string           override DB path (default: cwd's .lore/lore.db)
      --include-deferred    also include deferred (deferred_until in the future)
      --include-proposed    also include commitment=proposed
      --include-someday     also include commitment=someday
      --json                JSON output
      --mine                assigned to me or my team, and unclaimed or claimed by me
      --mission string      filter by mission_id
      --project string      override project (id or name)
      --read-only           skip lock acquisition; refuse writes
      --repo string         scope to a specific repo (mount_name or rep_id)
      --status string       filter: todo | in_progress | done | cancelled | blocked (empty = default active set, see --all)
```
<!-- /help -->

### `lore task show`

<!-- help: task show -->
Show task details

```text
Usage:
  lore task show <id>

Flags:
      --db string        override DB path (default: cwd's .lore/lore.db)
      --from string      the source of the id: repo | <org> | <org>/<folder>
      --json             JSON output
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
      --signals          show the claim as seen now (holder, token prefix, expires, renewed_seq)
```
<!-- /help -->

### `lore task done`

<!-- help: task done -->
Done finishes a task. On a task you hold, it is fenced: your claim token must still be on the signals ref (else exit 12/13/14) and the row change is pushed atomically with the claim's removal. --commit/--pr record the code so lore can tell when it merged (F9). Never prompts: --lesson saves a lesson, --no-lesson skips the reminder.

```text
Usage:
  lore task done <id>

Flags:
      --commit string    the commit that finished the task
      --db string        override DB path (default: cwd's .lore/lore.db)
      --from string      the source of the id: repo | <org> | <org>/<folder>
      --lesson string    save a lesson learned (a memory pointing at the task)
      --no-lesson        skip the lesson reminder
      --pr int           the pull request that finished the task
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
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
Cancel a task (a held task's holder is fenced out: E_CANCELLED)

```text
Usage:
  lore task cancel <id>

Flags:
      --db string        override DB path (default: cwd's .lore/lore.db)
      --from string      the source of the id: repo | <org> | <org>/<folder>
      --project string   override project (id or name)
      --read-only        skip lock acquisition; refuse writes
      --repo string      scope to a specific repo (mount_name or rep_id)
```
<!-- /help -->

### `lore mission add`

<!-- help: mission add -->
Add a new mission

```text
Usage:
  lore mission add <title>

Flags:
      --approvers strings        only these people (@handle,…) may approve the mission's gates
      --body string              longer description
      --created-by string        actor_id (act_*); defaults to current identity
      --db string                override DB path (default: cwd's .lore/lore.db)
      --everywhere               with --to me: file the note under everywhere/ (loads in every repo)
      --force-org lore org use   write even though LORE_ORG / lore org use disagrees with a store link
      --json                     JSON output (the created mission)
      --key string               idempotency key: a re-run with the same key adds nothing
      --live                     share this row with everyone at once on the live ref (refs/lore/live)
      --no-live                  keep this row with the code (this branch's .lore/data)
      --project string           override project (id or name)
      --read-only                skip lock acquisition; refuse writes
      --repo string              scope to a specific repo (mount_name or rep_id)
      --target string            target completion date (YYYY-MM-DD)
      --to string                where the row goes: repo | <org> | <org>/<folder> | me | me/<org>
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

### `lore plan apply`

<!-- help: plan apply -->
Apply reads a plan:

```text
Usage:
  lore plan apply <file>

Flags:
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
      --label string   the agent's label (one actor per label), e.g. frontend-2
      --owner string   the owning person (@handle); default: you
```
<!-- /help -->

### `lore task claim`

<!-- help: task claim -->
Claim takes a lease on a task with a unique token, pushed with a compare-and-swap on refs/lore/signals. Exactly one session holds a task at a time; renew it with 'lore task heartbeat'. Fails closed (exit 15, E_OFFLINE) when the push cannot be confirmed.

```text
Usage:
  lore task claim <task-or-mission-id>

Flags:
      --from string    the source of the id: repo | <org> | <org>/<folder>
      --owns strings   paths this claim owns (directory prefixes ending in / or exact files)
      --strict         refuse instead of warning when --owns overlaps another claim
```
<!-- /help -->

### `lore task next`

<!-- help: task next -->
Next returns the task you already hold, or claims the first ready task: open, not gated, no live claim, every --after dependency satisfied. Exit 10 (E_NO_WORK) lists why each task waits; exit 11 (E_CONTENDED) means every claim lost a race.

```text
Usage:
  lore task next

Flags:
      --mission string   only tasks of this mission
      --owns strings     paths the claim owns
      --repo string      only tasks of this repo (a repo name of the group)
      --role string      plan role (e.g. coordinator)
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
      --everywhere               with --to me: file the note under everywhere/ (loads in every repo)
      --force-org lore org use   write even though LORE_ORG / lore org use disagrees with a store link
      --from string              the source of the id: repo | <org> | <org>/<folder>
      --to string                where the row goes: repo | <org> | <org>/<folder> | me | me/<org>
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

### `lore resource claim`

<!-- help: resource claim -->
Claim holds a named resource on the project's signals ref, exclusively, until --ttl passes or you release it; running claim again renews your hold. Fails closed (exit 15, E_OFFLINE) when the push cannot be confirmed.

```text
Usage:
  lore resource claim <name>

Flags:
      --scope string   project (the project's signals ref) or machine (default "project")
      --ttl duration   how long the hold lasts (default: lease.ttl)
```
<!-- /help -->

### `lore resource release`

<!-- help: resource release -->
Release a resource you hold

```text
Usage:
  lore resource release <name>

Flags:
```
<!-- /help -->

### `lore handoff add`

<!-- help: handoff add -->
Add leaves a handoff for a person or agent on the roster; it shows in their inbox, grouped under the task's mission.

```text
Usage:
  lore handoff add --for <@handle> [--task <id>] <text>

Flags:
      --body string   the handoff text (instead of the positional text)
      --for string    who the handoff is for (@handle)
      --live          share this row with everyone at once on the live ref (refs/lore/live)
      --no-live       keep this row with the code (this branch's .lore/data)
      --task string   the task it is about
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

`lore memory add`, `lore rule add`, `lore decision add` (and the other `add` and `edit` commands of shared kinds) accept the same team flags:

| Flag | Meaning |
|---|---|
| `--to repo\|<org>\|<org>/<folder>\|me\|me/<org>` | which project the row is written to: this repo, the linked internal project, a store folder, or your personal store |
| `--create` | with `--to <org>/<folder>`: create that store folder if it does not exist yet (without it an unknown folder is refused) |
| `--everywhere` | with `--to me`: file the note under `everywhere/` so it loads in every repo |
| `--live` / `--no-live` | share this row with everyone at once on the live ref, or keep it with the code |
| `--force-org` | write even though `LORE_ORG` or `lore org use` disagrees with a store link |

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
| `live.defaults.<kind>` | tasks, missions, handoffs, comments: `true`; rules, decisions, memories: `false` | which kinds are live without `--live` |
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
| `E_ORG_DISAGREES` | 17 | LORE_ORG or `lore org use` names a different org than the store link; pass --force-org to override |
| `E_LINK_UNKNOWN` | 18 | lore cannot tell whether this repo is a client repo (store unreachable, no cached link index); pass --to or retry online |
| `E_NOT_A_STORE` | 19 | the repo is not a lore store (no lore-store.yaml marker, or it holds non-lore files) |
| `E_LEAK_BLOCKED` | 19 | the write would put internal content where the client can read it; see `lore check leaks --explain` |
| `E_CYCLE` | 19 | the dependency would create a cycle between tasks |
| `E_OLD_LORE` | 19 | this lore binary is below the live ref's min_lore_version; upgrade lore (reads still work) |
| `E_CONFIG_INVALID` | 1 | ~/.lore/config.yaml failed validation; run `lore config check` |
<!-- /errors -->
