# Commands Reference

Every command is listed below with: signature, key flags, example, and JSON output sample.

All commands accept the universal flags from SKILL.md (`--db`, `--project`, `--repo`, `--read-only`, `--json`, `--color`).

## Bootstrap

### `lore init [path]`
Initializes a Mode A project at cwd. Creates `.lore/lore.db`, applies schema, registers project, auto-adds to `.gitignore`.

```bash
lore init --non-interactive --name=my-app
# ✓ lore initialized at /path/to/my-app
#     project_id: prj_019e...
#     name:       my-app
#     identity:   human:alice@example.com (git config)
```

Flags: `--name=<str>`, `--non-interactive`.

### `lore project shared-init`
**Mode B** — register cwd as a project inside a shared SQLite file used by multiple repos.

```bash
lore project shared-init --db=${HOME}/.lore/shared.db --name=alpha
# Writes .lore/lore.toml at cwd pointing at the shared DB.
```

### `lore learn-from docs`
Ingest existing markdown (CLAUDE.md, AGENTS.md, .cursorrules, README) into `learn_candidates` for review.

```bash
lore learn-from docs                       # default sources at project root
lore learn-from docs --paths=foo.md        # explicit file(s)
lore learn-from docs --paths=docs/         # directory — RECURSES for *.md
lore learn list [--status=pending] [--json]
lore learn promote <id> --target=memories
lore learn reject <id>                     # single
lore learn reject --ids=a,b,c              # batch by ID list
lore learn reject --all                    # batch — every pending candidate
```

---

## Memory

```bash
# add
lore memory add "Tailwind v4 only" [--repo=web] [--kind=manual] [--allow-secrets]

# search (FTS5 BM25; LIKE fallback)
lore memory search "tailwind" [--limit=10] [--all-repos|--master-only|--no-inherit] [--include-archived] [--json]

# list (newest first)
lore memory list --json
```

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
lore rule add --severity=must --activation=always "no fmt.Println in prod" [--globs='["**/*.go"]']
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
lore hotfix add --severity=high "Beware: ent regen wipes resolver/ helpers"
lore hotfix list [--json]
lore hotfix show <id> [--json]
```

Severity: `info | low | medium | high | critical`. Hotfixes are **never truncated** under render budget pressure.

---

## Mission + Task (project management)

```bash
# mission = container for tasks
lore mission add "Ship v0.1" --target=2026-06-30 --body="..."
lore mission list [--status=active|paused|done|cancelled] [--json]
lore mission show <id|MS-N> [--json]
lore mission done <id|MS-N>

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
lore task list [--status=…] [--commitment=…] [--mission=<id>] \
               [--include-proposed] [--include-someday] [--include-deferred] [--all] [--json]
                                   # default = ActiveTask (accepted, not done/cancelled, not future-deferred)
lore task triage                   # commitment=proposed (AI-suggested, not committed)
lore task someday                  # commitment=someday (parking lot)
lore task deferred                 # snoozed: deferred_until in the future
lore task show <id|T-N> [--json]
lore task start <id|T-N>           # auto-promotes commitment=accepted, clears defer
lore task done <id|T-N>            # auto-promotes commitment=accepted, clears defer
lore task cancel <id|T-N>
lore task search <query> [--all]   # default surfaces only ActiveTask hits

# task edit — reparent, reassign, reprioritize, commit/snooze in one place
lore task edit <id|T-N> \
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
- `--repo` — scope to a repo (memory, rule, decision, hotfix, pattern, task)
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
lore snapshot add --title="<t>" "<body>"     # body via arg or stdin
lore snapshot list [--json]
lore snapshot show <id> [--json]
lore snapshot archive / unarchive <id>

# plugin — trust allowlist
lore plugin list [--json]
lore plugin trust --name=<n> --sha256=<sha>
lore plugin untrust <id>

# pii-pattern — custom secret detectors (disabled by default)
lore pii-pattern list [--json]
lore pii-pattern add --name=<n> --regex=<re> [--source=user]
lore pii-pattern enable / disable <id>

# task-view — saved task-filter presets
lore task-view add --name=<n> [--filter=<json>]
lore task-view list [--json]
lore task-view delete <id>

# external-source — registrations consumed by learn-from
lore external-source list [--json]
lore external-source add --name=<n> --kind=<k> [--config=<json>]
lore external-source enable / disable <id>

# techdoc — external documentation refs
lore techdoc add --name=<n> [--base-url=<u>] [--description=<d>]
lore techdoc list [--json]
lore techdoc show <id> [--json]

