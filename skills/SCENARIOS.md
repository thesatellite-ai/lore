# Scenarios — what to do in 40+ concrete situations

Each entry: situation → exact commands. Stop reading at the first match.

---

## A · First contact

### A.1 User opens a project for the first time, no .lore/
```bash
[ -d .lore ] && echo init-skip || echo "ask user: bootstrap?"
# If yes:
lore init --non-interactive
lore learn-from docs        # if any *.md exists
lore render
```

### A.2 User opens a project that has .lore/ but no CLAUDE.md
```bash
lore render     # creates CLAUDE.md from existing DB
```

### A.3 User opens a project with both .lore/ and CLAUDE.md
```bash
# Normal case. Nothing to do at session start except read CLAUDE.md.
# When user teaches you something new, capture → render.
```

### A.4 User says "set this up like my other project"
```bash
lore init --non-interactive
# Copy memories/rules from the other project:
sqlite3 ../other-project/.lore/lore.db ".dump memories rules decisions" \
    | sqlite3 .lore/lore.db
# (NOTE: project_id differs; you may need to UPDATE project_id after dump.)
lore render
```

---

## B · Capturing knowledge

### B.1 User says "remember: we use Tailwind v4"
```bash
lore memory add "We use Tailwind v4 (not v3) for all styling"
lore render
```

### B.2 User says "we never wrap stdlib errors"
```bash
lore rule add --severity=must --activation=always \
    "Never wrap stdlib errors with fmt.Errorf; return them directly."
lore render
```

### B.3 User says "should prefer composition over inheritance"
```bash
lore rule add --severity=should \
    "Prefer composition over inheritance in domain models."
lore render
```

### B.4 User explains a decision: "we picked SQLite over Postgres because…"
```bash
lore decision add \
    --title="Use SQLite (not Postgres)" \
    --body="Rationale: local-first, zero ops, single-writer fits load.

Considered: Postgres (extra service), DuckDB (no transactions).

Revisit when: multi-tenant or >10k writes/sec."
lore render
```

### B.5 User: "I keep forgetting that ent regen wipes resolver/ helpers"
```bash
lore hotfix add --severity=high \
    "ent regen wipes hand-written code in resolver/. Keep helpers in internal/, lace/, or saas/pkg/."
lore render
```

### B.6 User pastes a reusable code snippet: "let's save this pattern"
```bash
lore pattern add --name="options-pattern" --body="$(cat <<'GO'
type Option func(*Config)
func WithTimeout(d time.Duration) Option { return func(c *Config) { c.timeout = d } }
func New(opts ...Option) *Client {
    cfg := Config{timeout: 30 * time.Second}
    for _, opt := range opts { opt(&cfg) }
    return &Client{cfg: cfg}
}
GO
)"
lore render
```

### B.7 User: "let's record the release procedure"
```bash
lore playbook add --name="release" --body="$(cat <<'MD'
1. task test:all
2. Tag: git tag v$(date +%Y.%m.%d)
3. task release
4. Update CHANGELOG.md
5. gh release create $TAG --notes-file CHANGELOG.md
MD
)"
lore render
```

### B.8 User: "save this prompt as our default system prompt"
```bash
lore prompt add --name="system-v2" --body="$PROMPT_TEXT"
lore render
```

### B.9 User: "we had an outage yesterday — record what happened"
```bash
lore incident add \
    --title="2026-05-10 Redis OOM cascade" \
    --body="Cause: missing TTL on session keys → 6 GB → OOM kill → connection storm → API 503s for 14m.

Fix: set TTL=7d on session:* prefix. Add memory alert at 80%.

Owner: @bob. Status: closed."
lore render
```

### B.10 User: "suggestion: we should add rate limiting to /api"
```bash
lore suggestion add \
    --title="Rate-limit /api/* endpoints" \
    --body="Public endpoints currently uncapped. Suggest: 100req/min per IP, 1000req/min per API key. Use lace/ratelimit pkg."
lore render
```

---

## C · Tracking work

### C.1 User: "add a task to wire FTS5"
```bash
lore task add "Wire FTS5 backend" --priority=high
lore render
```

### C.2 User: "track the v0.2 sprint"
```bash
M=$(lore mission add "Ship v0.2" --target=2026-06-15 --json | jq -r '.data.id')
# Then add tasks: lore task add "..." --mission=$M
```

### C.3 User: "high-priority urgent task: fix login bug, due tomorrow"
```bash
lore task add "Fix login redirect bug" --priority=urgent --due=$(date -v+1d +%Y-%m-%d)
```

### C.4 User: "what's on my plate?"
```bash
lore task list --status=in_progress --json
# and / or
lore task list --status=todo --json | jq -r '.data | sort_by(.priority) | .[0:5][] | "T-\(.id) [\(.priority)] \(.title)"'
```

