# Playbooks

**In short:** step-by-step recipes for common jobs, such as setting up a new project or recovering a broken database. Copy the commands, change the names, run them.

Multi-step recipes for common situations. Each playbook is **safe to run verbatim** — copy + paste, swap names, done.

---

## P1 — Bootstrap a brand-new project

The user said "let's start using lore here" or you detected `.lore/` is missing.

```bash
cd ~/projects/my-app
lore init --non-interactive            # creates .lore/
# Existing CLAUDE.md / AGENTS.md / .cursorrules: no importer in 0.1.18.
# Read them and add each item that is still true with its own command:
lore rule add --severity=must --body="<a hard rule>"
lore decision add --title="<choice>" --body="<the choice and why>"
lore memory add --body="<a fact>"

lore render                            # writes .lore/LORE.md + @import pointer in CLAUDE.md
```

After this, the agent (next session) reads `CLAUDE.md`, which `@import`s `.lore/LORE.md` automatically.

---

## P2 — Capture a correction from the user

User just said "no, we don't wrap stdlib errors here."

```bash
lore rule add \
    --severity=must \
    --activation=always \
    --body="Do not wrap stdlib errors with fmt.Errorf in this repo"
lore render
```

If the rule applies only to specific files:

```bash
lore rule add \
    --severity=must \
    --activation=glob \
    --globs='["**/*.go", "!**/*_test.go"]' \
    --body="Do not wrap stdlib errors with fmt.Errorf in non-test Go files"
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
    --title="ent regen overwrites resolver/" \
    --body="ent regen overwrites resolver/ files — keep helpers in internal/ or pkg/"
lore render
```

Hotfixes are **never truncated** in the rendered `.lore/LORE.md`, even under tight budget.

---

## P5 — Multi-repo project with scoped knowledge

A monorepo with `web`, `admin`, `api` repos sharing one lore project.

```bash
lore init --non-interactive --name=my-platform
lore repo add web   --origin=git@github.com:org/web.git
lore repo add admin --origin=git@github.com:org/admin.git
lore repo add api   --origin=git@github.com:org/api.git

# Scoped memories
lore memory add --body="Tailwind v4"          --repo=web
lore memory add --body="Mantine"              --repo=admin
lore memory add --body="gqlgen + ent"         --repo=api

# Cross-cutting (no --repo)
lore memory add --body="Client charges in USD"
lore memory add --body="All times in UTC"

# Render per repo (each gets only its repo + master)
(cd web   && lore render)   # render runs in the repo it renders
(cd admin && lore render)
(cd api   && lore render)
```

Search defaults to "current repo + master". To search elsewhere:

```bash
lore memory search "" --all-repos     # everything
lore memory search "" --master-only   # cross-cutting only
lore memory search "" --no-inherit    # strict scope (no master fallback)
```

---

## P6 — Notes shared by sibling projects (a store)

Several repos reading and writing one set of notes. (The v0.1.17 shared-database mode is retired; in a repo that still has its pointer, run `lore migrate run`.)

```bash
# In repo A
cd ~/projects/alpha
lore store init shared "file://$HOME/stores/shared"

# In repo B
cd ~/projects/beta
lore store add shared "file://$HOME/stores/shared"
lore rule add --to shared --body "Every change comes with a test"

# Inspect
lore store list --json
```

Notes written `--to shared` show in every repo that attached the store; everything else stays in the repo's own `.lore/data`.

---

## P7 — Disaster recovery: my DB is broken

User: "everything's broken."

```bash
# 1. Diagnose
lore doctor --json | jq '.db_ok, .errors'

# 2. The cache is built from .lore/data: delete it, the next command rebuilds it
rm -f ~/.lore/cache/repo/<repo folder>-*
lore doctor

# 3. If .lore/data itself was deleted, git has it
git checkout HEAD -- .lore/data
```

Machine-local things (the audit log, sessions) live in `~/.lore/state/machine.db` and are not touched by a rebuild. Keep `.lore/data` committed and nothing shared can be lost.

---

## P8 — Add a task tracked under a mission

