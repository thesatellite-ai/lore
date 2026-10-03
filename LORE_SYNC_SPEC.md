---
title: Lore sync across git branches and developers
status: implemented
---

# Lore sync across git branches and developers — spec + plan

<DocStatus state="approved" owner="khanakia" updated="2026-10-03"></DocStatus>

Make lore's knowledge (rules, decisions, memories, tasks, …) travel with the code: per git branch, merged when the branch merges, shared through the same `git push` / `git pull` / PR flow every developer already uses. `main`'s copy is the "lore main DB". No server, no S3, no GitHub Action, no second version-control system.

<Callout type="info" title="One-line summary">

Lore keeps SQLite as its fast local engine, and writes every synced row as one small canonical JSON file under `.lore/data/`, which is committed. Git moves the files around; lore re-loads whatever git changed into its local `lore.db` before each command.

</Callout>

<Callout type="tip" title="Implementation status — shipped (uncommitted in the working tree, 2026-10-03)">

All phases P0–P7 are implemented and tested. The design below is kept as written where it held; every place the build deliberately departed from it is listed in **§0 Deltas** and corrected inline. Work is tracked in lore: mission "Git-backed lore sync (LORE_SYNC_SPEC.md)" in this repo's local lore DB.

</Callout>

## 0. Implementation status

### What shipped and where

| Area | Files |
|---|---|
| Sync engine (registry, triggers, codec, reconcile, bootstrap, adopt, merge driver, ops) | `saas/pkg/aicoder/lsync/` — `registry.go`, `infra.go`, `codec.go`, `store.go`, `files.go`, `meta.go`, `engine.go`, `bootstrap.go`, `ops.go`, `hygiene.go`, `mergedriver.go` |
| Generic helpers (importable by any project) | `saas/pkg/aicoder/canonjson` (byte-stable JSON), `saas/pkg/aicoder/merge3` (field-level three-way merge), `saas/pkg/aicoder/gitsetup` (idempotent git config / attribute blocks / chained hook blocks, `ReadTree`, `CommitFiles`) |
| Deterministic ids for natural keys | `saas/pkg/aicoder/ids/deterministic.go`, `saas/cmd/cli/natural_ids.go` (ent hook) |
| CLI wiring (pass before and after every command, fresh-clone build, git wiring, LORE.md re-render) | `saas/cmd/cli/sync_wire.go`, `memory.go` (`resolveContext`), `main.go`, `init.go` (`createProjectDB`, adopt-on-init) |
| Commands | `saas/cmd/cli/sync_cmd.go`: `lore sync`, `status`, `export --all`, `conflicts`, `resolve`, `trash`, `trash restore`, `purge`, `fix-projects`, `install-git`, `peek`, `promote`, `dupes`, `merge-rows`, hidden `hook`, hidden `merge-driver`; `lore restore --prefer` (`backup.go`); doctor `[sync]` section (`doctor.go`) |
| Docs | `README.md` (Team sync through git, Sharing through git, FAQ), `skills/` (SKILL, SKILL-mini, COMMANDS, PLAYBOOKS P13/P13b, GLOSSARY), `docs/DEVELOPMENT.md`, agent directive (`directive.go`) |
| Tasks | `task lore:test:sync`, `task lore:bench:sync` |

### Tests

| Suite | What it pins |
|---|---|
| `lsync` | bootstrap, adopt (project-id rewrite, prefer modes, purged ids, broken meta), import and export in both directions, raw-SQL and volatile-only writes, both-sides-changed merge and conflict copies, delete-vs-edit, invalid / conflicted / newer-format / mismatched files never imported nor overwritten, missing data dir, read-only, secrets, unknown fields, older files, natural-key merges in both directions, symlinks, oversize, crash debris, zero-length repair, lock timeout, purge, fix-projects, dangling refs, duplicates, dirty-only pass, codec for every column type, registry gates (every table classified; no counters in synced tables — both proven by feeding them a failing case), merge driver (clean, newest-wins, markers, add/add, non-string clash, `_purged` union, git text fallback, newer format) |
| `merge3`, `canonjson`, `gitsetup`, `ids` | the generic pieces, table-driven |
| CLI unit | data-dir rules, materialize conditions, cutoff / prefer parsing, hook and attribute text, broken-file detection, deterministic-id hook (drift test against the registry, proven by deleting an entry) |
| CLI end-to-end (24 scenarios, real binary + real git, isolated `HOME`) | init/bootstrap/fresh clone; branches follow and merge; same-row driver merge, conflict, pre-commit block and hand resolution; no-driver (GitHub) text conflict; revert → trash → restore; husky-style `core.hooksPath` chaining; `LORE_SYNC=0`; rollout of two pre-existing DBs; `restore --prefer db`; `init` on a clone adopts; secret never exported; post-merge hook imports; doctor and status; read-only; peek; promote; dupes; worktree; `stash -u`; deleted DB rebuilt; hand-added secret blocked at commit; LORE.md re-rendered after a pull and byte-identical across clones |
| Existing bash suites (`tests/run-all.sh`, `task lore:scenarios` + `lore:chaos`) | all green: 46 pass, 0 fail, 0 skipped when run with sudo available (without sudo, SC-21 skips; the refusal is also unit-tested). They were 23-red before this work; see "Also fixed" below |

Guards verified by breaking them: the marker-inside-JSON check in pre-commit, the never-overwrite-a-blocked-file guard (it needed its own white-box test — the first attempt passed vacuously because a second mechanism also protects it), the registry classification gate, the counter gate, and the natural-id drift gate.

### Also fixed while driving the existing suites to green

These were pre-existing and unrelated to the sync design, but sync's extra writes made the first one more frequent, and the gates could not go green without them:

| Problem | Fix | Pinned by |
|---|---|---|
| Two lore processes writing at once failed with `SQLITE_BUSY` and lost rows: `ApplyPragmas` hit the write lock before its own `busy_timeout`, and the timeout was set on one pooled connection only | `busy_timeout` and `_txlock=immediate` in the DSN for every connection; `busy_timeout` first in `ApplyPragmas` (`dbent/main.go`) | `TestE2E_ConcurrentWriters` (proven by reverting the fix), SC-5 |
| Searching any text containing `-`, `:` or `.` (e.g. `round-trip`) crashed with `no such column` — FTS5 parsed it as syntax | when SQLite rejects the query as FTS5 syntax, retry once with each word quoted literally; real FTS5 syntax still works (`saas/pkg/aicoder/fts5/query.go`) | `fts5` tests (proven by disabling the fallback), SC-16 |
| `lore project shared-init` (Mode B) never built the search index: every search failed with `no such table: memory_fts` | shared `setupSearchIndex` used by `init`, the fresh-clone build and `shared-init` | `TestE2E_SharedInitSearchAndNoSync`, SC-7 |
| A blank query (`search ""`) surfaced as `E_INTERNAL` | `E_INVALID_INPUT` with a hint to use `list` | SC-4, SC-10 |
| `task lore:scenarios` / `lore:chaos` looked for `../tests` and could never run | `TESTS_DIR` points at `tests/` | the tasks themselves |
| `go test -race` flagged parallel tests migrating at once (ent mutates package-level metadata) | migrate once per test package, copy the file per test (`dbent/pkg/dbtemplate`) | `task lore:test` with `-race` |
| Lint gates red on code nobody touched (3 unformatted files, 43 redundant `Sprintf`, 18 dead symbols incl. the superseded v0.1 `search.go`) | formatted, simplified, removed — no suppressions | `task lore:lint` + staticcheck clean |
| Scenario scripts used retired syntax (positional bodies, `aicoder.db`, `E1-001` eval codes, memories rendered into `CLAUDE.md`) | updated to the current CLI; assertions kept, re-aimed at the documented behaviour (must-rules in `.lore/LORE.md`, `@import` pointer in `CLAUDE.md`) | the scripts |

