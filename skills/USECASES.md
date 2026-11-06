# Use-case index

**In short:** a map from "I want to do X" to the one command that does it. Skim it when you are not sure which command to reach for.

Map of "I want to do X" → exact command. Skim this first when you're unsure which command to reach for.

## Capture knowledge

| Intent | Command |
|---|---|
| Persist any free-form fact | `lore memory add --body="<text>"` |
| Persist a hard constraint with severity | `lore rule add --severity=must --body="<text>"` |
| Persist a decision rationale (ADR-style) | `lore decision add --title="<t>" --body="<why>"` |
| Persist a recurring warning | `lore hotfix add --severity=high --title="<headline>" --body="<text>"` |
| Reusable code pattern | `lore pattern add --title=<n> --body="<code>"` |
| Reusable procedure | `lore playbook add --title=<n> --body="<steps>"` |
| Reusable model prompt | `lore prompt add --title=<n> --body="<prompt>"` |
| Architectural note | `lore architecturenote add --title=<t> --body=<b>` |
| Behaviour mode preset | `lore behaviour add --title=<n> --body=<b>` |
| Recipe (cookbook entry) | `lore cookbookrecipe add --title=<n> --body=<b>` |
| Incident postmortem | `lore incident add --title=<t> --body=<b>` |
| Improvement suggestion | `lore suggestion add --title=<t> --body=<b>` |
| Taste preference | `lore tastepref add --body=<b>` |

## Track work

| Intent | Command |
|---|---|
| Create an initiative | `lore mission add "<title>"` |
| Create a discrete work item | `lore task add "<title>" --priority=<p>` (`--tasklist=<tlt_id>` optional: the default list is used, created on demand) |
| Attach task to mission | `lore task add "<t>" --tasklist=<tlt_id> --mission=<msn_id>` |
| Attach task to plan | `lore task add "<t>" --tasklist=<tlt_id> --plan=<pln_id>` |
| Assign task to an actor | `lore task add "<t>" --tasklist=<tlt_id> --assigned-to=<act_id>` |
| Supersede a memory/rule/decision/hotfix/pattern | `lore <kind> add ... --supersedes=<old_id>` |
| Mark a memory as validated by an actor | `lore memory add ... --validated-by=<act_id>` |
| Authored-by override (any entity) | `--created-by=<act_id>` (defaults to current identity) |
| Move task to another tasklist | `lore task edit <id> --tasklist=<tlt_id>` |
| Reassign task | `lore task edit <id> --assigned-to=<act_id>` |
| Detach task from mission | `lore task edit <id> --clear-mission` |
| Rename / re-body any editable entity | `lore <kind> edit <id> --title=… --body=…` |
| Group tasks (alt grouping) | `lore tasklist add --title=<t>` |
| Multi-phase plan | `lore plan add --title=<t>` |
| Hand off context to a collaborator | `lore handoff add --for @<handle> [--task <id>] "<ctx>"` |
| Active task list view | `lore task list --status=in_progress` |
| My tasks / tasks assigned to someone | `lore task list --assignee=@me` (or `--assignee=<act_id\|stable_key\|display name>`) |
| Start a task | `lore task start <id\|tsk_<id>>` |
| Finish a task | `lore task done <id\|tsk_<id>>` |
| Cancel a task | `lore task cancel <id\|tsk_<id>>` |
| Pause / resume a mission | `lore mission pause <id>` / `mission resume <id>` |
| Acknowledge a handoff | `lore handoff ack <id>` |
| Future reminder | `lore reminder add "<msg>" --due=YYYY-MM-DD` |
| Recurring reminder | `... --recurrence=7d\|30d\|1m\|3m\|6m\|1y` |
| Tag/label any entity | `lore tag attach --on-table=<t> --on-id=<id> --tag=<n>` |
| Discuss any entity | `lore comment add --on-table=<t> --on-id=<id> --body="<text>"` |

## Teams, stores and agents

Full guide with real examples: [docs/team](../docs/team/README.md).

