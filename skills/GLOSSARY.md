# Glossary

**In short:** the words lore uses, in alphabetical order, each explained in a line or two.

Terms used throughout the lore documentation and CLI. Alphabetical.

## Activation
A rule's render-time scope policy. Values: `always`, `glob`, `semantic`, `manual`. Determines when the rule appears in the compiled `.lore/LORE.md`.

## Actor
The identity behind any write. Resolved via 8-step fallback chain (flag → env → toml → git config → $USER → machine-id → persisted salt → ephemeral). Always resolves; the chain step is recorded.

## AGENTS.md
A file name several coding tools read for agent context. `lore render --target=AGENTS.md` stitches the `@import` pointer into `AGENTS.md` instead of `CLAUDE.md`; the generated knowledge still lives in `.lore/LORE.md`.

## Audit log
Append-only, hash-chained log of every change lore makes on this machine (create, update, delete, purge, secret reveals), kept in `~/.lore/state/machine.db`. `lore audit verify` checks the chain (`E_AUDIT_CHAIN_BROKEN` on an edited or deleted entry); `lore audit log` lists entries. Teammates' changes are in `git log -- .lore/data`.

## BM25
Ranking function used by SQLite FTS5. Orders search hits by term frequency × inverse document frequency. Better hits = lower bm25() values; lore flips sign so "higher score = better" for callers.

## Bootstrap
The first-time setup: `lore init`, then turning existing notes into rows by hand. See [examples/01-bootstrap.md](./examples/01-bootstrap.md).

## Bundle (support bundle)
Sanitized tarball produced by `lore support-bundle` for bug reports. Contains DB schema, doctor output, scrubbed audit log. No bodies / no credentials.

## Canary
A content-derived sha256 prefix at the top of the generated knowledge file `.lore/LORE.md` (`<!-- AICODER:CANARY=rnd_<hex> -->`). Lets `why-context` look up exactly which render the agent read. Content-derived so renders are deterministic.

## CLAUDE.md
The context file that some coding tools read at the start of every session. lore's default **pointer target**: `lore render` writes the compiled knowledge to `.lore/LORE.md` and stitches an idempotent `@import` pointer into CLAUDE.md, leaving any hand-written content intact. The agent reads CLAUDE.md, which imports `.lore/LORE.md`; the user writes to lore; `lore render` bridges the two.

## Comment
Polymorphic free-form note attached to any entity via `--on-table` + `--on-id`. Timeline-ordered.

## Decision
ADR-style architectural choice with rationale, alternatives considered, and revisit criteria. Use when the WHY matters more than the WHAT.

## id
A monotonically-increasing per-project integer per entity kind. Lets users say `mem_<id>` instead of `mem_019e...`. Internal to one project; not portable across DBs.

## doctor
Health-check command. Exits 0 (healthy), 1 (degraded), 2 (broken). Returns structured JSON with `--json`. Probes: DB integrity, schema presence, identity resolution, WAL size.

## ent
The ORM used by lore. Schema-first (Go), code-gen'd. Schemas at `dbent/schema/`. Generated code at `dbent/gen/ent/` — never edit by hand.

## entity_table / entity_id
The polymorphic FK pattern for `comments` and `entity_tags` tables. `entity_table` is the literal table name (e.g., `"memories"`), `entity_id` is the opaque ID.

## Ephemeral identity
Last-resort identity step when all other resolvers fail. Generates a one-session pseudo-actor (`auto:ephemeral:<sha>`). Writes succeed but actor is anonymous.

## FTS5
SQLite's full-text-search extension. lore uses contentless FTS5 over `memories.body` with BM25 ranking. Falls back to LIKE if FTS5 isn't compiled in.

## Hotfix
A loud recurring warning the team keeps hitting. Pinned in render output, never truncated under budget pressure.

## Identity
See **Actor**.

## Master scope
A row with no `repo_id` — applies to the entire project, not just one repo. The default when no `--repo` is passed.

## Memory
Free-form learned fact. The lightest-weight capture shape. No rationale, no severity, no enforcement.

## Mission
A container for tasks. Used to group related work items under one initiative with a target date.

## Mode A
The v0.1.17 name for the default: one project per repo, its rows under `.lore/data/` (see Row file), shared through git. In 0.1.18 every repo works this way; the local cache is `.lore/lore-cache.db`.

## Row file
`.lore/data/<table>/<id>.json` — one canonical JSON file per shared row (sorted keys, UTC timestamps, `_v` format version, `_table`). The committed source of truth; `.lore/lore-cache.db` is a cache of these files (machine-local state lives in `~/.lore/state/machine.db`).

