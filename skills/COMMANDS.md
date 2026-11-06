# Commands Reference

**In short:** every lore command with its main flags, one example and a sample of its `--json` output. Look a command up here when you know what you want to do.

Every command is listed below with: signature, key flags, example, and JSON output sample.

Every command takes `--color auto|always|never` and `--trace`; most take `--json`. Commands that open the local database also take `--project`, `--repo` and `--read-only` (`--db` is refused on row commands: their rows are files in the repository). See SKILL.md.

## Bootstrap

### `lore init [path]`
Initializes a Mode A project at cwd. Creates `.lore/lore.db`, applies schema, registers project, auto-adds `lore.db` to `.gitignore`, and writes the first sync pass to `.lore/data/` (commit it). In a clone that already has `.lore/data/` it adopts the shared project instead of minting a new one.

```bash
lore init --non-interactive --name=my-app
# ✓ lore initialized at /path/to/my-app
#     project_id: prj_019e...
#     name:       my-app
#     identity:   human:alice@example.com (git config)
```

Flags: `--name=<str>`, `--non-interactive`.

### `lore store init` / `lore store add` (notes shared by several repos)
One folder store holds the notes several repos share; each repo attaches it once. (The v0.1.17 shared-database mode, `lore project shared-init`, is retired; `lore migrate run` converts a repo that still has its `.lore/lore.yaml` pointer.)

```bash
lore store init client2 "file://$HOME/stores/client2"   # in the first repo: make the store, attach it
lore store add client2 "file://$HOME/stores/client2"    # in every other repo: attach it
lore rule add --to client2 --body "Every screen works on a phone"
lore migrate run --dry-run                              # in a v0.1.17 Mode B repo: what moves
```

### Bringing in existing markdown
There is no importer in 0.1.18 (`learn-from` and `learn` are dropped). Read CLAUDE.md / AGENTS.md / .cursorrules / README section by section and add each item that is still true with the matching command: `rule add` (must/should), `decision add` (a choice + why), `memory add` (a fact), `hotfix add` (a recurring trap). Then delete the old file and commit.

---

## Memory

```bash
# add
lore memory add --body="Tailwind v4 only" [--repo=web] [--kind=procedural] [--tag=frontend] [--yes]

# search (FTS5 BM25; LIKE fallback)
lore memory search "tailwind" [--limit=10] [--all-repos|--master-only|--no-inherit] [--include-archived] [--json]

# list — repo-scoped, SAME scope semantics as search
lore memory list [--repo=web | --all-repos | --master-only | --no-inherit] [--json]
#   default (no --repo)      → master rows only (repo_id IS NULL)
#   --repo=web               → that repo's rows + master (inherited)
#   --repo=web --no-inherit  → that repo's rows only
#   --all-repos              → every row regardless of repo
#   --master-only            → master rows only
```