# mount-alias — repo-rename redirects (read-only)
lore mount-alias list [--json] [--expiring]

# config — DB-level KV (dbconfig table)
lore config get <key>
lore config set <key> <value>
lore config list [--json]
```

## Search (FTS5 — per-entity, multi-column, BM25-ranked)

Every text-bearing entity exposes a `search` subcommand. Powered by SQLite FTS5 with Porter stemming + per-column BM25 weights + snippet highlighting.

```bash
# Per-entity search
lore task search "<query>"       [--column=title,body] [--limit=20] [--json]
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
lore render                        # hybrid: directive + hotfixes + severity=must rules only
lore render --target=AGENTS.md     # alt output path
lore render --dry-run              # stdout, no write
```

**Hybrid model — what's pinned vs searched:**

| Entity | Pinned in CLAUDE.md | Searched on demand |
|---|---|---|
| Directive | always | — |
| Rules | `severity=must` | `severity=should` / `may` |
| Hotfixes | `severity=critical` + `high` | `severity=medium` / `low` |
| Memories, decisions, patterns, architecturenotes, taste prefs, playbooks | — | always |

Symmetric principle: pin only the **non-skippable subset** (constraints + warnings agents can't afford to miss). Everything else fetched via `lore search` per the directive's pre-response checklist. Keeps CLAUDE.md small even as the project's knowledge grows (rules + hotfixes can accumulate into hundreds without bloating the rendered file).

## Setup / migration (run after upgrade)

After `git pull && task lore:install:all`, run **`lore setup`** once in each project:

```bash
lore setup
```

It runs:
1. `dbent_migrate.Migrate` — ent schema auto-migration (new columns, new entities, new indexes)
2. `fts5.EnsureRegistrySchema` — creates/rebuilds 23 per-entity FTS tables + triggers + backfills
3. Stamps a registry fingerprint so per-command warnings stop

Fresh installs (`lore init`) run setup automatically — no manual step needed.

If you skip setup on an upgraded repo, every command prints `hint: run lore setup` on stderr until you do. Per-command overhead is one SELECT (~1ms); the heavy work only runs inside `setup`.

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

## Run logging (descriptive, agent-driven)

Track every agent attempt at a task — what model, which agent, tokens, cost, transcript. Mini does NOT execute runs; the agent (Claude Code / Cursor / human / hook) records descriptively.

```bash
# Open a run before starting work; returns run-id on stdout
RID=$(lore run start \
    --task=tsk_<id> \
    --model=claude-opus-4-7 \
    --agent=claude-code \
    --goal="wire auth middleware against JWT provider" | tail -1)

# Append steps as the agent works
lore run step $RID --kind=prompt --name="kickoff" --tokens-in=1200 --tokens-out=340 --cost=0.05 --duration-ms=1450
lore run step $RID --kind=tool   --name="bash"   --summary="ran go build" --duration-ms=830
lore run step $RID --kind=verify --name="tests"  --summary="all green" --passed=true

# Long payload via stdin
echo "<full prompt or response>" | lore run step $RID --kind=prompt --name=initial --payload-stdin

# Close
lore run end $RID --outcome=success --summary="shipped"
# Or fail / cancel
lore run end $RID --outcome=failed --error="timeout fetching JWKS"
lore run cancel $RID --reason="scope changed"

