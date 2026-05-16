# Playbooks

Multi-step recipes for common situations. Each playbook is **safe to run verbatim** — copy + paste, swap names, done.

---

## P1 — Bootstrap a brand-new project

The user said "let's start using lore here" or you detected `.lore/` is missing.

```bash
cd ~/projects/my-app
lore init --non-interactive            # creates .lore/lore.db
lore learn-from docs                   # ingests existing CLAUDE.md/AGENTS.md/.cursorrules
lore learn list --json                 # review what was extracted

# Accept the good ones
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done

lore render                            # writes CLAUDE.md
```

After this, the AI (next session) reads `CLAUDE.md` automatically.

---

## P2 — Capture a correction from the user

User just said "no, we don't wrap stdlib errors here."

```bash
lore rule add \
    --severity=must \
    --activation=always \
    "Do not wrap stdlib errors with fmt.Errorf in this repo"
lore render
```

If the rule applies only to specific files:

```bash
lore rule add \
    --severity=must \
    --activation=glob \
    --globs='["**/*.go", "!**/*_test.go"]' \
    "Do not wrap stdlib errors with fmt.Errorf in non-test Go files"
```

---

## P3 — Capture a non-obvious decision

User said "we picked SQLite over Postgres because we want local-first and zero ops."

```bash
lore decision add \
    --title="Use SQLite (not Postgres)" \
    --body="Local-first design. Zero ops. Acceptable since single-writer + WAL handles all expected load. Revisit if multi-tenant. Decided: 2026-04-30."
lore render
```

---

## P4 — Capture a "we keep hitting this" warning

User: "ugh, ent regen wiped my resolver helpers AGAIN."

```bash
lore hotfix add \
    --severity=high \
    "ent regen overwrites resolver/ files — keep helpers in internal/, lace/, or saas/pkg/"
lore render
```

Hotfixes are **never truncated** in the rendered CLAUDE.md, even under tight budget.

---

## P5 — Multi-repo project with scoped knowledge

A monorepo with `web`, `admin`, `api` repos sharing one lore project.

```bash
lore init --non-interactive --name=my-platform
lore repo add web   --origin=git@github.com:org/web.git
lore repo add admin --origin=git@github.com:org/admin.git
lore repo add api   --origin=git@github.com:org/api.git

# Scoped memories
lore memory add "Tailwind v4"          --repo=web
lore memory add "Mantine"              --repo=admin
lore memory add "gqlgen + ent"         --repo=api

# Cross-cutting (no --repo)
lore memory add "Client charges in USD"
lore memory add "All times in UTC"

# Render per repo (each gets only its repo + master)
cd web   && lore render --repo=web
cd admin && lore render --repo=admin
cd api   && lore render --repo=api
```

Search defaults to "current repo + master". To search elsewhere:

```bash
lore memory search "" --all-repos     # everything
lore memory search "" --master-only   # cross-cutting only
lore memory search "" --no-inherit    # strict scope (no master fallback)
```

---

## P6 — Shared DB across sibling projects (Mode B)

Multiple unrelated repos sharing one `.db` file under `~/.lore/`.

```bash
# In repo A
cd ~/projects/alpha
lore project shared-init --db=${HOME}/.lore/shared.db --name=alpha

# In repo B
cd ~/projects/beta
lore project shared-init --db=${HOME}/.lore/shared.db --name=beta

# Inspect
lore project shared-list --db=${HOME}/.lore/shared.db --json
```

Each project sees only its own rows (partitioned by `project_id`).

---

## P7 — Disaster recovery: my DB is broken

User: "everything's broken."

```bash
# 1. Diagnose
lore doctor --json | jq '.db_ok, .errors'

# 2. Attempt automatic repair (tries WAL replay first, then latest backup)
lore repair --tier=2 --confirm

# 3. If that fails, last resort: bootstrap empty + re-learn from CLAUDE.md
lore repair --tier=3 --confirm
lore learn-from docs
```

`tier=2` succeeds whenever a backup exists in `.lore/backups/`. Backups are created on-demand via `lore backup`; you should run that before risky operations.

---

## P8 — Add a task tracked under a mission

```bash
M=$(lore mission add "Ship v0.1" --target=2026-06-30 --json | jq -r '.data.id')
lore task add "Wire FTS5 backend"      --mission=$M --priority=high
lore task add "Write SC scenarios"     --mission=$M --priority=high --due=2026-05-15
lore task add "macOS code signing"     --mission=$M --priority=medium

lore mission show $M --json | jq '.data.tasks'      # eager-loaded tasks
```

To start working on a task:

```bash
lore task start tsk_019e...           # status: todo → in_progress
# ... do the work ...
lore task done  tsk_019e...           # status: in_progress → done
```

---

## P9 — Reminder for periodic review

```bash
# Once-off: 2026-06-01
lore reminder add "Audit FTS index quality" --due=2026-06-01

# Recurring: every 30 days
lore reminder add "Weekly review" --due=2026-05-18 --recurrence=7d

# Attached to a specific decision
lore reminder add "Re-evaluate SQLite choice" --due=2026-12-31 \
    --on-table=decisions --on-id=dec_<id>
```

`reminder done <id>` reschedules recurring; marks one-shot complete.

---

## P10 — Inspect what the AI is actually seeing

```bash
lore render
lore why-context --last-render --json | jq

# Or see the rendered text itself
lore why-context --last-render --rendered
```

Useful when the AI is "missing" context — verify the rule/memory is actually included.

---