```bash
M=$(lore mission add "Ship v0.1" --target=2026-06-30 --json | jq -r '.data.id')
TL=$(lore tasklist add --title="v0.1" --body="release backlog" --json | jq -r '.data.id')   # every task belongs to a tasklist
lore task add "Wire FTS5 backend"      --tasklist=$TL --mission=$M --commitment=accepted --priority=high
lore task add "Write SC scenarios"     --tasklist=$TL --mission=$M --commitment=accepted --priority=high --due=2026-05-15
lore task add "macOS code signing"     --tasklist=$TL --mission=$M --commitment=accepted --priority=medium

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

## P10 — Inspect what the agent is actually seeing

```bash
lore render
lore why-context --json | jq

# Or see the rendered text itself
lore why-context --rendered
```

Useful when the agent is "missing" context — verify the rule/memory is actually included.

---

## P11 — Migrate from `.cursorrules` / `AGENTS.md` to lore

```bash
lore init --non-interactive
# Read .cursorrules, AGENTS.md, CLAUDE.md, docs/conventions.md section by section;
# add each item that is still true (rule add / decision add / memory add / hotfix add)
lore render                            # produces .lore/LORE.md + @import pointer in CLAUDE.md
# Optionally: tell git to ignore the old sources, keep .lore/LORE.md as the canonical surface
```

---

## P12 — CI / read-only mode

In CI you want to fail-fast on missing rules but never mutate the DB:

```yaml
# .github/workflows/check.yml
- run: |
    LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok'
    LORE_READ_ONLY=1 lore render --dry-run > /tmp/expected.md
    diff .lore/LORE.md /tmp/expected.md || {
        echo ".lore/LORE.md out of date — run 'lore render' locally"; exit 1; }
```

Any write attempt under `--read-only` or `LORE_READ_ONLY=1` returns `E_READ_ONLY`.

---

## P13 — Onboard a new teammate

The user wants to bring Alice (an agency developer) up to speed on a project that already uses lore.

```bash
# 1. Make sure the shared knowledge is committed (lore exports it automatically)
lore sync status
git add .lore/data .gitattributes .lore/LORE.md CLAUDE.md && git commit -m "lore knowledge"

# 2. Alice clones the repo
git clone <repo>; cd <repo>

# 3. Any lore command builds her local cache from .lore/data (no init needed;
#    `lore init` would adopt the shared project too)
lore memory list

# 4. Alice opens her coding tool of choice — CLAUDE.md @imports the same .lore/LORE.md
```

Shared knowledge lives in `.lore/data/` — one committed JSON file per row — and travels with the code: it follows branches, merges with PRs, and arrives on `git pull` (the post-merge hook imports it immediately). `~/.lore/cache/repo/<repo folder>-<hash>.db` is a gitignored per-developer cache rebuilt from those files. Alice's captures are exported to `.lore/data/` as she makes them and reach the team when she commits and pushes them.

### P13b — Roll sync out to a team that already uses lore

Each developer has v0.1.17 data (an old `.lore/lore.db`, or `_v: 1` row files).

```bash
# 1. One person, on main, after upgrading lore:
lore migrate run --dry-run            # what moves; then:
lore migrate run                      # converts the v0.1.17 data (keeps a local backup ref; lore migrate rollback undoes it)
git add .lore/data .gitattributes && git commit -m "lore: shared knowledge" && git push

# 2. Everyone else, after upgrading:
git pull
lore memory list                      # adopts .lore/data: switches to the shared project id,
                                      # imports teammates' rows, exports their private rows
