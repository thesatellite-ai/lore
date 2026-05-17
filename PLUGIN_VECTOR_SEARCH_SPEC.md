---
title: Plugin store + vector/semantic search (CGO-free vec0)
status: accepted
---

# Plugin store + vector search — spec

<DocStatus state="review" owner="amank" updated="2026-05-17"></DocStatus>

Isolated plugin knowledge store with hybrid keyword + semantic search. Core `lore search` and `render` stay untouched. **No engine swap**: vectors come from the CGO-free `vec0` extension already shipping in the repo's `modernc.org/sqlite` driver (v1.50.1), enabled by one blank import. Embeddings computed by a **local, offline** model — no API key, no network after first model download. (libSQL was evaluated and rejected — see ADR-PLUGIN-1 superseded by ADR-PLUGIN-2.)

<Callout type="info" title="Scope boundary">

Plugins are a **separate namespace** behind `lore plugin …`. They do NOT feed core `lore search`, do NOT feed `render`, and do NOT participate in the render canary. This isolation is what makes a single JSON-backed table acceptable here when it would not be for the core 23 typed entities.

</Callout>

## Decision

<Callout type="danger" title="ADR-PLUGIN-1 is SUPERSEDED — do not implement libSQL">

The libSQL plan below was rejected by a spike (2026-05-17). `go-libsql` is **cgo-only**; this repo is deliberately `CGO_ENABLED=0` (pure-Go modernc, enforced in CI). The correct decision is **ADR-PLUGIN-2** further down: CGO-free `vec0` already shipping in the modernc driver the repo *already uses*. ADR-PLUGIN-1 is kept only as a record of the rejected path.

</Callout>

<ADR status="superseded" id="ADR-PLUGIN-1" date="2026-05-17" superseded-by="ADR-PLUGIN-2" title="libSQL + local embedder for isolated plugin store (REJECTED)">

### Context

Plugins need a low-ceremony place to persist arbitrary records and search them by keyword and by meaning. Core entities are ent-typed and feed deterministic `render`; plugin data must not regress that. At the time this was written it was *believed* plain SQLite (modernc) had no native vector index — that assumption was wrong (see ADR-PLUGIN-2).

### Decision

- Single DB. Engine = **libSQL** (`go-libsql`, embedded, SQLite-file-compatible). ent stays on the SQLite dialect, opened over the libSQL driver. Existing schema, migrations, and FTS5 keep working unchanged.
- One shared `plugin_record` table. `attrs` = JSONB source of truth. `search_text` = derived **values-only** flatten (canonical key order). `embedding` = `F32_BLOB(384)`.
- Search = FTS5 keyword + libSQL vector ANN, fused via Reciprocal Rank Fusion.
- Embeddings: **local offline model** — `cybertron` (pure-Go) running `all-MiniLM-L6-v2` (384-dim, cosine). Model auto-downloaded once, cached, then fully offline. `model_ver` stored per row so a model bump triggers reindex, not silent drift.

### Consequences

- New plugins need zero migration — pick a `plugin` string and write rows.
- libSQL is a superset of SQLite, so revert path = drop back to plain SQLite + FTS5 (vector deferred) if the engine swap destabilizes core.
- Adds a one-time model download + binary weight (accepted trade for offline guarantee).
- Rejected: Postgres (needs daemon), DuckDB (OLAP, weak transactional writes, no incremental FTS), Genji/Chai (maturity risk, no ent), API embeddings (breaks offline / determinism).
- **This ADR itself rejected** — libSQL forces cgo (see spike findings + ADR-PLUGIN-2).

</ADR>

<ADR status="accepted" id="ADR-PLUGIN-2" date="2026-05-17" supersedes="ADR-PLUGIN-1" title="CGO-free vec0 (modernc sqlite-vec) — no engine swap">

### Context

The repo is **deliberately `CGO_ENABLED=0`**: pure-Go `modernc.org/sqlite`, registered as `sqlite3` in `lace/db/db.go`, with that property enforced in `.github/workflows/ci.yml` (both `go build` and `go test` pin `CGO_ENABLED: "0"`) so the `lore` binary cross-compiles trivially as a single static file. A spike showed `tursodatabase/go-libsql` is cgo-only (`CGO_ENABLED=0` → "build constraints exclude all Go files"). `ncruces/go-sqlite3` (WASM, cgo-free) would work but is a *whole-binary engine swap* with a WASM perf tax on every core command. Then: `modernc.org/sqlite` **v1.47.0+** added a CGO-free transpile of `asg017/sqlite-vec`. The repo is **already on v1.50.1**.

### Decision