### C.5 User: "I'm starting tsk_<id>"
```bash
lore task start tsk_<id>
```

### C.6 User: "done with tsk_<id>"
```bash
lore task done tsk_<id>
```

### C.7 User: "cancel tsk_<id>"
```bash
lore task cancel tsk_<id>
```

### C.8 User: "remind me in 2 weeks to revisit tsk_<id>"
```bash
TID=$(lore task list --json | jq -r '.data[] | select(.id==3) | .id')
lore reminder add "Revisit task tsk_<id>" \
    --due=$(date -v+2w +%Y-%m-%d) \
    --on-table=tasks --on-id="$TID"
```

### C.9 User: "weekly review reminder"
```bash
lore reminder add "Weekly review" --due=$(date -v+sun +%Y-%m-%d) --recurrence=7d
```

### C.10 User: "show me all my reminders"
```bash
lore reminder list --json
```

### C.11 User: "mark the recurring reminder done"
```bash
lore reminder done rmd_019e...    # auto-reschedules to next occurrence
```

---

## D · Searching + retrieval

### D.1 "What do we know about redis?"
```bash
lore memory search "redis" --json
```

### D.2 "Anything about auth that ISN'T deprecated?"
```bash
lore memory search "auth NOT deprecated" --json
```

### D.3 "Find all decisions about databases"
```bash
lore decision list --json | jq '.data[] | select(.title | test("(database|db|sqlite|postgres)";"i"))'
```

### D.4 "Show me hotfix hfx_<id>"
```bash
lore hotfix show hfx_019e... --json
```

### D.5 "What rules apply to *.go files?"
```bash
lore rule list --json | jq '.data[] | select(.activation == "glob" and (.globs // [] | tostring | test("go")))'
```

### D.6 "Why is X showing in CLAUDE.md?"
```bash
lore why-context --last-render --rendered | grep -B2 -A2 "X"
```

### D.7 "What did the AI receive in its last render?"
```bash
lore why-context --last-render --json | jq '.scope_summary'
```

---

## E · Render

### E.1 Generic "refresh"
```bash
lore render
```

### E.2 "Preview without writing"
```bash
lore render --dry-run | less
```

### E.3 "Render for a specific repo"
```bash
lore render --repo=web --target=web/CLAUDE.md
```

### E.4 "Also write AGENTS.md for Codex"
```bash
lore render --target=AGENTS.md
# (Currently: render to one target at a time; multi-target is v0.2.)
```

### E.5 CLAUDE.md is a symlink to a shared file
```bash
# Render handles this automatically — writes through the link, preserves it.
lore render
```

---

## F · Health + recovery

### F.1 "Is everything OK?"
```bash
lore doctor
# exit 0 healthy / 1 degraded / 2 broken
```

### F.2 "I see a doctor warning"
```bash
lore doctor --json | jq '.warnings, .errors'
```

### F.3 "DB is corrupt"
```bash
lore repair --tier=2 --confirm
```

### F.4 "I have no backup"
```bash
# Tier 3 — bootstrap empty + re-learn from committed CLAUDE.md
lore repair --tier=3 --confirm
lore learn-from docs
for id in $(lore learn list --json | jq -r '.data[].id'); do
    lore learn promote "$id" --target=memories
done
lore render
```

### F.5 "Create a backup right now"
```bash
lore backup
ls -la .lore/backups/ | tail
```

### F.6 "Restore from a specific backup"
```bash
lore restore .lore/backups/20260510-180412.sqlite --confirm
```

### F.7 "DB is locked, another process is running"
```bash
# Either wait, or find + kill the other process:
lsof .lore/lore.db   # see PID
# If stale (process is dead), lore auto-reclaims on next attempt.
```

### F.8 "Open a bug — capture state"
```bash
lore support-bundle --out=/tmp/lore-bug.tar.gz
# Attach to issue. No secrets — bodies are scrubbed.
```

---

## G · Scope + multi-repo

### G.1 "Add a memory only for the web repo"
```bash
lore memory add "Tailwind v4 only" --repo=web
```

### G.2 "Add a project-wide memory"
```bash
lore memory add "Client charges USD"    # no --repo
```

### G.3 "Search in current repo only"
```bash
lore memory search "tailwind" --repo=web --no-inherit
```

### G.4 "Search across all repos"
```bash
lore memory search "tailwind" --all-repos
```

### G.5 "Register a new repo"
```bash
lore repo add admin --origin=git@github.com:org/admin.git --display-name="Admin Panel"
```

### G.6 "List repos in this project"
```bash
lore repo list --json
```

---

## H · Mode B (shared DB)

### H.1 "Use my home shared DB for this repo"
```bash
lore project shared-init --db=${HOME}/.lore/shared.db --name=myproject
```

