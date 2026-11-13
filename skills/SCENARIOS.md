# Scenarios — what to do in 40+ concrete situations

**In short:** more than 40 real situations ("I just cloned a repo that uses lore", "two people edited the same task"), each with the exact commands to run.

Each entry: situation → exact commands. Stop reading at the first match.

---

## A · First contact

### A.1 User opens a project for the first time, no .lore/
```bash
[ -d .lore ] && echo init-skip || echo "ask user: bootstrap?"
# If yes:
lore init --non-interactive
# existing *.md notes: add each true item with rule add / decision add / memory add
lore render
```

### A.2 User opens a project that has .lore/ but no CLAUDE.md
```bash
lore render     # writes .lore/LORE.md from existing DB + creates CLAUDE.md with the @import pointer
```

### A.3 User opens a project with both .lore/ and CLAUDE.md
```bash
# Normal case. Nothing to do at session start except read CLAUDE.md.
# When user teaches you something new, capture → render.
```

### A.4 User says "set this up like my other project"
```bash
lore init --non-interactive
# Rules both projects follow belong in a store both repos attach
# (PLAYBOOKS.md P6), not in a copy: in the other repo, once,
#   lore store init shared file:///<absolute path>   (or a git url)
# then here:
lore store add --name shared --url <url>
lore render
```

---

## B · Capturing knowledge

### B.1 User says "remember: we use Tailwind v4"
```bash
lore memory add --body "We use Tailwind v4 (not v3) for all styling"
lore render
```

### B.2 User says "we never wrap stdlib errors"
```bash
lore rule add --severity must --activation always \
    --body "Never wrap stdlib errors with fmt.Errorf; return them directly."
lore render
```

### B.3 User says "should prefer composition over inheritance"
```bash
lore rule add --severity should \
    --body "Prefer composition over inheritance in domain models."
lore render
```

### B.4 User explains a decision: "we picked SQLite over Postgres because…"
```bash
lore decision add \
    --title "Use SQLite (not Postgres)" \
    --body "Rationale: local-first, zero ops, single-writer fits load.

Considered: Postgres (extra service), DuckDB (no transactions).

Revisit when: multi-tenant or >10k writes/sec."
lore render
```

### B.5 User: "I keep forgetting that ent regen wipes resolver/ helpers"
```bash
lore hotfix add --severity high --title "ent regen wipes resolver/ helpers" \
    --body "ent regen wipes hand-written code in resolver/. Keep helpers in internal/ or pkg/."
lore render
```

### B.6 User pastes a reusable code snippet: "let's save this pattern"
```bash
lore pattern add --title "options-pattern" --body "$(cat <<'GO'
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
lore playbook add --title "release" --body "$(cat <<'MD'
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
lore prompt add --title "system-v2" --body "$PROMPT_TEXT"
lore render
```

### B.9 User: "we had an outage yesterday — record what happened"
```bash
lore incident add \
    --title "2026-05-10 Redis OOM cascade" \
    --body "Cause: missing TTL on session keys → 6 GB → OOM kill → connection storm → API 503s for 14m.

Fix: set TTL=7d on session:* prefix. Add memory alert at 80%.

Owner: @bob. Status: closed."
lore render
```

### B.10 User: "suggestion: we should add rate limiting to /api"
```bash
lore suggestion add \
    --title "Rate-limit /api/* endpoints" \
    --body "Public endpoints currently uncapped. Suggest: 100req/min per IP, 1000req/min per API key. Use the shared ratelimit pkg."
lore render
```

---

## C · Tracking work

### C.1 User: "add a task to wire FTS5"
```bash
TL=$(lore tasklist list --json | jq -r '.data[0].id')   # or: lore tasklist add --title … --body … --json
lore task add --title "Wire FTS5 backend" --tasklist $TL --commitment accepted --priority high
lore render
```

### C.2 User: "track the v0.2 sprint"
```bash
M=$(lore mission add --title "Ship v0.2" --target 2026-06-15 --json | jq -r '.data.id')
# Then add tasks: lore task add --title "<title>" --tasklist <tlt_id> --mission $M --commitment accepted
```