- **No engine swap. No new driver. No cgo.** Keep `modernc.org/sqlite` exactly as-is.
- Enable vectors with **one blank import**: `_ "modernc.org/sqlite/vec"` added beside the existing modernc import in `lace/db/db.go`.
- Vectors live in a `vec0` virtual table, queried with KNN `embedding MATCH ? ORDER BY distance LIMIT k` — same external-content / join-by-rowid pattern the codebase already uses for FTS5.
- `plugin_record` (ent) holds `attrs` (JSONB source of truth) + `search_text` (values-only canonical flatten) + metadata. `plugin_fts` (FTS5) and `plugin_vec` (`vec0`) are sibling virtual tables joined back by rowid.
- Embeddings: local offline `cybertron` (pure-Go) + `all-MiniLM-L6-v2` (384-dim), `model_ver` stamped per row.
- Search = FTS5 keyword + `vec0` KNN, fused via Reciprocal Rank Fusion.

### Consequences

- `openDB()` is **not touched** → the "blast radius" risk of ADR-PLUGIN-1 **disappears**. Core `render`/`search`/`task` run on the identical driver/code path.
- `CGO_ENABLED=0` preserved → CI, cross-compile, single-binary distribution unaffected. No `.github` change.
- The change is **additive** (one import + new tables) — the spike-gate collapses to "run the suite once to confirm the vec subpackage is inert," near-zero risk.
- Verified working: CGO-free `vec0` KNN on v1.50.1 returned correct nearest-neighbor ordering (see proof below).
- Rejected alternatives recorded in the driver matrix below.

</ADR>

### Spike findings (2026-05-17)

- `go-libsql`: `CGO_ENABLED=0 go build` → `build constraints exclude all Go files`; `CGO_ENABLED=1` → builds. Repo CI is `CGO_ENABLED=0`. **Hard incompatibility.**
- `modernc.org/sqlite v1.50.1` (already a repo dependency) + `_ "modernc.org/sqlite/vec"` → `CREATE VIRTUAL TABLE … USING vec0(...)` + KNN query worked under `CGO_ENABLED=0`:

```text
rowid=1 distance=0.000000      ← exact match first
rowid=3 distance=0.100000      ← nearest neighbor second
VEC OK — CGO-free vec0 KNN on modernc v1.50.1, driver unchanged
```

### Driver / engine matrix (why vec0 wins)

| Option | SQL vector | CGO-free | Engine swap | Verdict |
|---|---|---|---|---|
| `modernc.org/sqlite` + `vec` subpkg | ✅ `vec0` | ✅ | none (same driver) | **chosen** |
| `modernc.org/sqlite` (no vec import) | ✗ | ✅ | none | current; no vector |
| `tursodatabase/go-libsql` | ✅ native | ❌ cgo-only | full | rejected — breaks CGO=0 + CI |
| `mattn/go-sqlite3` + sqlite-vec | ✅ ext | ❌ cgo | full | rejected — cgo |
| `ncruces/go-sqlite3` + sqlite-vec | ✅ ext | ✅ (WASM) | full | rejected — whole-binary swap + WASM tax for a plugin-only feature |
| Postgres + pgvector | ✅ | ✅ | n/a | rejected — needs a daemon, kills local-first |
| App-layer HNSW/brute-force on modernc | ✅ (in Go) | ✅ | none | viable fallback; unnecessary now that `vec0` exists |

## Key-name noise — how it is solved

The search column indexes **values only, never keys**. The write hook walks `attrs`, emits leaf values in canonical key order, and never emits the key path.

<Diff>

```json before
{"severity":"must","body":"never commit secrets","tags":["auth","ci"]}
// search_text: "severity: must body: never commit secrets tags: auth ci"
// FTS tokens include severity/body/tags -> every row matches a key-name query
```

```text after
search_text: "must never commit secrets auth ci"
// FTS tokens: must never commit secrets auth ci
// key names absent -> zero key-name noise; values rank clean
```

</Diff>

Field-scoped queries (e.g. `severity = must`) are a **filter**, not full-text: they go through `json_extract(attrs,'$.severity')` + an indexed/generated column — added per-field only if a plugin actually needs it.

## Data model

```sql
CREATE TABLE plugin_record (
  id          TEXT PRIMARY KEY,         -- plg_<ulid>
  plugin      TEXT NOT NULL,            -- namespace, e.g. "bench", "notes"
  kind        TEXT NOT NULL,            -- plugin-defined record type
  project_id  TEXT,                     -- reuse core scoping mixin
  repo_id     TEXT,
  attrs       BLOB NOT NULL,            -- JSONB, source of truth
  search_text TEXT NOT NULL,            -- derived: values-only canonical flatten
  model_ver   TEXT NOT NULL,            -- embedder model version stamp
  created_at  INTEGER NOT NULL,
  archived_at INTEGER
);

CREATE INDEX plugin_record_ns ON plugin_record(plugin, kind, archived_at);

-- FTS5 external-content over search_text (DDL outside ent, like existing FTS5)
CREATE VIRTUAL TABLE plugin_fts USING fts5(
  search_text, content='plugin_record', content_rowid='rowid'
);

-- vec0 virtual table (CGO-free sqlite-vec via modernc/sqlite/vec).
-- Embeddings keyed by plugin_record.rowid; joined back like plugin_fts.
CREATE VIRTUAL TABLE plugin_vec USING vec0(
  embedding float[384]
);
-- KNN query shape:
--   SELECT rowid, distance FROM plugin_vec
--   WHERE embedding MATCH :query_vec ORDER BY distance LIMIT :k;
```