### H.2 "Show all projects in the shared DB"
```bash
lore project shared-list --db=${HOME}/.lore/shared.db --json
```

### H.3 "Switch this repo from Mode A to Mode B"
```bash
# Export Mode A rows:
sqlite3 .lore/lore.db ".dump memories rules decisions hotfixes" > /tmp/dump.sql
# Move aside Mode A db:
mv .lore/lore.db .lore/lore.db.modeA-backup
# Init Mode B:
lore project shared-init --db=${HOME}/.lore/shared.db --name=$(basename $PWD)
# Apply dump (manual project_id remap may be needed):
sqlite3 ${HOME}/.lore/shared.db < /tmp/dump.sql
lore render
```

---

## I · Tags + comments

### I.1 "Create urgent tag"
```bash
lore tag add --name=urgent --color="#ff0033"
```

### I.2 "Tag this memory as draft"
```bash
lore tag attach --on-table=memories --on-id=mem_019e... --tag=draft
```

### I.3 "Comment on dec_<id> with a status update"
```bash
DID=$(lore decision list --json | jq -r '.data[] | select(.id==3) | .id')
lore comment add --on-table=decisions --on-id=$DID "Reviewed 2026-08-01; still holds."
```

### I.4 "Show all comments on dec_<id>"
```bash
lore comment list --on-table=decisions --on-id=$DID --json
```

---

## J · Identity

### J.1 "Who am I to lore?"
```bash
lore identity show
```

### J.2 "Set explicit identity"
```bash
lore identity set --kind=human --display="Alice <alice@acme.com>"
```

### J.3 "I'm running in CI; what identity?"
```bash
# CI usually has no git config + no $USER. lore falls through to
# machine-id → persisted salt → ephemeral. Writes succeed; actor is
# stable across runs on the same machine.
lore identity show
```

---

## K · CI / read-only

### K.1 "CI check: CLAUDE.md is up to date"
```bash
LORE_READ_ONLY=1 diff -u CLAUDE.md <(lore render --dry-run) || exit 1
```

### K.2 "CI check: DB is healthy"
```bash
LORE_READ_ONLY=1 lore doctor --json | jq -e '.db_ok'
```

### K.3 "Block CI on stale rules"
```bash
# Track rule count over time; alert on unexpected drop:
N=$(lore rule list --json | jq '.data | length')
[ "$N" -ge "$EXPECTED_MIN_RULES" ] || exit 1
```

---

## L · Migration / one-off imports

### L.1 "Import from .cursorrules"
```bash
lore learn-from docs --paths=.cursorrules
lore learn list --json
# Then promote selectively or in bulk.
```

### L.2 "Import from a Notion export"
```bash
# Convert each Notion page to a memory:
find ./notion-export -name "*.md" -exec lore memory add "$(cat {})" \;
lore render
```

### L.3 "Import a curated list of rules from JSON"
```bash
jq -r '.rules[] | [.severity, .body] | @tsv' rules.json | while IFS=$'\t' read sev body; do
    lore rule add --severity=$sev "$body"
done
lore render
```

---

## M · Inspection / debugging

### M.1 "What error codes can occur?"
```bash
lore errors list --json
```

### M.2 "What version + schema?"
```bash
lore version --json
```

### M.3 "Show me the last 10 query logs"
```bash
lore querylog list --json | jq '.data[0:10]'
```

### M.4 "Show render history"
```bash
lore renderhistory list --json
```

### M.5 "Show recent sessions"
```bash
lore session list --json
```

### M.6 "Show background runs (learn, assemble, bench)"
```bash
lore run list --json
```

---

## N · Unusual / edge cases

### N.1 User pastes content with an embedded API key
```bash
# lore refuses by default with E_SECRET_DETECTED.
# Only override if user explicitly says "yes, I know, override":
lore memory add "<...>" --allow-secrets    # logged loudly
```

### N.2 User runs as root
```bash
# Refused with E_ROOT_REFUSED. Only proceed if user explicitly:
MINI_ALLOW_ROOT=1 lore init
```

### N.3 .lore/lore.db is a symlink
```bash
# Refused with E_SYMLINK_DB. Inspect target — if intentional, delete + restore.
ls -la .lore/lore.db
rm .lore/lore.db
lore repair --tier=2 --confirm   # if backup exists
```

### N.4 Two lore processes running concurrently
```bash
# Each holds a process flock during write. Second writer waits or returns
# E_LOCK_HELD if --read-only is off and the lock is held.
# Default: wait + retry up to 5s.
```