# Inspect later
lore run list                  # recent runs
lore run show $RID             # metadata
lore run replay $RID           # full step-by-step transcript
lore run show $RID --json
lore run replay $RID --json    # programmatic
```

**Step kinds:** `prompt`, `tool`, `verify`, `reflect`, `decide`, `error`, `note`.

**Outcomes:** `success`, `partial`, `failed`, `cancelled`.

**Tokens / cost** auto-sum from `run step` calls if not given on `run end`. Override with `--tokens-in / --tokens-out / --cost` on end.

**Retries**: pass `--retry-of=<prior-run-id>` on `run start` to link the chain. Useful for "tsk_<id> failed twice before succeeding".

## Agent directive

The CLI ships a loud, prescriptive directive block you can pin to the top of any agent-loaded markdown so future AI sessions don't inline-write memories/tasks into the file (and don't create sibling `NOTES.md` / `LEARNINGS.md` scratchpads as a workaround):

```bash
lore directive install                                # default: CLAUDE.md at cwd
lore directive install --target=AGENTS.md             # any file
lore directive install --target=CLAUDE.md,AGENTS.md   # repeatable
lore directive show                                   # print block to stdout
lore directive remove                                 # strip the block
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
lore tasklist add --title="Triage queue" --body="..."
lore workflow add --name="release" --body="..."
lore workspace add --name="dev" --body="..."
lore handoff add --to=alice --body="ctx for next session"
```

`plan show --json` eager-loads attached tasks.

---

## Pattern / Playbook / Prompt / ArchitectureNote / Behaviour / CookbookRecipe / Incident / Suggestion / TastePref

Same `add | list | show` shape. Each `list/show` supports `--json`.

```bash
lore pattern   add --name=NoFmt --body="..."
lore playbook  add --name=Release --body="..."
lore prompt    add --name=SystemV2 --body="..."
lore architecturenote add --title="Why SQLite" --body="..."
lore behaviour add --name=careful --body="..."
lore cookbookrecipe add --name="add-resolver" --body="..."
lore incident  add --title="..." --body="..."
lore suggestion add --title="..." --body="..."
lore tastepref add --name="composition" --body="..."
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
lore comment add --on-table=decisions --on-id=dec_<id> "agreed; revisit Q3"
lore comment list [--on-table=...] [--on-id=...] [--json]
```

Entity tables you can attach to: `memories | rules | decisions | hotfixes | patterns | tasks | missions | playbooks | …` (any entity table name).

---

## Project + Repo

```bash
lore project list [--json]
lore project show <id-or-name> [--json]    # JSON eager-loads repos
lore project shared-init --db=<path> --name=<n>
lore project shared-list --db=<path> [--json]

lore repo add <mount> [--origin=git@...] [--display-name=...]
lore repo list [--json]
```

---

## Render (compile knowledge → CLAUDE.md)

```bash
lore render                       # writes CLAUDE.md at cwd
lore render --dry-run             # prints to stdout, doesn't write
lore render --target=AGENTS.md    # write to a different file
```

Render is **deterministic** — same DB → byte-identical output. Canary line at top is content-sha-derived, not random.

Symlink-aware: if CLAUDE.md is a symlink, render writes through to the target, preserving the link.

After render, the result is queryable via `why-context`.

---

## Why-Context (introspect last render)

```bash
lore why-context --last-render [--json] [--rendered]
```

Shows what was included/excluded and why (scope filters, dedup decisions, budget truncation). `--rendered` prints the full rendered text.

---

## Doctor / Repair / Backup / Restore

```bash
# health
lore doctor [--json]              # exit 0 healthy, 1 degraded, 2 broken

# backup / restore round-trip
lore backup [--out=path]          # default: .lore/backups/<ts>.sqlite
lore restore <path> --confirm     # replaces current DB

# disaster recovery (3 tiers)
lore repair --tier=1 --confirm    # SQLite .recover from WAL
lore repair --tier=2 --confirm    # restore latest backup (default)
lore repair --tier=3 --confirm    # bootstrap empty DB (last resort)
```

---

## Identity

```bash
lore identity show           # who am I, what resolution step won
lore identity set --kind=human --display="Alice"
```

8-step fallback chain: flag → env → toml → git config → $USER → machine-id → persisted salt → ephemeral. Always resolves.

---

## Support Bundle (incident reporting)

```bash
lore support-bundle [--out=path]    # sanitized tarball: DB schema, doctor, last error, scrubbed audit log
```

Strips bodies/comments — only structure + metadata.

---

## Inspectors (read-only)

```bash
lore run         list [--json]    # background runs (learn, assemble, bench, …)
lore session     list [--json]    # CLI sessions
lore querylog    list [--json]    # search queries history
lore renderhistory list [--json]  # past renders w/ canary IDs
```

---

## Diagnostics

```bash
lore version [--json]             # binary + schema + bundle + plugin + mcp versions
lore errors list [--json]         # full E_* code registry with hints + doc URLs
lore --help                       # discoverable command tree
lore <cmd> --help                 # per-command help
```

---

## Bench — evaluation engine

DB-backed benchmark framework for measuring AI agent performance with vs
without lore context. Three nouns: `eval` (task templates), `run` (one
benchmark execution), `result` (one task × arm × attempt). Plus
`report`/`grader`/`config` for analysis.

See `BENCH_DESIGN.md` and `EVAL_PLAN.md` at repo root for full methodology.

### `lore bench eval` — task definitions (templates)

```bash
# Author a task
lore bench eval add \
    --category=rule-trigger \
    --link=rule:rul_<id> \
    --prompt-file=task.md \
    --grader-kind=programmatic \
    --grader-cmd='! grep -qE "fmt\.Errorf" "$OUTPUT_FILE"' \
    --expected-with=0.90 --expected-baseline=0.30 \
    --notes="..."