## Vector search pipeline

Vector search ≠ just a DB feature. It's a pipeline: text → embedding model → vector → `vec0` table → query embedding → KNN (`MATCH … ORDER BY distance`) → fuse with FTS5 keyword hits. The DB (`vec0`) gives storage + KNN. It does not give embeddings — that's the local model dependency (`cybertron`), the only part with real offline/determinism/setup consequences.

## Engine integration (CGO-free — no driver swap)

- **Driver:** UNCHANGED — `modernc.org/sqlite` (already in `lace/go.mod` at v1.50.1, registered as `sqlite3` in `lace/db/db.go`). `CGO_ENABLED=0` preserved.
- **Enable vectors:** add one blank import `_ "modernc.org/sqlite/vec"` beside the existing modernc import. That is the entire engine-side change.
- **ent:** unchanged — still SQLite dialect over the same `*sql.DB`. Existing schema/migrations/FTS5 untouched.
- **Vector DDL** (raw SQL, outside ent — same place as existing FTS5 DDL): `CREATE VIRTUAL TABLE plugin_vec USING vec0(embedding float[384])`, query with `embedding MATCH ? ORDER BY distance LIMIT k`.
- **Hybrid search:** FTS5 keyword + `vec0` KNN, fused via Reciprocal Rank Fusion → one ranked list. Pure-vector alone underperforms on exact terms/IDs.
- **No blast radius:** `openDB()` is not modified (only an added import elsewhere + new virtual tables). Core commands run the identical code path.

## Baseline (Search category) — propose in/defer/skip

| Item | Proposal | Reasoning |
|---|---|---|
| Add `_ "modernc.org/sqlite/vec"` import | in | the entire engine change; CGO-free, no driver swap |
| `plugin_vec` (vec0) table + KNN query | in | the actual ask |
| Embedding generation pipeline | in | mandatory — no embeddings, no vector search |
| Re-embed on row edit/delete (sync) | in | stale vectors = silently wrong results (CLAUDE.md: convention contracts fail silently → enforce via hook/trigger) |
| Hybrid FTS5 + vector (RRF fusion) | in | pure vector ranks IDs/exact terms badly; cheap to add |
| `lore plugin search <q> [--semantic] [--limit]` | in | the command surface |
| "no results" / empty-DB / model-unavailable states | in | Rule: graceful degradation, must not crash or silently return 0 |
| Pagination / `--limit` + offset | defer | top-k ANN covers v1; offset pagination later |
| Backfill embeddings for existing rows | defer | one-shot `lore plugin reindex` command, after core works |
| Core `lore search` gets vector too | skip (v1) | keep core deterministic; prove it on isolated plugins first |
| Query highlighting | skip | vector matches have no token span to highlight |

## Locked: local, offline embedder — concrete plan

**Embedder choice (local, offline, no cgo conflict, no API key)**

`cybertron` (pure-Go transformers, `github.com/nlpodyssey/cybertron`) running `all-MiniLM-L6-v2` → 384-dim vectors.

- Pure Go → no native ONNX runtime to ship; keeps the whole binary `CGO_ENABLED=0`.
- Model auto-downloaded once to `~/.lore/models/` (or `.lore/models/`), cached, then fully offline.
- 384-dim, cosine distance. Deterministic per model version (pin it; store `model_ver` next to each vector so a model bump triggers reindex, not silent drift).
- Graceful degrade: model missing + offline + not cached → `lore plugin search` falls back to FTS5-only with a `WARN: embed model unavailable, keyword-only` line (not a crash, not silent — CLAUDE.md skeleton-honesty rule).

**v1 scope (build this, defer the rest)**