### C.3 User: "high-priority urgent task: fix login bug, due tomorrow"
```bash
lore task add --title "Fix login redirect bug" --tasklist <tlt_id> --commitment accepted --priority urgent --due $(date -v+1d +%Y-%m-%d)
```

### C.4 User: "what's on my plate?"
```bash
lore task list --status in_progress --json
# and / or
lore task list --status todo --json | jq -r '.data | sort_by(.priority) | .[0:5][] | "T-\(.id) [\(.priority)] \(.title)"'
```

### C.5 User: "I'm starting tsk_<id>"
```bash
lore task start --id tsk_<id>
```

### C.6 User: "done with tsk_<id>"
```bash
lore task done --id tsk_<id>
```

### C.7 User: "cancel tsk_<id>"
```bash
lore task cancel --id tsk_<id>
```

### C.8 User: "remind me in 2 weeks to revisit tsk_<id>"
```bash
TID=$(lore task list --json | jq -r '.data[] | select(.id==3) | .id')
lore reminder add --message "Revisit task tsk_<id>" \
    --due $(date -v+2w +%Y-%m-%d) \
    --on-table tasks --on-id "$TID"
```

### C.9 User: "weekly review reminder"
```bash
lore reminder add --message "Weekly review" --due $(date -v+sun +%Y-%m-%d) --recurrence 7d
```

### C.10 User: "show me all my reminders"
```bash
lore reminder list --json
```

### C.11 User: "mark the recurring reminder done"
```bash
lore reminder done --id rmd_019e...    # auto-reschedules to next occurrence
```

---

## D · Searching + retrieval

### D.1 "What do we know about redis?"
```bash
lore memory search --query "redis" --json
```

### D.2 "Anything about auth that ISN'T deprecated?"
```bash
lore memory search --query "auth NOT deprecated" --json
```

### D.3 "Find all decisions about databases"
```bash
lore decision list --json | jq '.data[] | select(.title | test("(database|db|sqlite|postgres)";"i"))'
```

### D.4 "Show me hotfix hfx_<id>"
```bash
lore hotfix show --id hfx_019e... --json
```

### D.5 "What rules apply to *.go files?"
```bash
lore rule list --json | jq '.data[] | select(.activation == "glob" and (.globs // [] | tostring | test("go")))'
```

### D.6 "Why is X showing in CLAUDE.md?"
```bash
lore why-context --rendered | grep -B2 -A2 "X"
```

### D.7 "What did the agent receive in its last render?"
```bash
lore why-context --json | jq '.scope_summary'
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
(cd web && lore render)   # render runs in the repo it renders
```

### E.4 "Also stitch the pointer into AGENTS.md for Codex"
```bash
lore render --target AGENTS.md
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
# the cache is built from .lore/data: delete it, the next command rebuilds it
rm -f ~/.lore/cache/repo/<repo folder>-*
lore doctor
```

### F.4 "I deleted .lore/data"
```bash
git checkout HEAD -- .lore/data     # the files are the truth; git has them
```

### F.5 "Make a safe copy of my notes"
```bash
git add .lore/data && git status --short .lore/data   # commit them; no separate backup
```

### F.6 "DB is locked, another process is running"
```bash
# E_STORE_LOCKED: another lore command held the store lock past
# store.lock_timeout. Wait a few seconds and run the command again.
# A lock left by a dead process is taken over on the next attempt.
```

### F.7 "Open a bug — capture state"
```bash
lore support-bundle --out /tmp/lore-bug.tar.gz
# Attach to issue. No secrets — bodies are scrubbed.
```

---

## G · Scope + multi-repo

### G.1 "Add a memory only for the web repo"
```bash
lore memory add --body "Tailwind v4 only" --repo web
```

### G.2 "Add a project-wide memory"
```bash
lore memory add --body "Client charges USD"    # no --repo
```