# Categories (typed enum):
#   rule-trigger | hotfix-avoid | decision-respect | convention |
#   capture-back | custom
#
# Linkage (--link=kind:id): rule:rul_<id> | hotfix:hfx_<id> | decision:dec_<id> |
# memory:mem_<id>. Auto-snapshots the body at author-time.
#
# Grader kinds:
#   programmatic → --grader-cmd='shell command; exit 0 = PASS'
#   llm-judge    → --grader-rubric="..." [--grader-judge=claude-opus-4-7]
#   golden-diff  → --grader-spec='{"golden_file":"…","threshold":0.85}'
#   composite    → --grader-spec='{"checks":[…],"policy":"all-must-pass"}'

# Inspect
lore bench eval list [--category=… --linked-kind=… --include-archived] [--json]
lore bench eval show E1-001 [--json]   # accepts code OR opaque id

# Lifecycle
lore bench eval edit E1-001 --grader-cmd='…'   # update any field subset
lore bench eval archive E1-001                 # soft-delete (preserves history)
lore bench eval unarchive E1-001
lore bench eval delete E1-001 --confirm        # hard delete (refuses if any
                                                  # bench_result rows reference it)
lore bench eval duplicate E1-001 --as=E1-001b  # clone for editing

# Bulk
lore bench eval import --from=bench/tasks/     # one-shot YAML → DB migration
lore bench eval export --to=bench/tasks/       # DB → YAML for git diff'ing
```

JSON envelope shape:
```json
{
  "schema_version": 1,
  "kind": "bench.eval.show",
  "data": {
    "id": "bve_019e...",
    "code": "E1-001",
    "category": "rule-trigger",
    "linked_kind": "rule",
    "linked_id": "rul_019e...",
    "linked_body_snapshot": "Never wrap stdlib errors with fmt.Errorf",
    "prompt": "Write a Go function that ...",
    "grader_kind": "programmatic",
    "grader_spec": {"cmd": "..."},
    "expected_pass_with": 0.90,
    "expected_pass_baseline": 0.30,
    "archived_at": null,
    "created_at": "2026-05-11T..."
  }
}
```

### `lore bench run` — execute

```bash
lore bench run start --model=<model> [--runs-per-arm=N] [--parallel=N] \
    [--code=<human-code>] [--claude-md=<path>] \
    [--eval-set=all|<category>|<code,code,...>] \
    [--arms=baseline,with_skill] \
    [--budget-cap=<usd>] [--temperature=<f>]

# Models (router picks provider per call by prefix):
#   claude-haiku-4-5-20251001    → Anthropic API ($ANTHROPIC_API_KEY) or
#   claude-sonnet-4-6              local `claude` CLI on PATH
#   claude-opus-4-7
#   ollama:qwen3-coder:latest    → local Ollama (set $OLLAMA_HOST to override
#   ollama:qwen2.5:32b             default http://localhost:11434);
#   ollama:qwen2.5-coder:1.5b      cost = $0 so budget-cap doesn't trip
#
# --parallel=N (default 8): concurrent LLM calls in flight. Tune to your
# provider's rate limit (4-8 for Anthropic tier 1, 16-32 for tier 4,
# 8-20 for Ollama which serializes internally per model).

lore bench run list [--model=… --since=7d] [--json]
lore bench run show <run-id-or-code> [--json]
lore bench run cancel <run-id>
lore bench run retry <run-id> --only-failed
```

### `lore bench result` — individual outcomes

```bash
lore bench result list  --run=<id> [--eval=E1-008] [--arm=with_skill]
                           [--grade=pass|fail|error|skipped] [--json]
lore bench result show  <result-id> [--json]
lore bench result stats [--run=<id> | --latest] [--json]
    # Arm × grade tally for one run. Works MID-RUN — read whatever is
    # persisted so far. Default with no args = latest run.
lore bench result compare <id-a> <id-b>   # side-by-side diff
lore bench result regrade <id>            # re-run grader on stored output,
                                              # NO LLM call required
lore bench result replay  <id>            # re-run the LLM call entirely
```

### `lore bench report` — analysis

```bash
lore bench report summary [<run-id-or-code> | --latest] [--json|--md]
    # No-arg form = newest run for current project. Use --latest for the
    # same explicitly, e.g. in scripts.
lore bench report compare <run-a> <run-b> [--json]
lore bench report trend --since=30d [--by-model] [--json]
lore bench report by-category <run-id> [--json]
lore bench report regressions --since=last-week
lore bench report analyze <run-id> [--json]   # paired t-test, Cohen's d,
                                                  # 95% CI, power analysis