| Intent | Command |
|---|---|
| See which org, identity and stores apply here, and why | `lore whoami` (`--explain` for every rule) |
| Share a row with the whole team at once, on any branch | `lore task add "<title>"` (tasks are live by default); other kinds: `--live` |
| Keep a row with the branch instead | `--no-live` |
| Give a task to a teammate | `lore task add "<title>" --assign @<handle>` |
| See a teammate's new row now (skip the 60 s fetch cache) | `lore sync` |
| Land finished live rows on main now | `lore land --all` (`--dry-run` shows the plan) |
| Why did this row (not) land, which copy wins | `lore explain <id>` |
| Make a new empty repo a store | `lore store init <url>` (another store branch: `--branch <b>`) |
| Tie a client repo to its internal project (once, for the whole team) | `lore org link <org> --project <folder>` |
| Join the org's roster | `lore actor register` |
| Keep an internal note out of a client repo | `lore memory add --body "<text>"` (default in a client repo) or `--to <org>` |
| Put a row into the client repo on purpose | `--to repo` |
| A private note only you see | `--to me` (`--everywhere` to load it in every repo) |
| Choose an org for this repo on this machine | `lore org use <entry>` (one command: `LORE_ORG=<entry> lore …`) |
| Share an internal note with the client on purpose | `lore move <id> --to repo` (shows the text, asks) |
| A row was filed in the wrong place | `lore move <id> --to <target>` |
| Check a PR body for internal content before posting | `lore check pr-body <file>` (or `-` for stdin) |
| An internal row reached the client's live ref | `lore live purge <id>`, then `lore live compact` |
| Start an agent session | `lore agent start --label <name> --owner @<you>` and export what it prints |
| Create a mission and its tasks from a file (re-run adds nothing) | `lore mission apply <file>` |
| Take the next ready task as an agent | `lore task next --mission <id> [--repo <name>]` (exit 10: nothing ready) |
| Keep an agent's claim alive | `lore task heartbeat --daemon <id>`, started from a process that lives as long as the agent (it stops when the process that started it exits), or `lore task heartbeat` from the agent tool's hook |
| Record progress and push the work branch | `lore task note <id> "<text>"` |
| Finish with the code that did it | `lore task done <id> --commit <sha>` (or `--pr <n>`) |
| Mark a done task merged when lore cannot see it | `lore task merged <id>` |
| Stop agents until a person approves | `lore gate request <id>`; the person runs `lore gate approve <id>` |
| The whole mission at a glance | `lore mission status <id>` |
| What needs me | `lore inbox` |

## Lifecycle / cleanup

| Intent | Command |
|---|---|
| Archive an entity (soft-delete) | `lore <kind> archive <id>` (memory, rule, decision, hotfix, pattern, playbook, prompt, project, repo) |
| Unarchive | `lore <kind> unarchive <id>` |
| Invalidate a memory at a point in time (bitemporal) | `lore memory invalidate <id>` — sets `valid_until=now`; preserves the row in history |
| Supersede a knowledge row with audit trail | `lore <kind> add ... --supersedes=<old_id>` (memory, rule, decision, hotfix, pattern) |

## Lock CLAUDE.md against agent drift

| Intent | Command |
|---|---|
| Install the agent directive at the top of CLAUDE.md | `lore directive install` |
| Same, but into AGENTS.md (or both) | `lore directive install --target=AGENTS.md` (or `--target=CLAUDE.md,AGENTS.md`) |
| Print the directive block (no write) | `lore directive show` |

## Retrieve / search

| Intent | Command |
|---|---|
| BM25 full-text search across memories | `lore memory search "<q>"` |
| Same for any other entity | `lore <kind> search "<q>"` — works on 23 entities (rule, decision, hotfix, pattern, playbook, prompt, architecturenote, behaviour, cookbookrecipe, incident, suggestion, tastepref, snapshot, handoff, mission, task, tasklist, plan, workflow, workspace, techdoc, comment) |
| Search task title + body, eager-load relations | `lore task search "auth*" --json` (returns task + tasklist + mission + plan) |
| Restrict to specific columns | `lore <kind> search "<q>" --column=title,body` |
| List memories newest-first | `lore memory list --json` |
| Phrase search | `lore memory search '"exact phrase"'` |
| Boolean search | `lore memory search "redis OR cache"` |
| Wildcard prefix | `lore memory search "auth*"` |
| Negation | `lore rule search "auth NOT login"` |
| Scoped to one repo | `... --repo=<mount>` |
| Across all repos | `... --all-repos` |
| Master only (no repo) | `... --master-only` |
| Strict scope (no inheritance) | `... --no-inherit` |
| Include archived | `... --include-archived` |
| Index health / row counts | `lore search status [--json]` |
| Force rebuild index | `lore search rebuild [--kind=<entity>]` |