### G.3 "Search in current repo only"
```bash
lore memory search --query "tailwind" --repo web --no-inherit
```

### G.4 "Search across all repos"
```bash
lore memory search --query "tailwind" --all-repos
```

### G.5 "Register a new repo"
```bash
lore repo add --mount-name admin --origin git@github.com:org/admin.git --display-name "Admin Panel"
```

### G.6 "List repos in this project"
```bash
lore repo list --json
```

---

## H · Notes shared by several repos (a store)

### H.1 "Share notes between these repos"
```bash
lore store init --name shared --url "file://$HOME/stores/shared"   # first repo: make the folder store, attach it
lore store add --name shared --url "file://$HOME/stores/shared"    # each other repo
lore memory add --to shared --body "<...>"
```

### H.2 "Which stores does this repo see?"
```bash
lore store list --json
```

### H.3 "This repo still uses the old shared database (Mode B)"
Every command stops with `E_UNSUPPORTED` and names `lore migrate run` (0.1.18 retired Mode B).
```bash
lore migrate run --dry-run    # lists the rows that move into a folder store beside the shared DB
lore migrate run              # converts; the pointer is kept as .lore/lore.yaml.migrated
```

---

## I · Tags + comments

### I.1 "Create urgent tag"
```bash
lore tag add --name urgent --color "#ff0033"
```

### I.2 "Tag this memory as draft"
```bash
lore tag attach --on-table memories --on-id mem_019e... --tag draft
```

### I.3 "Comment on dec_<id> with a status update"
```bash
DID=$(lore decision list --json | jq -r '.data[] | select(.id==3) | .id')
lore comment add --on-table decisions --on-id $DID --body "Reviewed 2026-08-01; still holds."
```

### I.4 "Show all comments on dec_<id>"
```bash
lore comment list --on-table decisions --on-id $DID --json
```

---

## J · Identity

### J.1 "Who am I to lore?"
```bash
lore identity show
```

### J.2 "Set explicit identity"
```bash
lore identity set --identity "Alice <alice@agency.example>"
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

### K.1 "CI check: .lore/LORE.md is up to date"
```bash
LORE_READ_ONLY=1 diff -u .lore/LORE.md <(lore render --dry-run) || exit 1
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
cat .cursorrules
# no importer in 0.1.18: add each item that is still true with its own command
lore rule add --severity must --body "<rule from the file>"
```

### L.2 "Import from a Notion export"
```bash
# Convert each Notion page to a memory:
find ./notion-export -name "*.md" -exec lore memory add --body "$(cat {})" \;
lore render
```

### L.3 "Import a curated list of rules from JSON"
```bash
jq -r '.rules[] | [.severity, .body] | @tsv' rules.json | while IFS=$'\t' read sev body; do
    lore rule add --severity $sev --body "$body"
