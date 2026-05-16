# Use-case index

Map of "I want to do X" → exact command. Skim this first when you're unsure which command to reach for.

## Capture knowledge

| Intent | Command |
|---|---|
| Persist any free-form fact | `lore memory add "<text>"` |
| Persist a hard constraint with severity | `lore rule add --severity=must "<text>"` |
| Persist a decision rationale (ADR-style) | `lore decision add --title="<t>" --body="<why>"` |
| Persist a recurring warning | `lore hotfix add --severity=high "<text>"` |
| Reusable code pattern | `lore pattern add --name=<n> --body="<code>"` |
| Reusable procedure | `lore playbook add --name=<n> --body="<steps>"` |
| Reusable LLM prompt | `lore prompt add --name=<n> --body="<prompt>"` |
| Architectural note | `lore architecturenote add --title=<t> --body=<b>` |
| Behaviour mode preset | `lore behaviour add --name=<n> --body=<b>` |
| Recipe (cookbook entry) | `lore cookbookrecipe add --name=<n> --body=<b>` |
| Incident postmortem | `lore incident add --title=<t> --body=<b>` |
| Improvement suggestion | `lore suggestion add --title=<t> --body=<b>` |
| Taste preference | `lore tastepref add --name=<n> --body=<b>` |

## Track work

| Intent | Command |
|---|---|
| Create an initiative | `lore mission add "<title>"` |
| Create a discrete work item | `lore task add "<title>" --tasklist=<tlt_id> --priority=<p>` (tasklist required) |
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
| Hand off context to a collaborator | `lore handoff add --to=<name> --body=<ctx>` |
| Active task list view | `lore task list --status=in_progress` |
| Start a task | `lore task start <id\|T-N>` |
| Finish a task | `lore task done <id\|T-N>` |
| Cancel a task | `lore task cancel <id\|T-N>` |
| Pause / resume a mission | `lore mission pause <id>` / `mission resume <id>` |
| Acknowledge a handoff | `lore handoff ack <id>` |
| Future reminder | `lore reminder add "<msg>" --due=YYYY-MM-DD` |
| Recurring reminder | `... --recurrence=7d\|30d\|1m\|3m\|6m\|1y` |
| Tag/label any entity | `lore tag attach --on-table=<t> --on-id=<id> --tag=<n>` |
| Discuss any entity | `lore comment add --on-table=<t> --on-id=<id> "<text>"` |

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
| Strip the directive block | `lore directive remove [--target=...]` |

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
| After upgrading the binary (new columns or new search registry) | `lore setup` — runs ent migration + rebuilds FTS index + stamps fingerprint |
| Check whether setup is needed | Any command will print `hint: run lore setup` on stderr if fingerprint is stale; otherwise silent |
| Limit | `... --limit=<n>` |
| Show details for any entity | `lore <kind> show <id>` |
| List by status | `lore <kind> list --status=<s>` (where applicable) |

## Render + introspect

| Intent | Command |
|---|---|
| Compile CLAUDE.md from DB (hybrid; pins only directive + must-rules + critical/high hotfixes) | `lore render` |
| Render to a different target file | `lore render --target=AGENTS.md` |
| See what render would produce | `lore render --dry-run` |
| What did the AI actually see? | `lore why-context --last-render` |
| Same as above in JSON | `lore why-context --last-render --json` |
| Last rendered text | `lore why-context --last-render --rendered` |

## Project + repo management

| Intent | Command |
|---|---|
| Bootstrap new project (Mode A) | `lore init --non-interactive` |
| Bootstrap shared-DB project (Mode B) | `lore project shared-init --db=<path>` |
| List projects in cwd's DB | `lore project list` |
| Show project + repos | `lore project show <name> --json` |
| Add a repo | `lore repo add <mount> --origin=<url>` |
| List repos in current project | `lore repo list` |

## Health + recovery

| Intent | Command |
|---|---|
| Is everything OK? | `lore doctor` (exit 0 healthy, 1 degraded, 2 broken) |
| JSON health snapshot | `lore doctor --json` |
| Create a backup | `lore backup [--out=<path>]` |
| Restore from a backup | `lore restore <path> --confirm` |
| Auto-repair (WAL → backup → bootstrap) | `lore repair --tier=2 --confirm` |
| Capture bug bundle | `lore support-bundle --out=<path>` |

## Identity

| Intent | Command |
|---|---|
| Who am I to aicoder? | `lore identity show` |
| Override identity | `lore identity set --kind=human --display=<name>` |

## Diagnostics

| Intent | Command |
|---|---|
| Binary + schema versions | `lore version --json` |
| Full error code registry | `lore errors list --json` |
| Help for any command | `lore <cmd> --help` |

## Read-only / CI

| Intent | Command |
|---|---|
| Refuse writes globally | `LORE_READ_ONLY=1 lore ...` |
| Per-command read-only | `lore ... --read-only` |
| Verify CLAUDE.md is up to date | `diff CLAUDE.md <(lore render --dry-run)` |

## When in doubt

```bash
lore --help
lore <command> --help
lore errors list --json   # what can go wrong
```

Every list/show command supports `--json`. Every error has a code starting with `E_`. Every command accepts the universal flags (`--db`, `--project`, `--repo`, `--read-only`, `--color`).