```

### `lore bench grader` — meta-tools (coming next)

```bash
lore bench grader test E1-001 --output-file=/tmp/sample.txt
lore bench grader debug <result-id>
lore bench grader audit                  # flags too-strict/too-loose/flaky
```

### Status of phases

| Phase | Surface | Status |
|---|---|---|
| P2.1 | `bench_eval/run/result` schemas with full design + ent regen | ✅ shipped |
| P2.2 | `bench eval add/list/show/edit/archive/unarchive/delete/duplicate/import/export` | ✅ shipped |
| P2.3 | `bench run start/list/show/tail/cancel/retry` | next |
| P2.4 | Grader runner refactor (Python → Go in-process) | |
| P2.5 | `bench result list/show/compare/regrade/replay` | |
| P2.6 | `bench report summary/compare/trend/by-category/analyze` | |
| P2.7 | Statistical layer (t-test, Cohen's d, 95% CI) | |
| P2.8 | `bench grader test/debug/audit` | |
| P2.9 | Integration scenarios (SC-31..40 covering bench surface) | |
| P2.10 | Skill `bench.md` + shell-completion polish | |

---

## Skill — meta-tools for the bundle itself

The `skill` group manages the SKILL.md bundle (the docs you're reading right now).

### `lore skill compile` — LLM-compressed bundle (DRAFT GENERATOR — not ship-ready)

> **⚠ Status: draft generator, not ship-ready.** Head-to-head benching showed the auto-compiled output scores ~70-75% on the 30-eval suite vs the hand-tuned canonical `SKILL-mini.md` at 100%. Treat compiler output as a starting draft for human review; never ship un-bench-verified.

Reads every `.md` under `--source-dir` and asks an LLM to produce a compressed `SKILL-mini.md` (~13-25KB) suitable for small-context (≤32K) or weak-instruction-following models. The compiler's editorial rules are baked into the command (see `skill_compile.go::compressionRules`); update them there, then re-run.

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
- **`--budget-bytes=`** is a *target* the LLM aims for; not a hard cap.
- **`--temperature=`** at 0.2 is stable; raise to 0.5+ for more aggressive editorial trimming on retry.
- **`--dry-run`** prints the compiled output to stdout instead of writing the file. Use to A/B before overwriting.

**Non-determinism note:** the compiler is an LLM call. Same input does NOT produce the same output across runs. Always `git diff` before committing. Bench-verify the new bundle against your eval set before promoting it.

**Mandatory workflow — DO NOT ship compiler output without these steps:**

```bash
# 1. Compile to a SCRATCH path, never directly over SKILL-mini.md
lore skill compile --target=mini --output=skill/SKILL-mini-draft.md

# 2. Diff structurally
diff -u skill/SKILL-mini.md skill/SKILL-mini-draft.md | less

# 3. Bench-verify against the FULL eval set (not just smoke)
cd ../ai_coder_mini_skill_bench
ln -sf ../ai_coder_mini_go/skill/SKILL-mini-draft.md bench-CLAUDE.md
lore bench run start --model=ollama:qwen3-coder:latest \
    --eval-set=all --parallel=12 \
    --code=mini-draft-verify-$(date +%Y%m%d)
lore bench report summary

# 4. Compare to the canonical baseline
ln -sf ../ai_coder_mini_go/skill/SKILL-mini.md bench-CLAUDE.md
lore bench run start --model=ollama:qwen3-coder:latest \
    --eval-set=all --parallel=12 \
    --code=mini-canonical-$(date +%Y%m%d)
lore bench report compare mini-draft-verify-$(date +%Y%m%d) mini-canonical-$(date +%Y%m%d)

# 5. If draft >= canonical → promote with `mv` and `git diff` review.
#    If draft <  canonical → tweak compressionRules() in
#    saas/cmd/cli/skill_compile.go, recompile, re-bench.
#    NEVER promote a draft that scored below canonical.
```

**Why the bench-verify is non-negotiable:** the LLM compiler partially follows the editorial brief but consistently drops 1-2 critical examples per regen. Without an A/B vs canonical, those regressions ship silently.

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
"refresh the AI context file"                → render
"why does the AI see X?"                     → why-context --last-render
"is my project healthy?"                     → doctor
"my DB is broken"                            → repair --tier=2 --confirm
"what error codes exist?"                    → errors list --json
"author a benchmark task"                    → bench eval add
"list / show benchmark tasks"                → bench eval list / show
"migrate YAML test set to DB"                → bench eval import --from=…
"run the benchmark"                          → bench run start (v0.2.3+)
"see benchmark results"                      → bench report summary (v0.2.3+)
```