done
lore render
```

---

## M · Inspection / debugging

### M.1 "What error codes can occur?"
Read the error table in `docs/reference/errors.md` (generated by `lore spec docs` from the code). A failing command with `--json` prints its own code:
```bash
lore task show --id tsk_missing --json
```

### M.2 "What version + schema?"
```bash
lore version --json
```

### M.3 "What did the last render pin, and why?"
```bash
lore why-context
lore why-context --cut
```

### M.4 "How has LORE.md grown?"
```bash
lore render --trend --json
```

### M.5 "Show recent sessions"
```bash
lore session list --json
```

### M.6 "How long did the agent's runs on a task take?"
```bash
lore task time show --id tsk_<id>
```

---

## N · Unusual / edge cases

### N.1 User pastes content with an embedded API key
```bash
# lore stops with E_NEEDS_CONFIRM (findings[] + next.add_args_one_of); a bare --yes does not store it.
lore memory add --body "<...>" --secret-ref "vault://team/deploy-key"   # keep a reference instead of the value
lore memory add --body "<...>" --secret-unstored                        # keep the note, leave the value out
# Only if the person explicitly confirms it is not a secret:
lore memory add --body "<...>" --not-a-secret --reason "<why>" --yes    # the commit hook still refuses a credential
```

### N.2 User runs as root
```bash
# Refused with E_ROOT_REFUSED. Only proceed if user explicitly:
LORE_ALLOW_ROOT=1 lore init
```

### N.3 A lore file is a symlink
```bash
# Refused with E_SYMLINK_DB: lore never writes a database, the store lock
# (.lore/lock) or an audit file through a symbolic link. The message names
# the file. Inspect the target, then delete the link; the next command recreates it.
lore where | grep -A1 cache/repo   # the repo cache's file
rm <the linked file>
lore doctor
```

### N.4 Two lore processes running concurrently
```bash
# Writers take the store lock in turn; a second writer waits up to
# store.lock_timeout, then answers E_STORE_LOCKED (retry). Readers never wait.
```

### N.5 DB on iCloud / Dropbox / OneDrive
```bash
# lore detects cloud-sync folders by path and network filesystems by fs type,
# and returns E_NETWORK_FS. Fix: move the whole project to a local disk
# and share knowledge through git (.lore/data). --db / LORE_DB are refused
# on row commands, and a symlinked cache is refused (E_SYMLINK_DB).
# LORE_ALLOW_NETWORK_FS=1 overrides at the user's own risk.
```

### N.6 User: "I want to delete a memory"
```bash
lore memory delete --id mem_<id> --reason "<why>"   # to the trash; asks first
lore trash restore --target mem_<id>                    # undo
# or keep it out of the context without deleting:
lore memory archive --id mem_<id>
```

### N.7 User: "edit memory mem_<id>"
```bash
lore memory edit --id mem_<id> --body "<corrected text>"
# or record that the old one is wrong and add the replacement:
lore memory add --body "<corrected text>" --supersedes mem_<id>
```

### N.8 User: "rename the project"
```bash
# 0.1.18 has no project rename command; tell the user. Never edit the
# cache (~/.lore/cache/repo/<repo folder>-<hash>.db): it is rebuilt from .lore/data.
```

### N.9 User: "I deleted .lore/ by accident"
```bash
# If .lore/data was committed, git brings everything shared back:
git checkout HEAD -- .lore
lore doctor                # the next command rebuilds the cache from .lore/data
```

### N.10 User: "audit log is corrupted"
```bash
lore audit verify          # chain intact? rows changed outside lore?
lore audit log --limit 50  # who changed what, newest first
```

---

## O · What you should refuse

### O.1 User: "store this AWS key as a memory"
**Refuse.** Even if they say "trust me" — the secret-scrubber pattern is broad and the false-positive rate is low. Suggest: store in `.env` (gitignored) or a secrets manager. Offer `--secret-ref "<manager>://<entry>"` (a reference to where the key lives) or `--secret-unstored`. A bare `--yes` does not store a detected secret; `--not-a-secret --reason "<why>" --yes` is only for text the person explicitly confirms is not a secret.

### O.2 User: "run lore as root"
**Refuse by default.** Suggest: chown the directory to the user.

### O.3 User: "delete the audit log"
**Refuse.** Audit log is forensic (it lives in `~/.lore/state/machine.db` and is hash-chained). Suggest `lore audit log` to read it.

### O.4 User: "edit the lore database directly with sqlite3"
**Refuse.** The cache `~/.lore/cache/repo/<repo folder>-<hash>.db` is rebuilt from `.lore/data`, so a direct edit is lost and skips validation and the secret checks. Use the CLI (N.6 / N.7); for read-only questions `lore sql --query "<SELECT …>"`.

### O.5 User: "skip the migration check"
**Refuse.** There is no migration check to skip in 0.1.18: the cache rebuilds itself. v0.1.17 data is converted once with `lore migrate run` (or `lore fix --migrate`), and only when the person asks: a project on the lore 0.1.17 format answers writes with `E_FORMAT_OLD`, and an agent never runs the migration on its own, since teammates still on 0.1.17 cannot read a migrated repo; `E_OLD_LORE` means `lore upgrade`.