> Scope binds at **`add`** time and filters on **`list`/`search`** for every knowledge kind (`memory`, `rule`, `decision`, `hotfix`, `pattern`, `architecturenote`). `--repo` on `add` persists `repo_id`; the same scope flags filter `list` and `search` identically. You do **not** need `[REPO-NAME]` body prefix tags for per-repo organization — real `repo_id` scoping is the supported mechanism.
>
> **Re-scoping an existing row** (`memory/rule/decision/hotfix/pattern/architecturenote`): use the explicit rebind flags on `edit`:
>
> ```bash
> lore <kind> edit <id> --rebind-repo=<mount>   # move row to that repo (sets repo_id) lore <kind> edit <id> --rebind-master         # move row to master (clears repo_id)
> ```
>
> `--rebind-repo` errors loudly if the mount is unknown. Note: bare `--repo` on `edit` is **context-only** (which repo's data to resolve) and never mutates scope — it now prints a loud note instead of silently no-op'ing. For an audited body+scope change (new row + history chain) use `lore <kind> add "<body>" --supersedes=<old_id> --repo=<mount>` instead. Verify with `lore <kind> list --repo=<mount> --no-inherit`.

Kinds (typed enum, validated): `core | retrieved | episodic | procedural | archival`. Default `retrieved`. Quick guide:

- `core` — foundational, always-included project fact
- `retrieved` — surfaced from search; default for ad-hoc captures
- `episodic` — tied to a specific event/session
- `procedural` — how-to / steps
- `archival` — preserved for forensics; low render weight

JSON envelope:
```json
{
  "schema_version": 1,
  "query": "tailwind",
  "count": 1,
  "results": [
    {"id":"mem_019e...","body":"Tailwind v4 only","kind":"manual","trust_score":0.5,"scope":"master"}
  ]
}
```

---

## Rule (hard constraints)

```bash
lore rule add --severity=must --activation=always --body="no fmt.Println in prod" [--globs='["**/*.go"]']
lore rule list [--json]
lore rule show <id> [--json]
```

| Severity | Effect |
|---|---|
| `must` | Blocking. Verifier fails if violated. |
| `should` | Warning. |
| `may` | Suggestion. |

| Activation | When rendered |
|---|---|
| `always` | Always |
| `glob` | Match against `--globs` JSON array |
| `semantic` | Query-similarity match (v0.2 embedding) |
| `manual` | Only when explicitly referenced |

---

## Decision (ADR-style)

```bash
lore decision add --title="Use SQLite not Postgres" --body="local-first; zero ops"
lore decision list [--json]
lore decision show <id> [--json]
```

Status: `proposed | accepted | superseded | rejected`.

---

## Hotfix (loud recurring warnings)

```bash
lore hotfix add --severity=high --title="ent regen wipes resolver helpers" --body="Beware: ent regen wipes resolver/ helpers"
lore hotfix list [--json]
lore hotfix show <id> [--json]
```

Severity: `info | low | medium | high | critical`. Hotfixes are **never truncated** under render budget pressure.

---

## Mission + Task (project management)

```bash
# mission = container for tasks
lore mission add "Ship v0.1" --target=2026-06-30 --body="..." [--json]   # --json: the new mission (.data.id)
lore mission list [--status=active|paused|done|cancelled] [--json]
lore mission show <id> [--json]   # --json: mission fields + .data.tasks
lore mission done <id>

# task = discrete work — `--tasklist` is REQUIRED.
# `--commitment` is REQUIRED for agent callers (no default; missing = hard error)
lore task add "Wire FTS5 backend" \
  --tasklist=tlt_<id> \
  --commitment=accepted \    # accepted | proposed | someday  (agents MUST pass this)
  --priority=high \
  --defer-until=2026-07-01 \ # optional snooze; hidden until then, auto-resurfaces
  --mission=msn_<id> \
  --plan=pln_<id> \
  --due=2026-05-20 \
  --assigned-to=act_<id> \
  --created-by=act_<id>      # optional — defaults to current identity
lore task list [--status=…] [--commitment=…] [--mission=<id>] [--assignee=<ref>] \
               [--include-proposed] [--include-someday] [--include-deferred] [--all] [--json]
                                   # default = ActiveTask (accepted, not done/cancelled, not future-deferred)
                                   # --assignee: filter by assignee: actor_id (act_*), @me, stable_key or display name
                                   #   (a display name shared by several actors is refused with the candidate ids)
lore task triage                   # commitment=proposed (agent-suggested, not committed)
lore task someday                  # commitment=someday (parking lot)
lore task deferred                 # snoozed: deferred_until in the future
lore task show <id> [--json]
lore task start <id>           # auto-promotes commitment=accepted, clears defer
lore task done <id>            # auto-promotes commitment=accepted, clears defer
lore task cancel <id>
lore task search <query> [--all]   # default surfaces only ActiveTask hits

# task edit — reparent, reassign, reprioritize, commit/snooze in one place
lore task edit <id> \
  [--title=…] [--body=…] [--priority=…] [--status=…] \
  [--commitment=accepted|proposed|someday] \
  [--defer-until=YYYY-MM-DD | --clear-defer] \
  [--due=YYYY-MM-DD | --clear-due] \
  [--tasklist=tlt_…] \
  [--mission=msn_… | --clear-mission] \
  [--plan=pln_… | --clear-plan] \
  [--assigned-to=act_… | --clear-assignee]
```

Priority: `low | medium | high | urgent`. Status: `todo | in_progress | done | cancelled | blocked`. Commitment: `accepted | proposed | someday` (orthogonal to status; agents must set it explicitly on `add`).

**Required fields for `task add`:** `<title>` (positional) and `--tasklist=<tlt_id>`. Create the tasklist first (`lore tasklist add --title=...`) if none exists. **Agent callers must also pass `--commitment`** — `accepted` (user asked / doing now), `proposed` (your speculative idea), or `someday` (parking lot). No default; omitting it as an agent is a hard error so speculative tasks can't silently pollute the active list.

**Relation flags (apply across `<entity> add`):**
- `--mission`, `--tasklist`, `--plan` — group/parent relations (task)
- `--repo` — scope to a repo (memory, rule, decision, hotfix, pattern, architecturenote, task)
- `--supersedes=<id>` — supersede a previous row (memory, rule, decision, hotfix, pattern)
- `--created-by=<act_id>` — actor that authored (auto-filled from current identity if omitted; available on every `add` whose schema has `created_by_actor_id`)
- `--assigned-to=<act_id>` — task assignee
- `--validated-by=<act_id>` — memory validator
- `--from=<act_id>`, `--to=<act_id>` — handoff sender / receiver

Task JSON eager-loads parent IDs (`mission_id`, `tasklist_id`, `plan_id`).

## Inspectors / config / security (round-36 audit pass)

```bash
# actor — inspect who's writing
lore actor list [--json]
lore actor show <act_id> [--json]

# snapshot — point-in-time knowledge captures
lore snapshot add --title="<t>" --body="<body>"     # body via arg or stdin
lore snapshot list [--json]
lore snapshot show <id> [--json]
lore snapshot archive / unarchive <id>

# secret rules — your own secret detectors (settings rows; pii-pattern add|enable|disable refuse)
lore secrets rules
lore secrets rule add <rule-id> --regex=<re> [--description=<d>] [--to <store>]
lore secrets rule off|on <rule-id>
lore pii-pattern list              # read-only: patterns copied from v0.1.17, not checked

# task view — saved task-list filters (rows in .lore/data, travel with git)
lore task view add <name> --where '<filter>' [--sort <key>[:asc|:desc]] [--group-by <field>]
lore task view list|show|edit|delete
lore task list --view <name>

# external-source — registered outside sources (a list; nothing reads them yet)
lore external-source list [--json]
lore external-source add --name=<n> --kind=<k> [--config=<json>]
lore external-source enable / disable <id>

# techdoc — external documentation refs
lore techdoc add --name=<n> [--base-url=<u>] [--description=<d>]
lore techdoc list [--json]
lore techdoc show <id> [--json]

# config — DB-level KV (dbconfig table)
lore config get <key>
lore config set <key> <value>
lore config list [--json]
```

## Search (FTS5 — per-entity, multi-column, BM25-ranked)

Every text-bearing entity exposes a `search` subcommand. Powered by SQLite FTS5 with Porter stemming + per-column BM25 weights + snippet highlighting.

```bash
# Per-entity search
lore task search "<query>"       [--limit=20] [--all] [--json]
lore memory search "<query>"
lore rule search "<query>"
lore decision search "<query>"
lore hotfix search "<query>"
lore pattern search "<query>"
lore playbook search "<query>"
lore prompt search "<query>"
lore architecturenote search "<query>"
lore behaviour search "<query>"
lore cookbookrecipe search "<query>"
lore incident search "<query>"
lore suggestion search "<query>"
lore tastepref search "<query>"
lore snapshot search "<query>"
lore handoff search "<query>"
lore mission search "<query>"
lore tasklist search "<query>"
lore plan search "<query>"
lore workflow search "<query>"
lore workspace search "<query>"
lore techdoc search "<query>"
lore comment search "<query>"
```

**Query syntax** (FTS5):
- `auth*` — prefix wildcard
- `"connection pool"` — exact phrase
- `redis OR cache` — boolean
- `auth NOT login` — negation
- `(X OR Y) NOT Z` — grouping
- `{title body}: auth` — column-scoped (rarely needed; `--column=` is cleaner)

**Common flags on every search:**
- `--column=<a,b,c>` — restrict MATCH to specific columns
- `--limit=N` — cap results (default 20)
- `--include-archived` — include soft-deleted rows
- `--repo=<m> | --all-repos | --master-only | --no-inherit` — standard scope
- `--json` — envelope with eager-loaded relations

**JSON shape** (every entity returns the same envelope):
```json
{
  "schema_version": 1,
  "kind": "task.search",
  "query": "auth*",
  "count": 2,
  "data": [{
    "id": "tsk_...",
    "score": 1.7e-6,
    "snippet": "refactor <b>authorization</b> layer",
    "row": {"title": "...", "body": "...", "status": "todo", "priority": "medium"},
    "relations": {"tasklist": {...}, "mission": {...}, "plan": {...}}
  }]
}
```

`task search` eager-loads tasklist + mission + plan. Other entities load their per-schema relations (e.g. handoff loads from_actor + to_actor).

## Global search (cross-entity, one call)

```bash
# One call returns ranked hits across all 23 text-bearing entities
lore search "auth middleware"
lore search "redis OR cache"
lore search "auth NOT login" --json
lore search "auth*" --tables=memory,rule,decision --limit=10

# Output shape
# [rule      ] rul_<id>   <body excerpt>  (score=4.2)
# [decision  ] dec_<id>    <title>          (score=3.8)
# [memory    ] mem_<id>   <body excerpt>  (score=3.4)
# [hotfix    ] hfx_<id>   <title>          (score=2.9)
```

**Flags:**
- `--tables=<a,b,c>` restrict to specific entities (memory, rule, decision, …)
- `--limit=N` total hits after merge (default 20)
- `--per-table=N` per-entity top-K before merge (default 10)
- `--include-archived` include soft-deleted rows
- `--repo / --all-repos / --master-only` scope flags
- `--json` envelope output

**How it works:** runs FTS5 MATCH against every entity's per-table index in parallel, merges per-table rankings via Reciprocal Rank Fusion (RRF, k=60), returns the top-K by fused score. Each hit has `entity_table`, `id`, score, and snippet.

**Per-entity search** (`lore <kind> search "X"`) still works when you want to scope to one type. Global is the cross-cutting hammer.

## Search-index admin

```bash
lore search status                # per-entity row counts + drift health
lore search status --json         # envelope for scripts
lore search rebuild               # drop + recreate every FTS table; reindex all
lore search rebuild --kind=memory # one entity only
```

## Render (hybrid)

```bash
lore render                        # hybrid: directive + hotfixes + severity=must rules → .lore/LORE.md
lore render --target=AGENTS.md     # stitch the @import pointer into AGENTS.md instead of CLAUDE.md
lore render --out=docs/LORE.md     # change the generated knowledge-file path
lore render --no-pointer           # write the generated file only; leave the agent file untouched
lore render --dry-run              # stdout, no write
```

Render writes the compiled body to `.lore/LORE.md` and stitches an idempotent `@import` pointer into `CLAUDE.md`. See the **Render** section later in this file for the exact pointer shape.

**Hybrid model — what's pinned vs searched** (pinned content lives in `.lore/LORE.md`, which CLAUDE.md `@import`s):

| Entity | Pinned in `.lore/LORE.md` | Searched on demand |
|---|---|---|
| Directive | always | — |
| Rules | `severity=must` | `severity=should` / `may` |
| Hotfixes | `severity=critical` + `high` | `severity=medium` / `low` |
| Memories, decisions, patterns, architecturenotes, taste prefs, playbooks | — | always |

Symmetric principle: pin only the **non-skippable subset** (constraints + warnings agents can't afford to miss). Everything else fetched via `lore search` per the directive's pre-response checklist. Keeps CLAUDE.md small even as the project's knowledge grows (rules + hotfixes can accumulate into hundreds without bloating the rendered file).

## Setup / migration (after upgrade)

Nothing to run after `git pull && task lore:install:all` or any upgrade: the local cache and search index rebuild themselves from `.lore/data`, and the machine store schema updates when it is opened. `lore setup` is optional (a safe, idempotent schema update); `lore setup --agent-hooks` installs the coding tools' session and context hooks. The one real step is for data written by v0.1.17: `lore migrate run` (`--dry-run` first).

## Git commit linking (polymorphic)

Anchor "this work shipped as `<sha>`" against any entity — task, run, mission, decision, memory, etc. Backed by the `commit_links` table.

```bash
# Link the current HEAD commit to tsk_<id>. Auto-captures sha + message + author + committed-at.
lore link add --entity=tsk_<id> --commit=HEAD

# Specific sha
lore link add --entity=tsk_<id> --commit=abc1234

# With explicit metadata override (when not in a git repo)
lore link add --entity=tsk_<id> --commit=abc1234 --message="feat: wire auth" --author="Alice <a@x>"

# Multi-repo project: point at a sibling repo
lore link add --entity=tsk_<id> --commit=HEAD --repo-path=../web-app

# Link a run to its closing commit
lore link add --entity=$RID --commit=HEAD

# Same commit closes multiple tasks
lore link add --entity=tsk_<id> --commit=HEAD
lore link add --entity=tsk_<id> --commit=HEAD

# List
lore link list                       # all links in this project
lore link list --entity=tsk_<id>          # commits linked to tsk_<id>
lore link list --commit=abc1234      # entities linked to a sha
lore link list --json

# Reverse lookup
lore commit-show abc1234             # all entities for this sha

# Remove
lore link remove cml_xxx --confirm
```

**Note**: `--commit=HEAD` runs `git rev-parse HEAD` + `git log -1 --format=...` to capture full sha, message, author, and committed-at automatically. Pass a literal sha if you're outside the repo or want a specific commit.

**Duplicates**: a `(entity, sha)` pair is unique — re-linking the same commit to the same entity errors. Link the same sha to a *different* entity is fine.

## Run timing (agent-driven)

A run times one attempt at a task: which agent, which model, how it ended, tokens and cost. Lore does NOT execute runs; the agent (a coding tool, a human or a hook) opens and closes them. When the run names a task, `run end` / `run cancel` add the run's span to that task's time (`lore task time show <task-id>`). The run row itself stays on this machine.

```bash
# Open a run before starting work; prints "<run-id> started"
RID=$(lore run start \
    --task=tsk_<id> \
    --model=<model-id> \
    --agent=my-agent \
    --goal="wire auth middleware against JWT provider" | awk '{print $1}')

# Close
lore run end $RID --outcome=success --summary="shipped" --tokens-in=1200 --tokens-out=340 --cost=0.05
# Or fail / cancel
lore run end $RID --outcome=failed --error="timeout fetching JWKS"
lore run cancel $RID --reason="scope changed"

# The time it added to the task
lore task time show tsk_<id>
```

**Outcomes:** `success`, `partial`, `failed`, `cancelled`.

**No steps, list, show or replay.** A run is a timer, not a transcript. Record what was tried or decided as a memory or decision row instead.

**Retries**: pass `--retry-of=<prior-run-id>` on `run start` to link the chain. Useful for "tsk_<id> failed twice before succeeding".

## Agent directive

The CLI ships a loud, prescriptive directive block you can pin to the top of any agent-loaded markdown so future agent sessions don't inline-write memories/tasks into the file (and don't create sibling `NOTES.md` / `LEARNINGS.md` scratchpads as a workaround):

```bash
lore directive install                                # default: CLAUDE.md at cwd
lore directive install --target=AGENTS.md             # any file
lore directive install --target=CLAUDE.md,AGENTS.md   # repeatable
lore directive show [--full]                          # print block to stdout (--full: the long form)
# to remove the block, delete the lines between the sentinel markers by hand
```

Idempotent — re-running `install` replaces the block in place via sentinel markers (`<!-- lore:directive:start -->` … `:end -->`). Creates the file if missing.

**`<entity> edit <id>` is available on every entity where in-place mutation is meaningful:**

| Entity | Editable fields |
|---|---|
| task | title, body, priority, status, due (+ clear), tasklist, mission (+ clear), plan (+ clear), assigned-to (+ clear) |
| mission | title, body, status, target (+ clear) |
| memory | body, kind, source, source-ref *(use `--supersedes` on `add` if you want audit trail)* |
| rule | body, severity, activation, globs, source-ref *(supersede preferred for body)* |
| decision | title, body, status, source-ref *(supersede preferred for body)* |
| hotfix | title, body, severity *(supersede preferred for body)* |
| pattern | title, body *(supersede preferred for body)* |
| plan, tasklist, suggestion | title, body, status |
| playbook, prompt, behaviour, workflow, workspace | name, body |
| architecture-note, cookbook-recipe | title, body |
| tastepref | body, scope |

**No `edit` for:** incident (audit trail), handoff (point-in-time message), reminder (mark done + recreate), comment / tag (delete + recreate cheap).

Only flags you actually pass are applied — pass `--field` to set, `--clear-<field>` for nullable fields.

## Archive / lifecycle verbs

| Verb | Where it applies | Notes |
|---|---|---|
| `<kind> archive <id>` | memory, rule, decision, hotfix, pattern, playbook, prompt, project, repo | Soft-delete via `archived_at`. Row stays in DB; queries omit it unless `--archived` is passed (where supported). |
| `<kind> unarchive <id>` | same set | Clear `archived_at`. |
| `memory invalidate <id>` | memory only | Bitemporal: sets `valid_until=now`. Use when knowledge becomes wrong as of a point in time without supersede chain. |
| `memory list [--archived]` | memory | Browse memories; symmetric with `rule list` / `decision list`. |
| `memory show <id>` | memory | Detail view incl. archived_at, valid_until, superseded_by. |
| `mission pause <id>` / `mission resume <id>` | mission | Status enum transitions (`active ↔ paused`). |
| `handoff ack <id>` | handoff | Sets `status_str=acked` after the receiver has processed it. |
| `<kind> delete <id> --confirm` | 25 entities (memory, rule, decision, hotfix, pattern, playbook, prompt, snapshot, project, repo, task, mission, tasklist, plan, architecturenote, behaviour, cookbookrecipe, incident, suggestion, tastepref, workflow, workspace, comment, handoff, reminder, techdoc) | **Hard delete; no undo.** `--confirm` flag required. Prefer `archive` for entities that have it (reversible). |

---

## Plan / TaskList / Workflow / Workspace / Handoff

Same shape — `add | list | show`. Each `list/show` supports `--json`.

```bash
lore plan add --title="Q2 roadmap" --body="..."
lore tasklist add --title="Triage queue" --body="..." [--json]   # --json: .data.id, for task add --tasklist
lore workflow add --title="release" --body="..."
lore workspace add --title="dev" --body="..."
lore handoff add --for @alice --task tsk_31 "ctx for next session"
```

`plan show --json` eager-loads attached tasks.

---

## Pattern / Playbook / Prompt / ArchitectureNote / Behaviour / CookbookRecipe / Incident / Suggestion / TastePref

Same `add | list | show` shape. Each `list/show` supports `--json`.

```bash
lore pattern   add --title=NoFmt --body="..."
lore playbook  add --title=Release --body="..."
lore prompt    add --title=SystemV2 --body="..."
lore architecturenote add --title="Why SQLite" --body="..."
lore behaviour add --title=careful --body="..."
lore cookbookrecipe add --title="add-resolver" --body="..."
lore incident  add --title="..." --body="..."
lore suggestion add --title="..." --body="..."
lore tastepref add --body="..."
```

---

## Reminder

```bash
lore reminder add "Review FTS quality" --due=2026-06-01 [--recurrence=30d] [--on-table=tasks --on-id=tsk_...]
lore reminder list [--done] [--json]
lore reminder done <id>      # recurring → reschedules; one-shot → marks done
```

Recurrence values: `7d | 30d | 1m | 3m | 6m | 1y` (typed enum; bad values rejected).

---

## Tag + Comment (polymorphic, attaches to any entity)

```bash
# tag
lore tag add --name=urgent [--color=#ff0000]
lore tag list [--json]
lore tag attach --on-table=memories --on-id=mem_<id> --tag=urgent
lore tag detach --on-table=memories --on-id=mem_<id> --tag=urgent

# comment
lore comment add --on-table=decisions --on-id=dec_<id> --body="agreed; revisit Q3"
lore comment list [--on-table=...] [--on-id=...] [--json]
```

Entity tables you can attach to: `memories | rules | decisions | hotfixes | patterns | tasks | missions | playbooks | …` (any entity table name).

---

## Project + Repo

```bash
lore project list [--json]
lore project show <id-or-name> [--json]    # JSON eager-loads repos
lore store list                            # stores this repo sees (replaces project shared-list)

lore repo add <mount> [--origin=git@...] [--display-name=...]
lore repo list [--json]
```

---

## Render (compile knowledge → `.lore/LORE.md` + `@import` pointer)

```bash
lore render                       # writes .lore/LORE.md + stitches @import pointer into CLAUDE.md
lore render --dry-run             # prints the generated .lore/LORE.md body to stdout, doesn't write
lore render --out=.lore/LORE.md   # change the generated knowledge-file path
lore render --target=AGENTS.md    # stitch the pointer into a different agent file
lore render --no-pointer          # write the generated file only; don't touch the agent file
```

Render writes the compiled knowledge to a **generated file** (default `.lore/LORE.md`) and stitches an **idempotent `@import` pointer** into the agent file (default `CLAUDE.md`):

```
<!-- lore:pointer:start -->
@.lore/LORE.md
<!-- if your agent does not support @import, read .lore/LORE.md now -->
<!-- lore:pointer:end -->
```

The pointer is bounded by sentinel markers and replaced in place on every re-run, so your **hand-written CLAUDE.md content is never clobbered**. The canary line and every pinned rule/hotfix live in `.lore/LORE.md`, not CLAUDE.md.

Render is **deterministic** — same DB → byte-identical output. The canary line at the top of the generated file is content-sha-derived, not random.

Symlink-aware: if the generated file is a symlink, render writes through to the target, preserving the link.

After render, the result is queryable via `why-context`.

---

## Why-Context (introspect last render)

```bash
lore why-context [--json] [--rendered]
```

Shows what was included/excluded and why (scope filters, dedup decisions, budget truncation). `--rendered` prints the full rendered text.

---

## Doctor / Rebuild

```bash
# health
lore doctor [--json]              # exit 0 healthy, 1 degraded, 2 broken
lore doctor --fix                 # repair what doctor can fix safely (reversed --supersedes links, a task lead missing from assignees); safe to re-run

# read-only SQL over every table (counts, joins); sensitive values masked
lore sql "SELECT count(*) FROM rules"

# a damaged or lost .lore/lore.db: the files in .lore/data are the truth
mv .lore/lore.db .lore/lore.db.old && lore sync   # rebuilds the database from .lore/data
lore search rebuild                               # rebuilds the search index from the row files
git checkout HEAD -- .lore/data                   # a deleted .lore/data comes back from git

# caches and the binary itself (both list or check first, then ask)
lore cache prune --dry-run                        # cache files of stores no repo here attaches any more
lore upgrade --check                              # is there a newer release? `lore upgrade` installs it
```

---

## Identity

```bash
lore identity show           # who am I, what resolution step won
lore identity set "Alice <alice@example.com>"    # writes identity: in ~/.lore/config.yaml (an email is required)
lore identity unset          # clears it; resolution falls back down the chain
```

8-step fallback chain: flag → env → toml → git config → $USER → machine-id → persisted salt → ephemeral. Always resolves.

---

## Teams: orgs, live rows, stores, agents

Full design: `LORE_TEAM_SPEC.md`. Check where you are first:

```bash
lore whoami                                   # org, why it matched, identity, what loads here
lore config check                             # validate ~/.lore/config.yaml
```

Live rows (shared with everyone at once; tasks, missions, handoffs and comments are live by default; rules, decisions and memories travel with the code, `lore sync promote <id> --to main` sends one to main now):

```bash
lore task add "Rotate the staging keys"            # tasks, missions, handoffs and comments are live
lore land --all --dry-run                     # what would land on main now
lore live show                                # this project's live rows
lore explain tsk_01                           # where an id lives and which copy won
```

Orgs and stores (internal notes never go into a client repo):

```bash
lore store init git@github.com:agency/notes            # once, in an empty repo
lore org link agency --project client                   # tie this client repo to its internal project
lore org use agency                                     # pick the org entry for this project on this machine (not committed)
lore context --internal [--table rules] [--search <text>]   # internal and personal notes for this repo as text (never paste where the client reads)
lore memory add --body "client prefers calls" --to agency
lore memory add --body "my own reminder" --to me
lore move mem_01 --to repo                               # move to the client on purpose (asks)
lore check pr-body body.md                               # before posting a PR body or comment
```

Agents (one actor per session; claims have a lease):

```bash
lore agent start --label agent-backend --owner @alice        # prints LORE_ACTOR / LORE_SESSION to export
lore inbox
lore task next --mission mis_12                          # claims the best ready task (exit 10 = no work)
lore task heartbeat                                      # keep the claim alive
lore hold claim full-suite --ttl 30m                     # one heavy run at a time; lore hold release full-suite after
lore task note tsk_30 "mutation done; tests next"
lore task done tsk_30 --pr 412 --lesson "tokens expire in 48h"
lore memory add "invite links must use the public website address"   # a fact every agent finds with lore search
lore handoff add --for @agent-frontend-2 --task tsk_31 "form is half done"
```

Humans coordinating:

```bash
lore mission apply plan.yaml                     # mission + tasks; a re-run adds nothing
lore task add "invite UI" --after tsk_30:merged
lore gate request tsk_31 && lore gate approve tsk_31
lore mission status mis_12
lore task revoke tsk_31                       # the holder is fenced out
```

## Support Bundle (incident reporting)

```bash
lore support-bundle [--out=path]    # sanitized tarball: DB schema, doctor, last error, scrubbed audit log
```

Strips bodies/comments — only structure + metadata.

---

## Inspectors (read-only)

```bash
lore session     list [--json]    # agent sessions on this machine
lore why-context                  # the last render: what was pinned and why
```

---

## Diagnostics

```bash
lore version [--json]             # binary + schema + bundle + plugin + mcp versions
# error codes: docs/reference/cli-commands.md#errors (generated by lore spec docs); --json on a failing command gives code + hint
lore --help                       # discoverable command tree
lore <cmd> --help                 # per-command help
```

---

## Skill — meta-tools for the bundle itself

The `skill` group manages the SKILL.md bundle (the docs you're reading right now).

### `lore skill compile` — model-compressed bundle (DRAFT GENERATOR — not ship-ready)

> **⚠ Status: draft generator, not ship-ready.** Hand-tuned `SKILL-mini.md` has been better than compiled output so far. Treat compiler output as a starting draft for human review.

Reads every `.md` under `--source-dir` and asks a model to produce a compressed `SKILL-mini.md` (~13-25KB) suitable for small-context (≤32K) or weak-instruction-following models. The compiler's editorial rules are baked into the command (see `skill_compile.go::compressionRules`); update them there, then re-run.

```bash
lore skill compile --target=mini \
    [--source-dir=./skill] \
    [--output=./skill/SKILL-mini.md] \
    [--model=claude-sonnet-4-6] \
    [--budget-bytes=14000] \
    [--temperature=0.2] \
    [--dry-run] \
    [--include='*.md,examples/*.md,playbooks/*.md']
```

- **`--model=` recommendations:** `claude-sonnet-4-6` (default; ~$0.18 / compile) gives best editorial judgment. `claude-haiku-4-5-20251001` is ~5× cheaper but tends to under-compress. `ollama:qwen2.5:32b` is free + local but slower (~3-5 min).
- **`--budget-bytes=`** is a *target* the model aims for; not a hard cap.
- **`--temperature=`** at 0.2 is stable; raise to 0.5+ for more aggressive editorial trimming on retry.
- **`--dry-run`** prints the compiled output to stdout instead of writing the file. Use to A/B before overwriting.

**Non-determinism note:** the compiler is a model call. Same input does NOT produce the same output across runs. Always `git diff` before committing.

**Mandatory workflow — DO NOT ship compiler output without these steps:**

```bash
# 1. Compile to a SCRATCH path, never directly over SKILL-mini.md
lore skill compile --target=mini --output=skill/SKILL-mini-draft.md

# 2. Diff structurally
diff -u skill/SKILL-mini.md skill/SKILL-mini-draft.md | less

# 3. Read the draft against the canonical file: every example and rule it dropped
#    is a regression. Promote with `mv` only after that review; otherwise tweak
#    compressionRules() in the compile command and recompile.
```

**Why the review is non-negotiable:** the model-based compiler partially follows the editorial brief but consistently drops 1-2 critical examples per regen.

JSON envelope:
```json
{
  "schema_version": 1,
  "kind": "skill.compile",
  "data": {
    "target": "mini",
    "output": "./skill/SKILL-mini.md",
    "source_bytes": 148530,
    "output_bytes": 15998,
    "compression_x": 9.28,
    "cost_usd": 0.175,
    "elapsed_ms": 91823,
    "model": "claude-sonnet-4-6"
  }
}
```

## Cheat-sheet: pick a command from intent

```
Intent                                       → Command
─────────────────────────────────────────── → ─────────────────────────────────
"persist a fact"                             → memory add
"persist a hard rule"                        → rule add --severity=must
"persist a decision rationale"               → decision add
"persist a warning we keep hitting"          → hotfix add
"create a tracked work item"                 → task add
"group related work items"                   → mission add
"reusable code shape"                        → pattern add
"reusable procedure"                         → playbook add
"reusable system prompt"                     → prompt add
"future-self reminder"                       → reminder add
"label arbitrary entity"                     → tag attach
"discuss arbitrary entity"                   → comment add
"refresh the agent context file"             → render
"why does the agent see X?"                  → why-context
"is my project healthy?"                     → doctor
"my cache is broken"                         → search rebuild (the cache rebuilds from .lore/data)
```

## Sync (git-tracked `.lore/data/`)

Every shared row is mirrored to `.lore/data/<table>/<id>.json`; commit those files. A pass runs automatically before and after every command, so these are for inspection and repair. Design: `LORE_SYNC_SPEC.md`.

```bash
lore sync                          # run a pass now; non-zero exit when a file could not be synced
lore sync --json                   # {pass, imported[], exported[], removed_files[], deleted_rows[], conflicts[], errors[], warnings[], pending_exports}
lore sync status --json            # {baselined, data_dir_present, tracked_files, pending_exports, open_conflicts, trash_rows, errors[], uncommitted[]}
lore sync conflicts [--all] [--json]
lore sync resolve <n> --take kept|other
lore trash list --local [--json]   # rows sync removed because their file disappeared (this machine)
lore trash restore <n> --local     # core rows refuse and name the `git checkout` that brings the file back
lore sync peek <git-ref> [table] [--json]          # rows on another branch, nothing imported
lore sync promote <id>... --to <branch> [--dry-run]  # new local commit on <branch>; never pushes; not the current branch
lore sync merge-rows <table> --keep <id> --drop <id>   # fold a duplicate: references move to --keep, --drop goes to the trash
lore trash purge --archived-before <date|90d|720h> (--dry-run | --confirm)
lore sync fix-projects [--keep <prj_id>] [--dry-run]
lore sync ci-merge --base <ref> [--dry-run] [--json]
                                   # merge <ref> in with lore's driver and commit, ONLY when the host would see a
                                   # conflict, every conflict is in .lore/data or .lore/LORE.md, and lore settles all
                                   # of them; never pushes. --json: {outcome: up-to-date|clean|merged|needs-human,
                                   # base, commit, merged[], blocking[], reason}; exit 0 for every outcome
lore sync install-action [--auto] [--branch <b>]... [--manual] [--lore-version vX.Y.Z|latest] [--dry-run] [--force] [--json]
                                   # opt-in: writes .github/workflows/lore-sync-merge.yml (runs ci-merge for open
                                   # pull requests and pushes the merge); commit it to enable. Runs only when
                                   # started by hand; --auto adds pull_request + push triggers (spends Actions
                                   # time). A re-run without --auto/--manual keeps the installed triggers;
                                   # --manual (the default, kept for compatibility) switches back to by-hand.
                                   # --json adds manual (bool) and auto (bool). Without
                                   # --lore-version the pin follows lore upgrades: a newer lore release rewrites
                                   # it on its next command ("commit it"); --lore-version freezes it
# to remove the workflow: delete .github/workflows/lore-sync-merge.yml and commit
# merge driver, .gitattributes, hook blocks and .gitignore exceptions are wired after every command (no command)
```

Environment: `LORE_SYNC=0` (off), `LORE_SYNC_GIT=0` (no git wiring), `LORE_SYNC_QUIET=1`, `LORE_SYNC_ALLOW_SECRETS=1` (ignored since 0.1.18, warns).

Error kinds in `errors[]`: `invalid-file`, `conflict-markers`, `newer-format` (upgrade lore), `import-failed`, `secret-detected` (row kept local, not written to git).

## Audit

Every change to a shared row is appended to a hash-chained log by the sync passes: `<table>.write` (this lore command), `<table>.import` (from .lore/data), `<table>.merge`, `<table>.external-write` (found in lore.db, made by something other than lore).

```bash
lore audit verify [--json]          # exit 1 + E_AUDIT_CHAIN_BROKEN on an edited/deleted entry, or "hash mismatch" for rows changed outside lore
lore audit log [--limit 50] [--json]  # newest first; --limit 0 = all
```

`audit verify` and `audit log` open the DB without a sync pass, so run `verify` first when investigating — any other lore command records outside changes as external writes.

## Safety refusals

| Code | When | Override |
|---|---|---|
| `E_ROOT_REFUSED` | running as root (files would end up root-owned) | `LORE_ALLOW_ROOT=1` (legacy `MINI_ALLOW_ROOT=1`) |
| `E_NETWORK_FS` | project or DB inside iCloud/Dropbox/OneDrive/Google Drive, or on NFS/SMB | `LORE_ALLOW_NETWORK_FS=1` |
| `E_DISK_FULL` | the disk is full; nothing was written | free space, re-run |
| `E_MIGRATION_INCOMPLETE` (doctor) | a schema update was interrupted | `lore setup` (safe to repeat) |