lore sync conflicts                   # rows both sides changed: the newer won, the other is here
git add .lore/data && git commit -m "lore: my knowledge"   # on their branch, merged via PR
```

If two people bootstrapped in parallel, run `lore sync fix-projects` on main after the merge.

Things to check during the rollout (all found on a real repo):

- **A teammate on an old lore** (without `lore sync hook`) can still commit: the hook prints "upgrade it" and skips the check. Ask everyone to upgrade anyway, or nothing checks their lore files.
- **A broad `.gitignore` rule** (`_*`, `*.json`, `snapshots/`) would hide lore files; lore adds `!.lore/data/` exceptions and says so — commit the `.gitignore` change with the data. If the whole `.lore/` folder is ignored, lore leaves `.gitignore` alone and warns; change the rule yourself.
- **Committed hooks** (`core.hooksPath .githooks`): lore chains its lines into those scripts and adds `post-checkout`, `post-merge` and `post-rewrite`. Commit them so every clone gets them.

### P13c — A pull request shows a conflict in `.lore/data`

GitHub's merge button never runs lore's merge driver, so a row edited on the branch and on `main` shows as a conflict there even when lore would merge it.

```bash
# On the PR branch:
lore sync ci-merge --base origin/main --dry-run   # can lore settle it unattended? (outcome + files)
git pull origin main                              # lore's driver merges the rows here
lore sync status                                  # any row with the same text changed on both sides?
lore memory show <id>                             # shows both versions inside the value
lore memory edit <id> --body "<what the user chose>"
git add .lore/data && git commit && git push      # the PR merges normally now
```

To have this happen without anyone: `lore sync install-action --auto`, then commit `.github/workflows/lore-sync-merge.yml`. It is opt-in (it uses Actions time, billed on private repos; without `--auto` it runs only when started by hand from the Actions tab), it only pushes merges where every conflict was lore data and lore settled it, and it lists everything else in the run summary. Add a `LORE_SYNC_TOKEN` secret if required checks must re-run on its merge commits. After a lore upgrade, the next lore command may print `updated …lore-sync-merge.yml to lore vX — commit it`: commit that one-line change with your work.

---

## P14 — Promote a memory to a rule

Started as a soft fact; turns out the team really enforces it.

```bash
# Find the memory
M=$(lore memory search "stdlib errors" --json | jq -r '.data[0].id')

# Capture the same content as a rule
BODY=$(lore memory show $M --json | jq -r '.data.body')
lore rule add --severity=must --body="$BODY"

# Archive the original memory
lore memory archive "$M"

lore render
```

---

## P15 — Demote a rule to a memory

The opposite — turns out it was too strict.

```bash
R=$(lore rule list --json | jq -r '.data[] | select(.body | test("stdlib errors")) | .id')
BODY=$(lore rule show $R --json | jq -r '.data.body')

lore memory add --body="(formerly rule) $BODY"
lore rule archive "$R"
lore render
```

---

## P16 — Periodic review checklist

Run weekly:

```bash
# 1. Health
lore doctor --json | jq '.status'

# 2. Shared knowledge committed?
git status --porcelain -- .lore/data

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

# 6. Render + commit (shared rows live in .lore/data)
lore render
lore sync status
git diff --quiet .lore/LORE.md CLAUDE.md .lore/data || { git add .lore/LORE.md CLAUDE.md .lore/data; git commit -m "weekly: refresh lore knowledge"; }
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
# When did this rule change, and by whom?
lore audit log --limit 0 --json | jq '.data[] | select(.target_table == "rules" and .target_id == "rul_<id>")'

# Is the trail intact, and did anything change the DB behind lore's back?
lore audit verify
```

Every change lore makes on this machine is an entry (action `create`, `update`, `delete`, `purge`, or a `secret.*` action), with the row, the actor and the hashes before and after; the log lives in `~/.lore/state/machine.db`. Entries are hash-chained: editing or deleting one breaks `audit verify` (`E_AUDIT_CHAIN_BROKEN`). Changes that arrive from teammates through git are in `git log -- .lore/data`, not in this log.

---

## P19 — Render only a slice (budget control)

When the rendered knowledge grows large and you want a focused view:

```bash
# Per-repo render (that repo's rows + master)
(cd web && lore render)   # each repo renders its own rows + master

# Note: a plain `lore render` always includes project-master + project rows;
# there is no master-only render flag. Use --repo to narrow to a repo slice.

# A specific feature — use tags to filter (v0.2)
```

---

## P20 — Migrate to a new lore version

```bash
# 1. Commit current state
git status --porcelain -- .lore/data   # empty = everything is in git

# 2. Install the new binary
lore upgrade                 # or brew / the install script

# 3. Nothing else to run: the cache and search index rebuild themselves from
#    .lore/data and the machine store updates when it is opened.
#    Only a repo with v0.1.17 data needs one step:
lore migrate status          # the row format of every place
lore migrate run --dry-run && lore migrate run

# 4. Verify
lore doctor --json | jq '.status, .db_ok'
lore render   # confirm output unchanged
```

A binary below the project's `min_lore_version` (or one that does not know a format feature the project uses) refuses writes with `E_OLD_LORE` (reads still work): run `lore upgrade`. `lore setup` is optional (safe to repeat; `--agent-hooks` installs the coding tools' hooks).
