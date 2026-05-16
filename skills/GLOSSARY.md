# Glossary

Terms used throughout the lore documentation and CLI. Alphabetical.

## Activation
A rule's render-time scope policy. Values: `always`, `glob`, `semantic`, `manual`. Determines when the rule appears in compiled CLAUDE.md.

## Actor
The identity behind any write. Resolved via 8-step fallback chain (flag → env → toml → git config → $USER → machine-id → persisted salt → ephemeral). Always resolves; the chain step is recorded.

## AGENTS.md
Codex / OpenAI convention for AI context files. lore can render to `AGENTS.md` via `--target=AGENTS.md`. Same content shape as CLAUDE.md.

## Audit log
Append-only ledger of every write, with hash chain for tamper detection. Verifiable via `lore audit verify` (v0.2).

## Bench
The DB-backed evaluation engine. Three nouns: **eval** (task template), **run** (one execution), **result** (task × arm × attempt outcome). Replaces the Phase-1 YAML + bash runner. See `BENCH_DESIGN.md`.

## Bench eval
A benchmark task template — independent of any run. Has `code` (e.g. `E1-001`), category enum, prompt, polymorphic link to a captured rule/hotfix/decision/memory, and a grader spec. Authored via `lore bench eval add`.

## Bench run
One benchmark execution. Captures `model + temperature + runs_per_arm + claude_md_sha256` (for reproducibility). Status flow: `running → complete | aborted | failed`. The 1:N parent of bench_results.

## Bench result
One row per (run × eval × arm × attempt). Stores `prompt_sent + output_received` verbatim so the grader can be re-run after a bug fix without re-spending on LLM calls. `grade` enum: `pass | fail | error | skipped`. The unit of statistical analysis.

## BM25
Ranking function used by SQLite FTS5. Orders search hits by term frequency × inverse document frequency. Better hits = lower bm25() values; lore flips sign so "higher score = better" for callers.

## Bootstrap
The first-time setup: `lore init` + `lore learn-from docs`. See [examples/01-bootstrap.md](./examples/01-bootstrap.md).

## Bundle (support bundle)
Sanitized tarball produced by `lore support-bundle` for bug reports. Contains DB schema, doctor output, scrubbed audit log. No bodies / no credentials.

## Canary
A content-derived sha256 prefix in CLAUDE.md (`<!-- AICODER:CANARY=rnd_<hex> -->`). Lets `why-context --last-render` look up exactly which render the AI consumed. Content-derived so renders are deterministic.

## CLAUDE.md
Anthropic's convention for AI context files. lore's default render target. The AI reads this; the user writes to lore; lore render bridges the two.

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

## learn-from
Bootstrap command that ingests existing markdown (`CLAUDE.md`, `AGENTS.md`, `.cursorrules`, README) into `learn_candidates` for review. Use `lore learn promote <id>` to accept.

## Master scope
A row with no `repo_id` — applies to the entire project, not just one repo. The default when no `--repo` is passed.

## Memory
Free-form learned fact. The lightest-weight capture shape. No rationale, no severity, no enforcement.

## Mission
A container for tasks. Used to group related work items under one initiative with a target date.

## Mode A
Local DB mode. One `.lore/lore.db` per project root. The default.

## Mode B
Shared DB mode. Multiple project roots pointing at a single shared SQLite file via `.lore/lore.toml`. Used when many sibling projects want one knowledge base.

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
The compile step: structured DB → text file (CLAUDE.md by default). Deterministic — same DB content produces byte-identical output.

## Repo
A code repository registered within a project. Identified by `mount_name` (human) or `rep_id` (opaque). Used to scope rules / memories / tasks to one part of a multi-repo project.

## Rule
Hard constraint with severity (`must | should | may`) and activation (`always | glob | semantic | manual`).

## Schema version
The DB schema generation. v0.1 = `schema_version: 1`. Bumped only on breaking changes. Additive changes don't bump.

## Scope
The triple `(project_id, repo_id, master?)` that determines who sees a row. Flags `--repo`, `--all-repos`, `--master-only`, `--no-inherit` control scope at query time.

## Secret scrubber
Pre-write check that refuses bodies containing credential patterns (AWS keys, GitHub PATs, JWTs, OpenAI keys, Stripe keys, etc.). Override via `--allow-secrets`, which is logged loudly.

## Symlink protection
lore refuses to open `.lore/lore.db` if it's a symlink (O_NOFOLLOW semantics). Prevents an attacker with write access to `.lore/` from redirecting writes to `/etc/passwd` etc.

## Tag
Polymorphic label. Created once (`tag add --name=…`), then attached to many entities via `tag attach --on-table=… --on-id=…`.

## Task
Discrete work item with status + priority + optional mission. Lifecycle: todo → in_progress → done; alternative terminal states: cancelled / blocked.

## TaskList
Alternative grouping for tasks. Lighter than Mission; no target date.

## TOML pointer
The 2-line file at `.lore/lore.toml` in Mode B. Lists `db_path` (where the shared DB lives) and `project_id` (which project this directory belongs to).

## Trojan source defense
lore strips bidi-override + zero-width characters from input text. Prevents copy-paste attacks that visually swap characters mid-string.

## UUIDv7
Timestamp-prefixed UUID variant. lore uses UUIDv7 for all IDs so insert order ≈ chronological order (id-sorted = newest last). Critical for FTS5 + audit chain ordering.

## why-context
Introspection command. Shows what the most recent render included/excluded and why. Use when "the AI seems to be missing X."

## WAL
SQLite's Write-Ahead Log mode. lore uses it via PRAGMA for crash safety + concurrent readers. Sidecar files: `lore.db-wal`, `lore.db-shm`.