## Bootstrap / Adopt
The v0.1.17 first sync pass (0.1.18 writes the row files directly; v0.1.17 data converts with `lore migrate run`). **Bootstrap** (no `.lore/data` yet): back up `lore.db`, export every shared row, write `_meta.json`. **Adopt** (`.lore/data` exists but this DB never synced — a teammate's pull, a fresh clone, a restore): switch to the shared project id, import files, export private rows, keep losing versions of clashing rows as conflicts.

## Sync trash
Rows deleted because their file disappeared (revert, `git clean`, stash) are copied to the trash first; `lore trash list --local` lists them; core rows come back from git (`git checkout HEAD -- <file>`).

## Merge driver
A program git runs instead of its line-by-line merge for files `.gitattributes` assigns to it. lore's (`lore merge-driver`) merges row files field by field: a field changed on one side takes that side, `updated_at` takes the later time, a status / priority / date / reference changed both ways takes the newer edit (and says so), and text changed both ways becomes a real conflict with markers inside that one value. Configured in each clone automatically; host merge buttons (GitHub, GitLab) never run it.

## ci-merge
`lore sync ci-merge --base <ref>` — merges a base branch into the current branch with lore's driver and commits, only when the host would see a conflict, every conflict is in lore data, and lore settles all of them; otherwise reports `needs-human` and changes nothing. Never pushes. What the optional pull-request workflow runs.

## lore-sync-merge workflow
`.github/workflows/lore-sync-merge.yml`, written by `lore sync install-action` (opt-in; runs only when started by hand unless installed with `--auto`; a re-run or a version update keeps the triggers the file already has). Runs `ci-merge` for open pull requests (with `--auto`, whenever lore data changes) and pushes the merges it is allowed to make. Uses a `LORE_SYNC_TOKEN` secret when present so required checks re-run on its merge commits. Its lore version is pinned `auto` (follows the newest lore release a developer runs in the repo; never downgraded) or `fixed` (`--lore-version`); a checksum line marks hand edits, which stop the automatic updates.

## Mode B
The v0.1.17 shared-database mode: several repos pointing at one shared database file via `.lore/lore.yaml` (older: `.lore/lore.toml`). Retired in 0.1.18: share notes through a store (`lore store init` / `lore store add`); `lore migrate run` converts such a repo.

## NFC
Unicode Normalization Form C. lore normalizes all text input to NFC on write so equivalent characters compare equal. Defeats homoglyph attacks and accidental encoding drift.

## Pattern
Reusable code shape captured for future reference.

## Playbook
Reusable multi-step procedure (different from this docs' playbooks file — same idea, different scope).

## Prefix
The 3-character identifier prefix on every opaque ID. `mem_` = memory, `rul_` = rule, `dec_` = decision, etc. 32 registered prefixes; see `saas/pkg/lore/ids/registry.go`.

## Project
The top-level scope in lore. One project per `lore init`. Has many repos.

## Quick check
SQLite's `PRAGMA quick_check` — fast page-level integrity check. lore runs it on every open + augments with a schema-presence probe (was the file truncated to zero?).

## Reminder
Time-based notification. Supports recurrence (`7d | 30d | 1m | 3m | 6m | 1y`). One-shot reminders flip to done; recurring ones reschedule.

## Render
The compile step: structured DB → generated knowledge file (`.lore/LORE.md` by default) + an idempotent `@import` pointer stitched into the agent file (`CLAUDE.md` by default). Deterministic — same DB content produces byte-identical output. `--out` changes the generated path; `--target` changes the pointer's agent file; `--no-pointer` skips the stitch.

## Repo
A code repository registered within a project. Identified by `mount_name` (human) or `rep_id` (opaque). Used to scope rules / memories / tasks to one part of a multi-repo project.

## Rule
Hard constraint with severity (`must | should | may`) and activation (`always | glob | semantic | manual`).

## Schema version
The DB schema generation. v0.1 = `schema_version: 1`. Bumped only on breaking changes. Additive changes don't bump.

## Scope
The triple `(project_id, repo_id, master?)` that determines who sees a row. Flags `--repo`, `--all-repos`, `--master-only`, `--no-inherit` control scope at query time.

## Secret scrubber
Pre-write check that refuses bodies containing credential patterns (AWS keys, GitHub PATs, JWTs, OpenAI keys, Stripe keys, etc.). The write stops with `E_NEEDS_CONFIRM` (the JSON error carries `findings[]` and `next.add_args_one_of`); a bare `--yes` does not store it. The choices are `--secret-ref "<manager>://<entry>"`, `--secret-unstored`, or `--not-a-secret --reason "<why>" --yes` (after the person confirms); at a terminal a menu offers redact / unstored / not a secret (reason required) / cancel. The commit hook still refuses a credential.

## Symlink protection
lore refuses to write a database file (the cache and its sidecars, the machine store), the store lock `.lore/lock` or an audit file through a symbolic link (`E_SYMLINK_DB`); `.lore/LORE.md` may be a link only when it stays inside the repository. Prevents an attacker with write access to `.lore/` from redirecting writes to `/etc/passwd` etc.

## Tag
Polymorphic label. Created once (`tag add --name=…`), then attached to many entities via `tag attach --on-table=… --on-id=…`.

## Task
Discrete work item with status + priority + optional mission. Lifecycle: todo → in_progress → done; alternative terminal states: cancelled / blocked.

## TaskList
Alternative grouping for tasks. Lighter than Mission; no target date.

## TOML pointer
The 2-line file at `.lore/lore.yaml` (older: `.lore/lore.toml`) in a v0.1.17 Mode B repo; `lore migrate run` converts it and keeps it as `<file>.migrated`. Lists `db_path` (where the shared DB lives) and `project_id` (which project this directory belongs to).

## Trojan source defense
lore strips bidi-override + zero-width characters from input text. Prevents copy-paste attacks that visually swap characters mid-string.

## UUIDv7
Timestamp-prefixed UUID variant. lore uses UUIDv7 for all IDs so insert order ≈ chronological order (id-sorted = newest last). Critical for FTS5 + audit chain ordering.

## why-context
Introspection command. Shows what the most recent render included/excluded and why. Use when "the agent seems to be missing X."

## WAL
SQLite's Write-Ahead Log mode. lore uses it via PRAGMA for crash safety + concurrent readers. Sidecar files: `lore-cache.db-wal`, `lore-cache.db-shm`.