## P11 — Migrate from `.cursorrules` / `AGENTS.md` to lore

```bash
lore init --non-interactive
lore learn-from docs --paths='.cursorrules,AGENTS.md,CLAUDE.md,docs/conventions.md'
lore learn list                        # review proposals
# Accept all:
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done
lore render                            # produces CLAUDE.md
# Optionally: tell git to ignore the old sources, keep CLAUDE.md as the canonical surface
```

---

## P12 — CI / read-only mode

In CI you want to fail-fast on missing rules but never mutate the DB:

```yaml
# .github/workflows/check.yml
- run: |
    LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok'
    LORE_READ_ONLY=1 lore render --dry-run > /tmp/expected.md
    diff CLAUDE.md /tmp/expected.md || {
        echo "CLAUDE.md out of date — run 'lore render' locally"; exit 1; }
```

Any write attempt under `--read-only` or `LORE_READ_ONLY=1` returns `E_READ_ONLY`.

---

## P13 — Onboard a new teammate

The user wants to bring Alice up to speed on a project that already uses lore.

```bash
# 1. Make sure CLAUDE.md is in git and current
lore render
git add CLAUDE.md && git commit -m "refresh CLAUDE.md"

# 2. Alice clones the repo
git clone <repo>; cd <repo>

# 3. Alice initializes her own local DB (CLAUDE.md is the seed)
lore init --non-interactive
lore learn-from docs --paths=CLAUDE.md
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done

# 4. Alice opens her AI tool of choice — sees the same CLAUDE.md
```

Knowledge lives in CLAUDE.md (committed), DB is per-developer (gitignored). Alice's writes stay local until she re-commits CLAUDE.md.

---

## P14 — Promote a memory to a rule

Started as a soft fact; turns out the team really enforces it.

```bash
# Find the memory
M=$(lore memory search "stdlib errors" --json | jq -r '.results[0].id')

# Capture the same content as a rule
BODY=$(lore memory show $M --json | jq -r '.data.body')
lore rule add --severity=must "$BODY"

# Archive the original memory
sqlite3 .lore/lore.db "UPDATE memories SET archived_at=datetime('now') WHERE id='$M'"

lore render
```

---

## P15 — Demote a rule to a memory

The opposite — turns out it was too strict.

```bash
R=$(lore rule list --json | jq -r '.data[] | select(.body | test("stdlib errors")) | .id')
BODY=$(lore rule show $R --json | jq -r '.data.body')

lore memory add "(formerly rule) $BODY"
sqlite3 .lore/lore.db "UPDATE rules SET archived_at=datetime('now') WHERE id='$R'"
lore render
```

---

## P16 — Periodic review checklist

Run weekly:

```bash
# 1. Health
lore doctor --json | jq '.status'

# 2. Backup
lore backup
ls -la .lore/backups/ | tail -3

# 3. Are there active reminders due this week?
lore reminder list --json | jq --arg until "$(date -v+7d +%Y-%m-%d)" '
    .data[] | select(.due_at <= $until)
'

# 4. Open hotfixes (high-severity warnings still relevant?)
lore hotfix list --json | jq '.data[] | "HF-\(.id) [\(.severity)] \(.body[0:80])"'

# 5. Stale tasks (in_progress > 7 days)
lore task list --status=in_progress --json | jq --arg cutoff "$(date -v-7d +%Y-%m-%dT00:00:00Z)" '
    .data[] | select(.started_at < $cutoff) | "T-\(.id) stale since \(.started_at)"
'

# 6. Render + commit
lore render
git diff --quiet CLAUDE.md || { git add CLAUDE.md; git commit -m "weekly: refresh CLAUDE.md"; }
```

---

## P17 — Export project knowledge to JSON

```bash
mkdir -p export
for kind in memory rule decision hotfix pattern playbook prompt task mission; do
    lore $kind list --json > export/$kind.json
done
tar czf project-knowledge.tar.gz export/
```

Useful for: portability, manual review, feeding into a fine-tune.

---

## P18 — Audit log review (forensics)

```bash
# When did this rule get added, and by whom?
sqlite3 .lore/lore.db <<'SQL'
SELECT a.tx_at, a.actor_id, a.op, a.entity_id, a.row_after
FROM audit_log a
WHERE a.entity_table = 'rules'
  AND a.row_after LIKE '%stdlib%'
ORDER BY a.tx_at DESC
LIMIT 10;
SQL
```

(`audit_log` is internal — there's no CLI front in v0.1. v0.2 will expose `lore audit search`.)

---

## P19 — Render only a slice (budget control)

When CLAUDE.md grows large and you want a focused view:

```bash
# Per-repo render (only that repo's memories + master)
lore render --repo=web --target=web/CLAUDE.md

# Cross-cutting only (no repo memories)
lore render --master-only --target=master.md

# A specific feature — use tags to filter (v0.2)
```

---

## P20 — Migrate to a new lore version

```bash
# 1. Backup current state
lore backup

# 2. Check the new binary's schema vs the DB's schema
lore version --json | jq '.schema_version'
sqlite3 .lore/lore.db "SELECT MAX(version) FROM schema_migrations"

# 3. If new binary expects a newer schema, run migration (auto on next open)
lore doctor   # triggers auto-migrate

# 4. Verify
lore doctor --json | jq '.status, .db_ok'
lore render   # confirm output unchanged
```

If the new binary expects an OLDER schema (downgrade), lore refuses with `E_SCHEMA_VERSION_MISMATCH`. Use the matching binary version OR restore from a backup taken with the old version.