### Fixed after an independent code review of the whole diff

| Finding | Fix | Pinned by |
|---|---|---|
| Pre-commit guard skipped an UNSTAGED lore file (git's ` M` lost its leading space to output trimming) and then auto-staged it unchecked | `gitsetup.Uncommitted` parses `--porcelain -z` untrimmed (renames handled) | `TestUncommitted_UnstagedAndSubdir`, `TestE2E_PreCommitGuardsUnstagedFileInSubdirProject` (proven by reverting) |
| Same guard read the wrong files when the lore project lives in a subdirectory of the repo (repo-root-relative paths joined onto the project dir) | `Uncommitted` returns paths relative to the directory asked about | same tests |
| `lore restore --prefer <typo>` replaced the DB, then failed before resetting sync state | flags validated before any file is touched | `TestE2E_RestoreRejectsBadPreferBeforeTouchingDB` |
| `--prefer db` re-imported rows added after the backup (they still had files), silently undoing the restore | after a restore the preference is a rollback: `db` removes file-only rows' files, `files` trashes DB-only rows | `TestAdopt_PreferIsARollback` |
| Deterministic ids come from editable fields: re-creating a row under a renamed row's old key hit a primary-key collision | on an id (not natural-key) collision the hook falls back to a random id | `TestNaturalIDHook_RenamedRowDoesNotBlockRecreate` (proven by disabling) |
| `--columns` scoped only the first search term (FTS5 binds a column filter to the next phrase) | `{cols}: (expr)` | `TestSearchEntity_ColumnFilterScopesAllTerms` (proven by removing the parentheses) |
| Two lore projects in one repo overwrote each other's hook block | per-project hook marker | `TestHookMarkerPerProject`, `TestE2E_TwoProjectsShareHooks` |
| Doctor's file-error count disagreed with its warnings | doctor reports the exact list it warns about (`file_errors`) | `TestE2E_DoctorAndStatus` |
| Duplicated project-file counter; hardcoded doctor statuses and flag names | `lsync.CountRowFiles`; named constants in `doctor.go` and `saas/pkg/constants/flags.go` | build + lint |
| (found in self-review) A natural-key merge left references dangling in rows imported later in the same pass (tables sync in name order) | merges are re-applied after every drain round; id rewrites never touch `_lore_sync_*` bookkeeping | `TestNaturalKeyMerge_RewritesRowsImportedLater` |

### Found by a trial migration of a real repo (ifpghub_backend)

Copies only: a bare remote cloned from the repo, the real `lore.db` copied in, two clones (A migrates, B joins fresh with the repo's `core.hooksPath .githooks`), the same memory edited on two branches and merged. The real repo was not touched. Migration: 1,690 rows → 1,691 files (7 MB) in about 1 s; B rebuilt its DB in about 1 s with all 11 shared tables matching the original row for row.

| Found | Fix | Pinned by |
|---|---|---|
| The repo's `.gitignore` had `_*`, so `git add` silently dropped `.lore/data/_meta.json` and B's fresh clone failed with `E_NOT_PROJECT_ROOT` | git wiring probes one path per kind of lore file with `git check-ignore --no-index`; when a rule hides any, it appends `!.lore/data/` exceptions to `.gitignore`; if that cannot help (a whole `.lore/` folder is excluded) it undoes the edit, warns once per state of the ignore files, and `install-git` fails with `E_SYNC_DATA_IGNORED` | `TestIgnoredPaths`, `TestSamplePaths`, `TestE2E_RepoIgnoreRuleCannotHideData`, `TestE2E_IgnoredLoreFolderIsReported` (both verified failing with the fix disabled) |
| A teammate on lore 0.1.9 could not commit at all: the committed `.githooks/pre-commit` called `lore sync hook`, which 0.1.9 does not have | the pre-commit line probes `lore sync hook --help` first and prints an "upgrade it" note instead of failing | `TestHookLines`, `TestPreCommitLineAgainstOldAndNewLore` (runs the real line against stand-in old and new binaries) |
| A same-text conflict could not be settled with lore: the file was blocked, so `lore memory edit` changed the DB but never the file | a valid file with markers inside a value is imported (clean fields land, both versions visible) and its notice persists until an edit exports a settled file; raw-marker files stay blocked and are never overwritten | `TestConflictMarkersInsideStringValue` (rewritten to the new contract), `TestRawConflictMarkersStayBlocked`; trial: blocked commit → `lore memory edit` → commit → A and B converge |
| `lore memory show` crashed (nil `valid_until`) on every memory without a valid-until date — all 550 in that repo, also in the released 0.1.9; `run show` / `run end` had the same shape with `started_at` | nil guards at all three sites (every other optional-time dereference in the CLI was checked and is guarded or a non-pointer field) | `TestE2E_ShowCommandsWithOptionalTimesUnset` (verified failing with the guard removed) |

Not changed, needs a decision: `.lore/LORE.md` gets a new random `AICODER:CANARY` id on every render, so any clone that re-renders after a pull shows `LORE.md` modified even when no rule changed. It merges cleanly (`merge=lore-keep-ours`) but is noise in `git status`.

### Deferred features and gaps closed (previously skipped scenarios)

| Item | Built | Pinned by |
|---|---|---|
| Audit trail (R16 #4, R27 #9) — the `audit` package existed but nothing wrote to it | every sync pass appends one hash-chained entry per changed shared row (`<table>.write` / `.import` / `.merge` / `.external-write`) inside the pass transaction (`lsync` `Options.OnChange`); chain ordered by insertion; `lore audit verify` (no sync pass: flags broken chain and rows changed outside lore) and `lore audit log` | `audit` tests, `TestOnChangeObserver`, `TestUnexportedChanges`, `TestE2E_AuditVerifyAndLog`, SC-2 |
| Schema migration log (R21 #49) — table existed, never written | `migrationlog`: definition hash + resulting schema hash per migration; `lore setup` records begin → migrate → complete; doctor: interrupted = broken, hash mismatch = degraded | `migrationlog` tests, SC-26, CH-3 |
| Network / cloud-sync filesystem refusal (R18 #17) | `guard.CheckNetworkFS`: iCloud / Dropbox / OneDrive / Google Drive path segments + per-OS fs-type probe (NFS, SMB, AFP, WebDAV); `E_NETWORK_FS`, override `LORE_ALLOW_NETWORK_FS=1` | `guard` tests, `TestCheckNetworkFSOverride`, SC-24 |
| Refuse root (R16 #14) | every command refuses euid 0 (`E_ROOT_REFUSED`), override `LORE_ALLOW_ROOT=1` (legacy `MINI_ALLOW_ROOT=1`) | `TestRootRefusal` (injected uid); SC-21 runs when root or sudo is available (verified under sudo) |
| Full disk (R23 #1) | SQLite full / cannot-open on a full filesystem is reported as `E_DISK_FULL`; the DB stays intact and lore recovers when space is freed | `TestExplainStorageError`, CH-7 (real full disk on a mounted image, macOS) |
| Monotonic `tx_at` (R27 #29) | per-DB high-water mark; a new memory gets max(now, high-water + 1µs) | `TestTxAtMonotonicAcrossClockRewind`, CH-2 (libfaketime when installed, else a simulated clock rewind) |
| Corruption detection wording | doctor names a damaged file `E_DB_CORRUPT` with the repair command | CH-1 (now deterministic) |
| `lore init` refused any existing `.lore/`, and appended ignore lines git already covered | refuses only an existing `lore.db` / `lore.toml`; skips lines git already ignores | `TestE2E_InitRefusesOnlyRealProject`, `TestE2E_InitRespectsBroaderIgnore` |
| Gaps | cross-OS build gate in `task lore:lint` (all six release targets, verified by deleting a platform file); `go mod tidy` for `saas`; manual-trigger docsync workflow; Windows hook note in README; stale "aicoder" error texts fixed | the gates |
| Work tracking | lore mission "Git-backed lore sync (LORE_SYNC_SPEC.md)" with 12 done tasks in this repo's local lore DB (`.lore/` is gitignored here) | `lore mission list` |

### Measured performance (Apple silicon, APFS)

| Measurement | 1k rows | 10k rows |
|---|---|---|
| No-op full pass (stat cache warm) | 9 ms | 70 ms |
| Bootstrap (export every row) | 0.27 s | 2.7 s |
| Fresh-clone adopt (import every file) | 0.20 s | 2.5 s |

Real per-command overhead at 2k rows (`lore tag list`): 64 ms with sync on vs 42 ms with `LORE_SYNC=0`, i.e. +22 ms. Two optimisations came out of measuring: per-file `fsync` made bootstrap take 74 s at 10k rows (dropped; see Deltas), and loading every base blob on each pass dominated the no-op cost (now lazy). The end-of-command pass is dirty-only, so one full directory walk happens per command, and git wiring is verified by a stat fingerprint instead of four `git` processes (each 20–27 ms on this machine).

### Deltas from the original design

| Topic | Spec said | Built | Why |
|---|---|---|---|
| Write ordering (§4 rule 3, §8.1) | file first, then DB | DB write first (any writer); triggers record the row in a dirty set in the same transaction; the end-of-command pass exports it | One mechanism for every writer (ent, raw SQL, tui, sqlite3); the dirty mark survives a crash, so no write can be lost between DB and file |
| Change detection (§8.2) | `git rev-parse HEAD:.lore/data` + `git status` | stat cache (size, mtime, racy-git window) + content hash per file; no git needed | Covers committed and uncommitted changes and non-git directories (E17) uniformly |
| Outbox (§12.1) | log of operations | a SET of `(table, id)`; the drain compares actual content | Idempotent, no operation ordering to get wrong |
| Trigger SQL | `INSERT OR IGNORE` | guarded `INSERT … WHERE NOT EXISTS` | SQLite lets an outer UPSERT's conflict policy override the trigger's `OR IGNORE`, turning duplicates into hard errors — found by the tests |
| Directory names (§5) | `rule/`, `memory/` | the SQL table name (`rules/`, `memories/`) | Unambiguous mapping, no singularisation table |
| Envelope key (§6) | `_kind` | `_table` | Same reason |
| Timestamps (§6) | RFC3339 UTC, milliseconds | UTC, fixed nine fractional digits | ent writes nanoseconds; fixed width keeps string comparison chronological for the merge driver |
| `fsync` per file | implied by "atomic" | atomic rename without `fsync`; a zero-length file left by a power loss is re-exported from the DB | `fsync` cost ~7 ms per file (74 s bootstrap at 10k rows); the DB WAL is the durable copy |
| Hooks (§10) | set `core.hooksPath=.githooks` when unset | never set `core.hooksPath`; chain a marked block into whatever hook directory git already uses | Setting it would silently disable existing `.git/hooks` scripts |
| Git wiring frequency | every command | every command, skipped when a stat fingerprint of the git config, hook files and `.gitattributes` is unchanged | Each git spawn costs 20–27 ms |
| Uncommitted reminder (E37) | every command | after commands that wrote row files; `lore sync status` and doctor always show the count; doctor treats it as information, not degradation | A `git status` per command doubled the overhead; uncommitted work is normal state |
| Actor emails (E4, Q3) | commit a hash | commit `stable_key` as is | Hashing needed identity-resolution changes and produced duplicate actors; git commit metadata already exposes the same email to every reader of the repo |
| Hard delete (E9) | no hard deletes; delete = archive | `lore <kind> delete` removes the row; the export removes the file | lore already ships hard delete; a modify/delete conflict in git is visible, not silent |
| Newer-format file in the merge driver (E13) | exit 1, leave conflict | fall back to `git merge-file` (text merge) | The driver never rewrites a file it cannot fully represent, and the user still gets standard markers |
| File id ≠ file name (E30) | trust the content; `doctor --fix` renames | refuse to import; report on every pass | Loud beats clever; renaming is a one-line manual fix |
| `min_lore_version` (E45) | in `_meta.json` | not added: every row file carries `_v`, and older binaries refuse newer files per file | Same protection without an extra field |
| Duplicate detection (E48) | similarity | exact match after case and whitespace normalisation (`lore sync dupes`), folded with `lore sync merge-rows` | Similarity is a judgement call; exact duplicates are the common case after adopt |
| P7 items | later | shipped: `peek` (the Q1 cross-branch view, as `lore sync peek <ref>`), `promote`, `purge` | All phases were requested |

## 1. Problem

Teams work on `main`, `stag`, `dev` and many `feature/*` branches at once. Lore's data does not follow any of them today.

| Fact today | Where | Consequence |
|---|---|---|
| `.lore/lore.db` is a per-clone SQLite file, auto-added to `.gitignore` | `saas/cmd/cli/init.go:232` (`ensureGitignore`) | Nothing a developer teaches lore ever reaches a teammate |
| lore is branch-blind | no branch handling anywhere in `saas/` | `git checkout main` still shows `feature/xyz`'s memories and tasks |
| Mode B (`.lore/lore.toml` → shared DB path) | `saas/cmd/cli/project_shared.go` | shares a DB between projects on ONE machine; not between developers or branches |
| no export / import / sync command | `saas/cmd/cli/` | no path to share at all |
| IDs are UUIDv7 `<prefix>_<32hex>` | `saas/pkg/aicoder/ids/ids.go:29` | good news: two devs creating rows never collide, so merging is solvable |

## 2. Goals and non-goals

**Goals**

- Lore data follows git branches: checkout, merge, rebase, revert, cherry-pick, stash all "just work" for lore the way they do for code.
- `main` holds the canonical team knowledge. A branch's knowledge reaches `main` exactly when its PR merges, and never before.
- One channel: pushing code pushes its knowledge. Memory cannot be lost separately from the code that produced it.
- The GitHub "Merge pull request" button works in the normal case (different rows changed).
- Knowledge changes are reviewable in the PR diff.
- Zero required infrastructure, zero metered CI, zero credentials beyond git.
- Correct with hooks absent: hooks may speed things up but nothing depends on them.

**Non-goals (v1)**

- Seeing another branch's unmerged knowledge live (a read-only `--ref` view is a later phase).
- Real-time multi-user editing.
- Syncing telemetry (search logs, render history, run logs).
- Mode B (shared DB across projects) — keeps today's behaviour; see Open question Q4.

## 3. Decisions

<ADR status="accepted" id="SYNC-001" date="2026-10-03" title="Text files in git are the source of truth; lore.db becomes a local cache">

### Context

We need lore data to follow branches and merge with PRs. Four storage options were evaluated (see SYNC-002..004 for the rejected ones).

### Decision

Every synced row is written as one canonical JSON file under `.lore/data/<kind>/<id>.json` and committed. `.lore/lore.db` stays gitignored and is a derived cache rebuilt from those files at any time.

### Consequences

- Git provides branching, merging, history, transport, review and access control for free.
- One-file-per-row means concurrent additions never conflict; conflicts only arise when the same entity is edited on two branches.
- Lore must build: a canonical serializer, a write-through exporter, an incremental importer ("reconcile"), and an optional field-level merge driver.
- Lore files appear in PR diffs (this is a feature for review, and noise for people who don't care — mitigated with `.gitattributes linguist-generated`).

</ADR>

<ADR status="rejected" id="SYNC-002" date="2026-10-03" title="Commit the binary lore.db">

### Context

Simplest idea: commit `lore.db` itself, with a custom git merge driver for it.

### Decision

Rejected. Measured on a 4.1 MB DB with 100 commits: repo size is fine after packing (9 MB normal gc, 2.3 MB aggressive) but **230 MB of loose objects locally, and git's auto-gc never fires** (it counts objects, not bytes; also with `gc.auto=200`). Worse: GitHub's merge button does not run custom merge drivers, so every PR where `main`'s DB also changed is blocked with "too complex to resolve in the web editor"; a clone without the driver installed resolves by picking one whole file and silently drops teammates' rows; PR diffs are unreadable; GitHub rejects files over 100 MB.

### Consequences

None — not built.

</ADR>

<ADR status="rejected" id="SYNC-003" date="2026-10-03" title="Main DB in S3 (or any store) + per-branch DBs + pre-push sync hook">

### Context

Store `main.db` and `branches/<b>.db` in S3; a pre-push hook uploads the branch DB; lore merges branch DB into main when the PR merges.

### Decision

Rejected. Workable, but it is a second transport beside git, so lore would have to build what git already does: base snapshots for 3-way merge, detection of "PR merged" (breaks on GitHub squash merges, whose commit is not an ancestor), compare-and-swap on concurrent writes to `main.db` (S3 `If-Match`), branch switching, credentials on every machine, and a blocking hook when offline. It is also close to the central-store option that was ruled out.

### Consequences

None — not built.

</ADR>

<ADR status="rejected" id="SYNC-004" date="2026-10-03" title="Dolt (git-for-data SQL engine)">

### Context

Dolt stores tables as content-addressed Prolly trees (~4 KB chunks), merges cell-by-cell by primary key, auto-gc since 1.75, and can use a GitHub repo as a remote under `refs/dolt/data`.

### Decision

Rejected for v1. Best merge engine of all options, but its branches live beside git's: `git checkout` / PR merge do not move Dolt data, so a git→Dolt branch mapping and a "PR merged → dolt merge" trigger (i.e. a GitHub Action) are still needed. Also: MySQL dialect (lore uses SQLite FTS5 in 11 files and raw SQL in 13), a ~103 MB engine inside a single-binary CLI, and lore changes invisible in GitHub PR diffs.

### Consequences

Revisit only if SQL-level history queries (`AS OF`, cell lineage) become a hard requirement.

</ADR>

## 4. Architecture overview

```
 ┌──────────────────────────── developer machine ─────────────────────────────┐
 │                                                                            │
 │   lore CLI command                                                         │
 │     │                                                                      │
 │     ├─(1) reconcile ──► fingerprint .lore/data ──changed?──► import files  │
 │     │                                                         into lore.db │
 │     │                                                                      │
 │     ├─(2) run command (query / search / add / edit) against lore.db        │
 │     │                                                                      │
 │     └─(3) on write: DB row + dirty mark (one tx) ──► exported at the end   │
 │                                                                            │
 │   .lore/data/**.json   ◄── committed, the source of truth                  │
 │   .lore/lore.db        ◄── gitignored, derived cache + local-only tables   │
 │                                                                            │
 └──────────────┬─────────────────────────────────────────────▲───────────────┘
                │ git commit / git push                       │ git pull / checkout / merge
                ▼                                             │
        ┌──────────────────────── GitHub repo ────────────────┴──┐
        │  refs/heads/main          .lore/data/  = lore main DB  │
        │  refs/heads/stag          .lore/data/  = stag's lore   │
        │  refs/heads/feature/xyz   .lore/data/  = xyz's lore    │
        └────────────────────────────────────────────────────────┘
```

Three rules hold the design together:

1. **Files are the truth, the DB is a cache.** Deleting `lore.db` loses nothing that is synced.
2. **Reconcile before every command.** Whatever git did since the last command (pull, checkout, merge, rebase, stash, revert), lore catches up first.
3. **Every write goes through the DB, and the DB remembers what is unexported.** A trigger marks the row dirty in the same transaction as the write; the end-of-command pass exports it. A crash before the export leaves the row dirty, and the next pass exports it (built this way instead of "file first" — see §0 Deltas).
4. **Any DB write is captured, whoever made it.** Triggers on synced tables record every change in an outbox that lore exports to files on the next command (§12), so `lore tui`, raw SQL and `sqlite3` cannot leave files stale.

## 5. On-disk layout

```filetree
your-repo/
  .gitattributes        (lore lines: eol=lf, merge=lore, linguist-generated)
  .lore/
    data/               (COMMITTED — source of truth)
      _meta.json        (format version, project id)
      project/
        prj_0192….json
      rules/
        rul_0192….json
      memories/
        mem_0192….json
      decisions/
      tasks/
      missions/
      tags/
        tag_<hash>.json   (deterministic id, see E2)
      entity_tags/
    LORE.md             (COMMITTED, generated; see E31)
    lore.db             (gitignored — cache + local-only tables)
    lore.db-wal         (gitignored)
    state/              (gitignored)
    backups/            (gitignored)
```

<!-- ds:block id=sync-datanames-nhh4krj2 -->
One directory per synced table (named exactly like the SQL table); one file per row; filename is exactly the row id plus `.json`. Hook blocks live in whatever hook directory git uses for the clone (`.git/hooks`, or a hook manager's `core.hooksPath`), not in a committed directory. No nesting by date or shard in v1 (10k files per directory is fine for git and every OS lore targets; revisit past 50k).

## 6. File format

```json
{
  "_table": "rules",
  "_v": 1,
  "archived_at": null,
  "body": "All /api routes validate a JWT; never session cookies.",
  "created_at": "2026-10-03T10:12:00.000000000Z",
  "created_by_actor_id": "act_0192…",
  "id": "rul_01928f3a7b2c4d5e8f9a0b1c2d3e4f5a",
  "project_id": "prj_0192…",
  "repo_id": null,
  "superseded_by_id": null,
  "title": "Use JWT for API auth",
  "updated_at": "2026-10-03T10:12:00.000000000Z"
}
```

<!-- ds:block id=sync-formatversion-h8e7vzn7 -->
<!-- ds:block id=sync-timelayout-ykcphmfb -->
**Canonical serialization rules** (byte-identical output for identical data, so git never sees noise):

| Rule | Why |
|---|---|
| Keys sorted lexicographically, one key per line, 2-space indent | Git's line merge can combine edits to different fields; stable diffs |
| `_table` and `_v` (format version) sort first via the `_` prefix | Importer can dispatch before parsing the rest |
| Timestamps UTC with exactly nine fractional digits | Round-trip stability, and string-comparable for the merge driver |
| Floats formatted with a fixed, shortest-round-trip formatter | Round-trip stability (E38) |
| Explicit `null` for unset optional fields | Distinguishes "unset" from "field unknown to this version" (E24) |
| LF line endings, trailing newline, UTF-8, no BOM | Cross-OS stability (E22) |
| Volatile fields excluded (see §7) | Reads must never produce diffs |
| Unknown fields preserved verbatim on rewrite | Mixed lore versions across a team must not drop data (E24) |

Write only when the canonical bytes differ from the file on disk. An edit that changes nothing semantically leaves the file untouched.

## 7. Table classification

<!-- ds:block id=sync-syncedtables-rgmtjeph -->
<!-- ds:block id=sync-localtables-dcpb69pj -->
<!-- ds:block id=sync-volatile-4t6jsc7v -->
Every ent schema table is in exactly one class. A unit test walks `dbent/schema` and fails if any table is unclassified, so a new table cannot silently skip the decision.

| Class | Meaning | Tables (proposed — confirm in review) |
|---|---|---|
| **synced** | Written to `.lore/data/`, follows branches | project, repo, actor (sanitized, E4), memory, rule, decision, hotfix, pattern, playbook, prompt, architecture_notes, cookbook_recipes, incidents, behaviours, plans, mission, task, tasklist, reminder, handoffs, tag, entity_tag, knowledge_ref, comment, commit_link, rule_verifier_ref, memory_code_ref, project_config, external_sources |
| **local** | Never leaves the machine | query_log, render_history, audit_log, sessions, runs, run_steps, assemble_runs, assemble_citations, compress_runs, learn_runs, learn_candidate, suggestions, drafts, bench_evals, bench_runs, bench_results, intervention_metric, activities_archive, identity_profiles, task_views, config, pii_patterns, mount_alias, schema_migration, knowledge_revisions (E5) |
| **derived** | Rebuilt from synced data or from the code tree | FTS5 virtual tables (`*_fts`), code_file, code_symbols, snapshot |

**Volatile columns inside synced tables** stay out of the file and live in a local overlay table `sync_local_overlay(entity_table, entity_id, column, value)`: `last_accessed_at`, `trust_score` decay updates, `actor.last_seen_at`, `confidence` (LLM-graded, re-gradable), any counter. If a column is ever both shared and churned, split it.

## 8. Flows

### 8.1 Write (any `add` / `edit` / `archive` / status change)

As built (see §0 Deltas for why this replaced "file first"):

```
lore rule add "Use JWT"
  │
  ├─ start-of-command pass (full): import what git changed
  ├─ the command writes the row through ent (natural-key rows get a deterministic id, E2)
  │     └─ trigger: (rules, rul_…) into _lore_sync_dirty, same transaction
  └─ end-of-command pass (dirty-only):
        ├─ acquire .lore/state/sync.lock
        ├─ canonical JSON from the row; secret scan (E33) — a refused row stays dirty and is reported
        ├─ write .lore/data/rules/rul_….json.tmp ─► rename     (atomic)
        ├─ record base (hash, bytes, stat); clear dirty
        └─ print: "lore sync: N file(s) in .lore/data not committed"
```

### 8.2 Reconcile (start of every command)

<!-- ds:block id=sync-racywindow-zrv7fwan -->
<!-- ds:block id=sync-drainrounds-2adqnp3a -->
As built — a stat cache instead of git tree hashes (see §0 Deltas):

```
for every .lore/data/<table>/<id>.json:
    (size, mtime) == recorded and outside the 2 s racy window  → skip      ← the common case
    else read + hash; equal to the recorded base hash           → refresh the stat
    else                                                        → file change
for every recorded file that is gone                            → file deletion
for every row in _lore_sync_dirty                               → DB change
apply §12.2 per row; repeat while merges mark more rows dirty (≤ 4 rounds)
```

Rebase, force-push, reset, stash and checkout need no special case: they only change files, and the cache sees the files.

`import(paths)`, under the sync lock, inside one transaction with foreign keys deferred:

1. Added or modified file → parse, upcast to current `_v`, upsert row, update FTS row.
2. Deleted file → delete row, delete FTS row; local-only rows that referenced it keep a dangling id (E27).
3. Unparseable file (raw conflict markers, bad JSON) → skip it, keep the old DB row, record it in `sync_errors`, print a loud warning (E12). A valid file whose markers sit inside one value (the merge driver's output, §9) is imported — the cleanly merged fields land and the text shows both versions — with the notice kept in `sync_errors` until a lore edit exports a settled file over it.
4. After the batch: check references (E8); report dangling ones, never abort.

### 8.3 Fresh clone

```
git clone …           → .lore/data/ present, no lore.db
lore <anything>       → no lore.db → create schema → full_import() → run command
                      → also: register merge driver + hooksPath if absent (§10)
```

### 8.4 Branch work → PR → main → teammates

```
Day 1  main: R1 R2
       Alice: git checkout -b feature/auth     Bob: git checkout -b feature/billing
Day 2  Alice: lore rule add "JWT"   → R3 file     Bob: lore memory add "stripe" → M1 file
       Alice: commit + push                    Bob: commit + push
Day 3  Alice's PR merges on GitHub             → main: R1 R2 R3   (plain git file add)
       Bob's PR merges on GitHub               → main: R1 R2 R3 M1 (different files, no conflict)
Day 4  Carol: git pull; lore search …          → reconcile imports R3, M1
```

### 8.5 Branch switch

```
git checkout feature/auth   → .lore/data/ now has feature/auth's files
lore task list              → reconcile: diff old HEAD tree vs new → rows of other
                              branches removed, this branch's rows added → answer
```

## 9. Merge semantics

**Default (no driver, e.g. GitHub merge button):** git's line merge on canonical JSON.

| Situation | Result |
|---|---|
| Different rows added / edited on each side | clean (different files) |
| Same row, both sides identical change | clean (git treats identical changes as one) |
| Same row, different fields | usually a conflict, because both sides also changed `updated_at` (adjacent lines) — resolve locally (driver) |
| Same row, same field | conflict |
| Row archived on one side, edited on other | conflict on `archived_at` / edited field lines — never a modify/delete conflict, because there are no hard deletes (E9) |

<!-- ds:block id=sync-strategyfor-ycedma28 -->
**`lore merge-driver %O %A %B %P` (local merges, rebases, pulls):** field-level 3-way merge per file.

| base → ours | base → theirs | result |
|---|---|---|
| unchanged | changed | take theirs |
| changed | unchanged | take ours |
| changed to X | changed to X | X |
| changed to X | changed to Y | **scalar enum / timestamp** (status, priority, archived_at): newest `updated_at` wins, warning printed. **free text** (title, body): real conflict — driver writes git-style conflict markers inside the string value and exits 1, so the human resolves it (with `lore <entity> edit`, or by editing the value). |
| `updated_at` | always | max(ours, theirs) |
| unknown fields | | same rules, preserved verbatim |

Text fields stay loud on purpose: silently picking one of two edited rule bodies is exactly the silent data loss this design exists to prevent.

## 10. Git integration (auto-configured, optional for correctness)

<!-- ds:block id=sync-gitattributes-xzr8sqyk -->
<!-- ds:block id=sync-mergedriver-config-bur8yta8 -->
Lore checks and repairs this on every command, idempotently, in the per-clone `.git/config` only:

```ini
[merge "lore"]
    name = lore field-level merge
    driver = lore merge-driver %O %A %B %P
```

```gitattributes
.lore/data/**/*.json  text eol=lf merge=lore linguist-generated=true
.lore/LORE.md         text eol=lf merge=lore-regen linguist-generated=true
```

**Hooks** — git never auto-runs hooks from a clone; each clone must opt in. Lore installs them on its first command in a clone (anyone who can create lore data has run lore, so the hook exists wherever data can be lost). As built, lore never changes `core.hooksPath`:

```
core.hooksPath unset   → chain lore's block into .git/hooks/<hook> (created if missing)
core.hooksPath = <dir> → (husky, lefthook, a committed .githooks) chain into THAT dir's scripts (E46)
existing script        → block inserted right after the shebang, so an early `exit 0` cannot skip it
```

The check runs at the end of every command but costs only a few `stat` calls: lore records a fingerprint of the git config file, the hook files and `.gitattributes`, and spawns git only when one of them changed.

<!-- ds:block id=sync-hooklines-3282rwuw -->
Hooks provided (all optional — correctness never depends on them):

- `pre-commit`: run a pass; refuse the commit when any lore file is invalid JSON, carries conflict markers (raw, or inside a JSON string value), or matches a credential pattern; otherwise stage `.lore/data/`. The hook first checks that the installed lore has `sync hook`: hook scripts may be committed (`.githooks`), and a teammate on an older lore must still be able to commit (they get an "upgrade it" note).
- `post-checkout` / `post-merge` / `post-rewrite`: `lore sync hook refresh` — imports immediately and re-renders `.lore/LORE.md` when rows arrived. Never fails the git operation.

## 11. Edge cases

Grouped by area. Each row is a case the implementation must handle and a test must pin.

### 11.1 Identity and collisions

| # | Case | Fix |
|---|---|---|
| E1 | Two developers each ran `lore init` before sharing → two `project` rows with different random ids; every row's `project_id` differs | `.lore/data/_meta.json` holds the project id and is committed. `lore init` in a repo with existing `.lore/data` adopts it instead of creating a new project. If two `_meta.json` histories meet in a merge (both inited in parallel): `lore doctor --fix` picks the older id, rewrites `project_id` in every file of the other, in one commit. |
| E2 | Natural-key uniques collide: tag `(project_id,name)`, repo `(project_id,mount_name)`, prompt `(project_id,name)`, project_config `(project_id,key)`, actor `stable_key` — two branches create tag "auth" with different UUIDs → import violates the unique index | Rows with a natural key get a **deterministic id**: `prefix + hex(sha256(project_id + "\x00" + natural_key))[:32]`. Both branches then write the same path with the same content → git merges identical adds cleanly. Pre-existing random-id duplicates: importer detects the unique violation, keeps the lexicographically smaller id, rewrites references (entity_tag etc.), logs it. |
| E3 | Renaming a deterministic-id row (tag rename) changes its id | Rename = create new + re-point references + archive old, done by the CLI in one operation; documented on the tag schema field. |
| E4 | `actor.stable_key` holds `human:<email>` → committing exposes emails | Committed actor file stores `stable_key_hash` (sha256) + `display_name` + `kind`; the raw key stays local. Note: git commit metadata already exposes author emails to repo readers, so this is defense in depth, not a new boundary. **As built:** `stable_key` is committed unchanged — see §0 Deltas. |
| E5 | `knowledge_revisions.revision_num` is a per-entity sequence: two branches both write revision N+1 → duplicate | Do not sync revisions. Git history of the entity file *is* the revision history (`git log -p -- .lore/data/rule/rul_….json`). Revisions table becomes local-only (fast local undo). |
| E6 | Any other sequence / counter column in a synced table | Grep gate in the classification test: integer columns named `*_num`, `seq`, `count`, `*_count` in synced tables fail the test unless explicitly whitelisted with a reason. |
| E7 | UUIDv7 collision | 74 random bits per id; probability negligible. Importer still treats "same id, different `_kind`" as a hard error. |

### 11.2 Merge and conflicts

| # | Case | Fix |
|---|---|---|
| E8 | Dangling references after a merge (task → mission that only existed on the other branch; `superseded_by_id` → archived row) | Import with foreign keys deferred / disabled; after import, a reference check lists dangling ids in `sync_errors`; commands render "(missing)" instead of failing; `lore doctor` reports them. |
| E9 | Deleted on one branch, edited on the other → git modify/delete conflict | No hard deletes of synced rows. Delete = set `archived_at`. Hard purge only via `lore gc --purge-archived --older-than` run deliberately on `main`. **As built:** `lore <kind> delete` still hard-deletes; the export removes the file and git shows a visible modify/delete conflict if another branch edited it — see §0 Deltas. |
| E10 | Same entity edited on two branches, merged on GitHub (no driver) | Usually conflicts (the `updated_at` line). GitHub shows a normal text conflict in a small JSON file; it can be resolved in the web editor, or locally where the driver auto-resolves non-text fields. Documented as expected. |
| E11 | Driver not installed in a clone, developer resolves by picking one side whole | Damage is limited to that one entity (one file), not the whole DB as with a binary file. Lore installs the driver on first command (§10), so the window is "clone + merge before ever running lore". |
| E12 | Conflict markers or invalid JSON committed | Importer skips the file, keeps the previous DB row, records `sync_errors`, prints the path on every command until fixed. The pre-commit hook rejects it when installed. |
| E13 | Merge driver hits a file of a newer `_v` than it understands | Exit 1 (leave conflict to git) with message "lore vX.Y needed to merge this file". Never rewrite a file it cannot fully understand. **As built:** falls back to `git merge-file` (standard markers) instead of exiting untouched. |
| E14 | Squash merge / rebase merge on GitHub | Non-issue: files ride the commits; the squash commit contains the same `.lore/data` changes. (This is the case that breaks the S3 design.) |

### 11.3 Git operations

| # | Case | Fix |
|---|---|---|
| E15 | Rebase, force-push, `reset`, gc'd history → `last_head` no longer exists | `git cat-file -e last_head` fails → full import. Measure and budget full import time (target: 10k files < 1 s). |
| E16 | Multiple worktrees | Each worktree has its own `.lore/lore.db` (path is per worktree, gitignored); `HEAD` is per worktree; works unchanged. Test it. |
| E17 | Not a git repo, or `git` not on PATH | Fallback fingerprint: walk `.lore/data`, hash (path, size, mtime_ns); import files whose stat changed. Sync still works locally; sharing is the user's problem. Print once that git is unavailable. |
| E18 | Lore's own uncommitted writes show up as dirty in `git status` | `sync_state(path, blob_hash)` records the git blob hash lore last wrote/imported per file; dirty files whose hash matches are skipped. |
| E19 | Shallow clone | Works: only the current tree is needed. `git diff last_head` may fail on a missing commit → full import (E15). |
| E20 | Sparse checkout excluding `.lore/data` | Detect via `git ls-files -- .lore/data` non-empty but directory missing → warn "lore data excluded by sparse checkout; lore is read-only here". |
| E21 | Detached HEAD, bisect, cherry-pick, revert | All just change the tree → reconcile handles them; revert of an "add rule" commit correctly removes the rule. |
| E22 | `git stash` with uncommitted lore files | Stash removes them → rows disappear on next command → `stash pop` brings them back. Correct by construction; test it. |
| E23 | Submodule or nested repo with its own `.lore` | Project root resolution already stops at the nearest `.lore`; fingerprint commands run with `-C <project_root>`. Test nested repo. |

### 11.4 Files, platforms, concurrency

| # | Case | Fix |
|---|---|---|
| E24 | Mixed lore versions on a team: older binary reads a newer file with extra fields | Unknown fields kept in a raw side map and written back verbatim. Newer `_v` major than supported → read-only for that file + "upgrade lore" hint. Never drop fields. |
| E25 | Schema migration renames / reshapes a field | Upcasters per `_v` applied on read; `lore sync migrate` rewrites all files to the new `_v` in one commit on main. Branches with old-format files keep importing via upcasters. |
| E26 | Windows `core.autocrlf` rewrites line endings → hashes differ, spurious diffs | `.gitattributes` forces `eol=lf` for lore paths; reader normalizes CRLF before parsing. |
| E27 | Local-only tables (query_log, runs) reference synced rows that vanish on branch switch | No foreign keys from local tables to synced tables (or `ON DELETE SET NULL`); readers tolerate missing targets. |
| E28 | Two lore processes at once (two agents in one repo) | `.lore/state/sync.lock` (flock) around reconcile and around each write; SQLite WAL already serialises DB writes. |
| E29 | Crash between file write and DB write | File-first ordering + atomic rename; next reconcile sees the file's hash ≠ `sync_state` and re-imports it. Crash mid-rename leaves only a `.tmp`, which reconcile deletes. |
| E30 | User hand-edits or moves a JSON file | Hand edit: imported like any change (`updated_at` unchanged is fine). File whose `id` ≠ filename: warn, trust the content's `id`, `doctor --fix` renames the file. **As built:** such a file is refused and reported on every pass; no auto-rename. |
| E31 | Case-insensitive filesystems (macOS, Windows) | Ids are lowercase hex, kinds lowercase ASCII → no case collisions. Assert in the serializer. |
| E32 | Very long bodies / large binary-ish content | Same limits as today's DB fields; serializer rejects bodies over a named max (constant with why-doc) with a clear error rather than committing multi-MB JSON. |

<!-- ds:block id=sync-locktimeout-rh9uf4cq -->
<!-- ds:block id=sync-maxfilesize-zk9m2syn -->
As built, the numbers behind E28 and E32 are named constants: a pass waits up to 15 seconds for another process's sync lock before giving up, and a row file larger than 4 MiB is refused on read (it is reported, never imported).

### 11.5 Security

| # | Case | Fix |
|---|---|---|
| E33 | A secret or PII in a memory body gets committed and pushed — git history is effectively permanent | Run the existing PII/secret scanner on the canonical JSON at write time, not only on `add` input (edits too). `--allow-secrets` still exists but now prints that the value will be committed to git. Pre-commit hook re-scans staged lore files. |
| E34 | Hostile JSON from a teammate's branch (path traversal in a field, oversized file, deeply nested JSON) | Importer derives paths only from validated ids (`ids.ValidateAny`), caps file size and nesting depth, never follows symlinks inside `.lore/data` (same posture as the existing `lore.db` symlink refusal in `saas/cmd/cli/memory.go:83`). |

### 11.6 Derived and generated files

| # | Case | Fix |
|---|---|---|
| E35 | `.lore/LORE.md` is generated and committed (not in `ensureGitignore`) → conflicts on nearly every merge | Keep it committed (agents read it via `@import` before any lore command runs on a fresh clone), give it `merge=lore-regen`: the driver regenerates it from the merged data instead of merging text. Reconcile also re-renders when synced data changed. On GitHub (no driver) a conflict here is resolved by taking either side; the next lore command re-renders. |
| E36 | FTS5 index after a large branch switch | Incremental delete+insert per changed row; if more than a named threshold of rows changed, drop and rebuild the FTS table instead (faster). |

### 11.7 Human workflow

| # | Case | Fix |
|---|---|---|
| E37 | Developer forgets to commit `.lore/data` changes | Every lore command prints "N lore files not committed" when dirty; agents read lore output and commit them; optional pre-commit hook auto-stages. **As built:** the reminder prints after commands that wrote row files; `lore sync status` / doctor always show the count. |
| E38 | Developer stages only specific files, leaves lore out, PR merges without its knowledge | Same warning keeps showing on their branch after merge; `lore doctor` flags "lore changes exist that are not on any pushed commit". |
| E39 | Knowledge that must apply to every branch immediately (urgent rule) | Out of scope for v1 sync; the path is a small PR to `main` that branches then merge. Later phase: `lore promote <id>` opens that PR. |
| E40 | Existing projects with a populated, never-shared `lore.db` per developer | Bootstrap export + adopt, see §12. |

## 12. Existing `lore.db` files and direct DB writes

Every repo using lore today has a `lore.db` and no `.lore/data/`. And not every write goes through lore's write path: `lore tui` opens the same `lore.db` through enttui (`saas/cmd/cli/tui.go`), 13 CLI files run raw SQL, people run `sqlite3` by hand, `lore backup restore` replaces the whole file, and teammates on an older lore binary keep writing to their DB with no files at all. So sync must work in **both directions**: files → DB (§8.2) and DB → files (this section).

### 12.1 Catching every DB write: triggers + outbox

Lore cannot rely on its own Go code paths to export, because other writers bypass them. Instead the DB itself records what changed:

```
synced table  ──AFTER INSERT / UPDATE OF <synced columns> / DELETE──►  sync_outbox(entity_table, entity_id, op, at)
   ▲                                                                     │
   │ any writer: lore CLI, lore tui, raw SQL, sqlite3, scripts            │ drained at the start of every
   │                                                                     ▼ lore command (before reconcile)
   └────────────────────────────── export: write canonical file if bytes differ
```

- Triggers are generated from the §7 classification registry, so a new synced table gets its trigger automatically and the classification test covers it.
- `UPDATE OF <synced columns>` lists only synced columns, so volatile columns (`last_accessed_at` etc.) never fill the outbox.
- No loop guard is needed: the importer's own writes also land in the outbox, but draining compares canonical bytes with the file on disk and writes nothing when they are equal.
- DELETE on a synced row writes a tombstone op; export turns it into `archived_at` on the file (E9), never a file deletion, unless the row was never exported (then nothing to do).
- The triggers live inside the `lore.db` file, so they keep firing even when the writer is a tool that knows nothing about sync.

### 12.2 Both sides changed the same row

Between two commands, a row can change in the DB (via tui) AND in its file (via `git pull`). The last-synced hash per row in `sync_state` is the merge base:

| DB vs base | File vs base | Action |
|---|---|---|
| same | same | nothing |
| changed | same | export DB → file |
| same | changed | import file → DB |
| changed | changed, equal to DB | nothing (record new base) |
| changed | changed, different | field-level 3-way with the §9 rules; on a text conflict the **file wins**, the DB version is saved in `sync_errors` as a conflict copy, and every command warns until `lore sync resolve <id>` is run |

Order per command: drain outbox (DB-side changes) and reconcile (file-side changes) run in one locked pass so this table is applied to the union of both change sets.

### 12.3 Bootstrap: first run of the new lore in an existing repo

```
lore <any command>   (new version, LORE_SYNC on)
  │
  ├─ .lore/data/ missing in the working tree?
  │     yes ─► BOOTSTRAP EXPORT
  │            • create triggers + sync_outbox + sync_state
  │            • write _meta.json (this DB's project id, format _v)
  │            • write one file per synced row (telemetry stays local)
  │            • PII scan every file; rows that fail are NOT exported and are listed
  │            • record every file's hash as the sync base
  │            • print: "lore: exported 1,284 rows to .lore/data — review and commit"
  │
  └─ .lore/data/ present (someone already bootstrapped and you pulled it)?
        and this lore.db has no sync_state yet
        ─► ADOPT
           • project id: rewrite this DB's project_id to the one in _meta.json
             (one transaction, every synced table; E1)
           • rows only in files      → import
           • rows only in the DB     → export (your unshared knowledge, now on your branch)
           • rows in both, differing → newer updated_at wins, the other version goes to
                                        sync_errors as a conflict copy, warning printed
           • natural-key duplicates  → resolve via deterministic ids (E2)
           • print a summary: imported N, exported M, conflicts K
```

Bootstrap never modifies existing DB rows except the project-id rewrite in ADOPT, and it backs up `lore.db` first via the existing `lore backup` path (`.lore/backups/<ts>.sqlite`), so it can always be undone.

### 12.4 Rolling out to a team that already uses lore

```
Step 1  One person, on main:  upgrade lore → run any command → BOOTSTRAP → commit .lore/data → push / PR
Step 2  Everyone else:        upgrade lore → git pull main → run any command → ADOPT
                              → their private knowledge is exported onto their current branch
                              → commit it with their next push; it reaches main when that PR merges
```

If two people bootstrap in parallel before either merges, both `_meta.json` files carry different project ids → `lore doctor --fix` (E1) unifies them after the merge. The recommended order above avoids that.

### 12.5 Other direct-write cases

| # | Case | Fix |
|---|---|---|
| E41 | `lore tui` edits a row | Trigger → outbox → exported on the next command (or immediately: tui calls the drain on exit). |
| E42 | Raw SQL inside lore's own code paths, or `sqlite3` by hand | Same triggers; nothing to remember per code path. |
| E43 | `lore backup restore` replaces `lore.db` | The restored DB has an old or no `sync_state`. Restore asks `--prefer db` (default, since restoring means "I want this DB back") or `--prefer files`. With `db`: export every differing row (the result is a reviewable git diff before commit). With `files`: full import over the restored DB. |
| E44 | `lore.db` deleted or corrupt | Full import from files rebuilds every synced row; only local-only telemetry is lost. This becomes the cheapest recovery tier, ahead of today's backup tiers. |
| E45 | A teammate still on an old lore binary keeps writing to their DB with no triggers | Their changes are invisible until they upgrade; on upgrade, ADOPT exports them (rows only in the DB) and conflict-copies any that diverged. `_meta.json` carries `min_lore_version`; future versions refuse to write below it. Rollout note: upgrade everyone in step 2 above. **As built:** no `min_lore_version` field; the per-file `_v` check gives the same protection. |
| E46 | Existing hook manager owns `core.hooksPath` (husky, lefthook, a repo's `.githooks` like sync_go) | Append lore's marked block to that directory's scripts; never repoint `core.hooksPath` (also referenced in §10). |
| E47 | Rows deleted on `main` (via archive) get resurrected by an old DB during ADOPT | Cannot happen for archived rows (file still exists with `archived_at`; DB row is older → file wins by `updated_at`). Can happen only after `lore gc --purge-archived` removed the file; purge therefore records purged ids in `.lore/data/_purged.json`, and ADOPT skips them. |
| E48 | Two developers independently wrote the same knowledge with different ids (both added "use JWT") | Not auto-merged (content similarity is a judgement call). `lore doctor --dupes` lists near-duplicates after ADOPT for a human or agent to merge with `lore <kind> merge <keep> <drop>`. |
| E49 | Mode B projects (`.lore/lore.toml` → shared DB) | Out of scope for v1 (Q4): bootstrap does not run when the project resolves via Mode B, and prints why once. |

## 13. Plan

All phases shipped; sync is on by default (`LORE_SYNC=0` turns it off). Per-phase evidence is in §0.

| Phase | Scope | Exit criteria |
|---|---|---|
| **P0 — spike** | Canonical serializer prototype; measure reconcile on 1k / 10k / 50k files (fingerprint + full import); confirm `git status` cost with `core.untrackedCache` | Numbers recorded in this doc; budgets set (per-command overhead target < 50 ms at 10k files) |
| **P1 — model** | Table classification registry + test (every table classified, E6 counter gate); canonical serializer + upcaster framework; unknown-field preservation; deterministic ids for natural-key tables (E2) | Round-trip test: DB row → file → DB row byte-identical for every synced kind; classification test fails when a new table is added unclassified (verified by breaking it) |
| **P2 — write path** | File-first atomic writes, `sync_state`, sync lock, PII scan on canonical JSON, "not committed" warning; triggers + `sync_outbox` generated from the registry and the drain step (§12.1) | Crash-injection test between file and DB write recovers on next command (E29); a raw `sqlite3` UPDATE and a tui edit both reach the file on the next command (E41, E42) |
| **P3 — reconcile** | Fingerprint, incremental import, full-import fallback, deferred-FK import, `sync_errors`, dangling-ref report, FTS incremental + rebuild threshold, non-git fallback (E17) | Two-clone integration harness passes: add / edit / archive / branch switch / merge / rebase / stash / revert / force-push (E15–E22) |
| **P4 — merge** | `lore merge-driver` (field-level 3-way, loud on text), `lore-regen` driver for `LORE.md`, `.gitattributes` writer | Harness merges with the driver AND with plain `git merge` (no driver, simulating GitHub) and asserts no silent loss in either |
| **P5 — git wiring** | Auto-config of merge driver + `core.hooksPath` with chaining into existing hook dirs (E46 husky / `.githooks`); optional hooks | Fresh-clone test: first lore command configures everything; existing `.githooks/pre-commit` (sync_go style) still runs after lore appends |
| **P6 — migration + default on** | BOOTSTRAP and ADOPT (§12.3) with pre-backup; both-sides-changed resolution (§12.2) and `lore sync resolve`; `backup restore --prefer` (E43); `_purged.json` (E47); `lore doctor` checks (E1, E8, E12, E30, E38, E48); `lore init` adopts existing `.lore/data`; rollout guide (§12.4); flip default | Harness: two clones with independent pre-existing `lore.db` files bootstrap + adopt, merge, and end with identical data and a clean doctor; old-binary DB adopted without loss |
| **P7 — later** | `lore task list --ref origin/feature/x` read-only view via `git show`; `lore promote`; `lore gc --purge-archived` | — |

**Test harness (built in P3, used by every later phase):** a Go test helper that creates a bare "origin" repo plus two or three clones in a temp dir, runs real lore commands and real git commands in each, and asserts on the resulting DB contents. Every edge case row above gets at least one scenario in it.

## 14. Open questions — resolved

The owner asked for the recommendations to be applied. As built: Q1 tasks follow branches, plus `lore sync peek <ref>` as the cross-branch view; Q2 loud conflicts for free text; Q3 actor keys committed as is (changed from the recommendation, see §0 Deltas); Q4 Mode B untouched; Q5 revisions stay local; Q6 `.lore/LORE.md` committed, merged with keep-ours and re-rendered from the merged data (verified byte-identical across clones); Q7 split as in `registry.go` (`snapshots`, `suggestions`, `handoffs`, `external_sources`, `reminders` sync; `task_views`, `learn_candidates`, `tech_doc_pages`, `trusted_plugins`, `memory_code_refs`, `drafts` stay local).

The original questions, for the record:

| # | Question | Recommendation |
|---|---|---|
| Q1 | Should tasks and missions follow branches (only on `main` after merge), or be visible across branches immediately? | Follow branches in v1 (consistent, zero infra); add the `--ref` read-only view in P7. |
| Q2 | Conflict policy for free-text fields when both sides edited: loud conflict (proposed) or last-writer-wins? | Loud. Silent LWW on prose is silent data loss. |
| Q3 | Commit `actor` emails or only hashes (E4)? | Hashes + display name. |
| Q4 | Mode B (shared DB across projects on one machine): keep as-is, or redefine as "this project reads another checkout's `.lore/data`"? | Keep as-is for v1; sync only applies to Mode A. Revisit after P6. |
| Q5 | Drop `knowledge_revisions` from sync and rely on git history (E5)? | Yes. |
| Q6 | Is `.lore/LORE.md` committed (proposed, E35) or gitignored and regenerated? | Committed, because a fresh clone's agent reads it before running lore. |
| Q7 | Final synced/local split in §7 — especially `snapshot`, `suggestions`, `handoffs`, `external_sources`, `reminder`. | Review row by row. |