## Upgrade / migrate

| Intent | Command |
|---|---|
| First-time setup on a fresh project | `lore init` (runs setup internally) |
| After upgrading the binary | nothing: the cache and search index rebuild themselves; `lore migrate run` only for v0.1.17 data |
| Install the coding tools' hooks | `lore setup --agent-hooks` (optional; `--dry-run` shows the changes) |
| Limit | `... --limit=<n>` |
| Show details for any entity | `lore <kind> show <id>` |
| List by status | `lore <kind> list --status=<s>` (where applicable) |

## Render + introspect

| Intent | Command |
|---|---|
| Compile `.lore/LORE.md` from DB + `@import` pointer into CLAUDE.md (hybrid; pins only directive + must-rules + critical/high hotfixes) | `lore render` |
| Stitch the pointer into a different agent file | `lore render --target=AGENTS.md` |
| Change the generated knowledge-file path | `lore render --out=docs/LORE.md` |
| Write the generated file only (no pointer stitch) | `lore render --no-pointer` |
| See what render would produce (the `.lore/LORE.md` body) | `lore render --dry-run` |
| What did the agent actually see? | `lore why-context` |
| Same as above in JSON | `lore why-context --json` |
| Last rendered text | `lore why-context --rendered` |

## Project + repo management

| Intent | Command |
|---|---|
| Bootstrap new project (Mode A) | `lore init --non-interactive` |
| Share notes between repos (replaces Mode B) | `lore store init <name> file://<abs-path>` once, `lore store add <name> <url>` in each other repo; an old Mode B repo: `lore migrate run` |
| List projects in cwd's DB | `lore project list` |
| Show project + repos | `lore project show <name> --json` |
| Add a repo | `lore repo add <mount> --origin=<url>` |
| List repos in current project | `lore repo list` |

## Health + recovery

| Intent | Command |
|---|---|
| Is everything OK? | `lore doctor` (exit 0 healthy, 1 degraded, 2 broken) |
| JSON health snapshot | `lore doctor --json` |
| Repair what doctor can fix safely (`--supersedes` links written by older versions) | `lore doctor --fix` (safe to re-run) |
| Keep a safe copy of the notes | commit `.lore/data` (the files are the truth) |
| Rebuild a damaged DB | `mv .lore/lore.db .lore/lore.db.old && lore sync` |
| Rebuild the search index | `lore search rebuild` |
| Capture bug bundle | `lore support-bundle --out=<path>` |

## Identity

| Intent | Command |
|---|---|
| Who am I to lore? | `lore identity show` |
| Override identity | `lore identity set "<Name> <email>"` (the identity key is the email; a bare name is refused) |

## Diagnostics

| Intent | Command |
|---|---|
| Binary + schema versions | `lore version --json` |
| Full error code registry | `docs/reference/cli-commands.md#errors` (generated by `lore spec docs`) |
| Help for any command | `lore <cmd> --help` |

## Read-only / CI

| Intent | Command |
|---|---|
| Refuse writes globally | `LORE_READ_ONLY=1 lore ...` |
| Per-command read-only | `lore ... --read-only` |
| Verify the rendered knowledge is up to date | `diff .lore/LORE.md <(lore render --dry-run)` |

## When in doubt

```bash
lore --help
lore <command> --help
lore doctor               # what is wrong right now
```

Every list/show command supports `--json`. Every error has a code starting with `E_`. Every command takes `--color auto|always|never` and `--trace`; commands that open the local database also take `--project`, `--repo` and `--read-only` (`--db` is refused on row commands).