### N.5 DB on iCloud / Dropbox / OneDrive
```bash
# lore detects via path string match and returns E_NETWORK_FS.
# Move the DB to a local path:
mv .lore/lore.db /Users/me/local-lore/$(basename $PWD).db
mkdir -p .lore
ln -s /Users/me/local-lore/$(basename $PWD).db .lore/lore.db
# (NOTE: symlink will be refused — instead use --db flag or LORE_DB env.)
echo "LORE_DB=/Users/me/local-lore/$(basename $PWD).db" >> .envrc
```

### N.6 User: "I want to delete a memory"
```bash
# v0.1: no `memory delete` command. Direct SQL (advanced):
sqlite3 .lore/lore.db "DELETE FROM memories WHERE id=7"
# OR set archived_at to soft-delete:
sqlite3 .lore/lore.db "UPDATE memories SET archived_at=datetime('now') WHERE id=7"
lore render    # rerender without archived rows
```

### N.7 User: "edit memory mem_<id>"
```bash
# v0.1: no `memory edit`. Add a new one + archive the old:
sqlite3 .lore/lore.db "UPDATE memories SET archived_at=datetime('now') WHERE id=3"
lore memory add "<corrected text>"
lore render
```

### N.8 User: "rename the project"
```bash
sqlite3 .lore/lore.db "UPDATE projects SET name='newname' WHERE id=(SELECT id FROM projects)"
```

### N.9 User: "I deleted .lore/ by accident"
```bash
# If you have a backup outside .lore/:
mkdir .lore
lore restore /path/to/backup.sqlite --confirm
# If you committed CLAUDE.md to git, you can rebuild from that:
lore init --non-interactive
lore learn-from docs
```

### N.10 User: "audit log is corrupted"
```bash
# v0.2 feature — `lore audit verify` will land then. For now:
sqlite3 .lore/lore.db "SELECT COUNT(*) FROM audit_log"
```

---

## P · Benchmark engine

### P.1 "Set up a benchmark task for rule rul_<id>"
```bash
lore bench eval add --category=rule-trigger \
    --link=rule:rul_<id> \
    --prompt-file=/tmp/prompt.md \
    --grader-kind=programmatic \
    --grader-cmd='! grep -qE "fmt\.Errorf" "$OUTPUT_FILE"'
```

### P.2 "List benchmark tasks"
```bash
lore bench eval list                          # human
lore bench eval list --json | jq '.count'     # script
lore bench eval list --category=rule-trigger  # filter
lore bench eval list --include-archived       # see archived too
```

### P.3 "Show E1-001"
```bash
lore bench eval show E1-001
lore bench eval show E1-001 --json | jq '.data.grader_spec'
```

### P.4 "Edit the grader for E1-001"
```bash
lore bench eval edit E1-001 \
    --grader-cmd='grep -qE "return.*err" "$OUTPUT_FILE"'
```

### P.5 "Clone E1-001 to experiment"
```bash
lore bench eval duplicate E1-001 --as=E1-001-experimental
lore bench eval edit E1-001-experimental --grader-cmd='...'
```

### P.6 "Archive a task"
```bash
lore bench eval archive E1-001        # soft-delete (preserves history)
lore bench eval unarchive E1-001      # restore
lore bench eval delete E1-001 --confirm  # hard-delete (refuses if any
                                            # bench_result rows reference it)
```

### P.7 "Import the YAML test set"
```bash
lore bench eval import --from=bench/tasks/
# 30 tasks imported, classified by category
lore bench eval list --json | jq '.count'   # 30
```

### P.8 "Export task set for git diff"
```bash
lore bench eval export --to=bench/tasks/
git diff bench/tasks/
```

### P.9 (v0.2.3+) "Run the benchmark"
```bash
lore bench run start --model=claude-sonnet-4-6 --runs-per-arm=3
```

### P.10 (v0.2.3+) "Report on the latest run"
```bash
lore bench report summary $(lore bench run list --latest --id-only)
```

---

## O · What you should refuse

### O.1 User: "store this AWS key as a memory"
**Refuse.** Even if they say "trust me" — the secret-scrubber pattern is broad and the false-positive rate is low. Suggest: store in `.env` (gitignored) or a secrets manager. If they really insist, require explicit `--allow-secrets` AND log a warning.

### O.2 User: "run lore as root"
**Refuse by default.** Suggest: chown the directory to the user.

### O.3 User: "delete the audit log"
**Refuse.** Audit log is forensic. Suggest: archive the DB and start fresh if needed.

### O.4 User: "edit .lore/lore.db directly with sqlite3"
**Discourage** for normal operations. Suggest the CLI. **Allow** for documented surgery (rename, delete, archive) with clear caveats — see N.6 / N.7 / N.8 above.

### O.5 User: "skip the migration check"
**Refuse.** Schema mismatch + skip = data corruption. Suggest: upgrade lore OR pin DB to old version.