1. Add `_ "modernc.org/sqlite/vec"` import in `lace/db/db.go` (next to existing modernc import). No driver swap, no ent change, `CGO_ENABLED=0` preserved.
2. `plugin_record` table (the isolated plugin store): `id, plugin, kind, project_id, repo_id, attrs JSONB, search_text TEXT, model_ver, created_at, archived_at`. Mixins reused.
3. Write hook (central, one place): on insert/update → canonical values-only flatten → `search_text`; embed `search_text` → upsert into `plugin_vec` by rowid; stamp `model_ver`. Delete → cascade `plugin_fts` + `plugin_vec` rows. This is the "enforce via hook, not convention" point.
4. DDL outside ent (raw SQL, like FTS5 already is): `plugin_fts` (FTS5 over `search_text`) + `plugin_vec` (`vec0(embedding float[384])`), both joined by rowid.
5. `lore plugin search <q> [--semantic] [--limit=N]`: keyword (FTS5) + vector (`vec0` KNN `MATCH … ORDER BY distance`) → RRF fusion → ranked. `--semantic` = vector-only; default = hybrid. Empty/no-results/model-unavailable states handled.
6. `lore plugin add|get|edit|rm|list` — grammar cloned from existing entity commands.

Deferred: `lore plugin reindex` (backfill/model-bump), offset pagination, vector on core `lore search`.

**Risks flagged**

- ~~Engine swap blast radius~~ — **eliminated** by ADR-PLUGIN-2. No driver swap; `openDB()` untouched; change is an additive import + new virtual tables. Gate collapses to: run the suite once to confirm the `vec` subpackage is inert.
- `vec0` is from `asg017/sqlite-vec`, **pre-v1** — expect possible breaking changes on modernc upgrades. Pin `modernc.org/sqlite`; treat a vec API change as a reindex event (same as a model bump).
- Binary size / first-run latency: cybertron + model download. One-time, cached. Acceptable for offline guarantee (confirmed tradeoff).
- Determinism: core `render` canary must stay byte-identical. Plugins don't feed render (v1), and `model_ver` is excluded from any rendered output → no canary churn.

**How to proceed (pick one)**

1. **Spike-confirm (recommended):** do step 1 only (add the import), run the full suite, confirm green (proves the vec subpackage is inert for core). Then build steps 2–6.
2. **Full v1 in one go:** build steps 1–6, review at the end.

## Integration — how to do it

<Steps>

<Step title="Enable vec, keep driver">

Add `_ "modernc.org/sqlite/vec"` beside the existing modernc import in `lace/db/db.go`. No driver swap, no ent change, `CGO_ENABLED=0` preserved. **Run the full existing test suite green** — confirms the vec subpackage is inert for core (near-zero risk, additive).

</Step>

<Step title="Add plugin_record + DDL">

ent schema `PluginRecord` (reuse audit + project-scope mixins). `plugin_fts` (FTS5) + `plugin_vec` (`vec0(embedding float[384])`) DDL run as raw SQL in the migration path, exactly where existing FTS5 DDL lives.

</Step>

<Step title="Central write hook">

One ent hook on `plugin_record` insert/update: canonical values-only flatten of `attrs` -> `search_text`; embed `search_text` via the local model -> `embedding`; stamp `model_ver`. Delete cascades FTS + vector rows. Convention-only sync is banned — the hook is the single enforcement point.

</Step>

<Step title="Local embedder">

`cybertron` + `all-MiniLM-L6-v2`. Model fetched once to `~/.lore/models/`, cached. If model missing AND offline AND uncached: degrade to FTS5-only and emit `WARN: embed model unavailable, keyword-only` — never crash, never silently return zero.

</Step>

<Step title="lore plugin commands">

`add | get | edit | rm | list | search`. Grammar cloned from existing entity commands. `lore plugin search <q> [--semantic] [--limit=N]`: default = hybrid (FTS5 + vector, RRF fusion); `--semantic` = vector-only.

</Step>

</Steps>

## Risks

<Callout type="tip" title="Engine swap blast radius — ELIMINATED">

ADR-PLUGIN-1's libSQL plan would have swapped the driver binary-wide. ADR-PLUGIN-2 does NOT: `modernc.org/sqlite` stays, vectors come from its CGO-free `vec` subpackage via one blank import. `openDB()` is untouched; core `render`/`search`/`task` run the identical code path. Gate reduced to: run the suite once to confirm the additive import is inert.

</Callout>

<Callout type="warning" title="sqlite-vec is pre-v1">

`vec0` comes from `asg017/sqlite-vec` (pre-v1, breaking changes expected). Pin `modernc.org/sqlite`; gate version bumps on the vec example test; treat a vec format change as a reindex event.

</Callout>

<Callout type="warning" title="Determinism">

`render` canary must stay byte-identical. Plugins do not feed render (v1) and `model_ver` is excluded from any rendered output, so embeddings cannot churn the canary. Re-verify if a future version lets plugins contribute to render.

</Callout>

<Callout type="warning" title="Model drift">

Embeddings are only comparable within one model version. `model_ver` is stored per row; a model bump must trigger `lore plugin reindex`, not a silent mixed-vector space.

</Callout>
