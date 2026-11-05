// Package sqlite is the SQLite cache adapter (STORE_SERVER_SPEC 4.4, 4.5,
// 7.4): the local, rebuildable copy of one or more stores that search, list,
// sql and render read. It implements store.Cache.
//
// Nothing here is the truth. Every envelope Apply receives is kept byte for
// byte in the internal table `_rows` (tombstones and rows of tables this
// binary does not know included, so a newer lore's rows survive), and every
// typed table is a projection of `_rows` that can be dropped and refilled at
// any time. That is how a schema change is handled: when a table definition
// changes (a kind gains or renames a field), its table is dropped and
// refilled from `_rows` on the next open or Apply, never ALTERed by guessing.
//
// # Typed tables
//
// One table per schema.TableDef, built at run time (no generated code):
//
//	id          TEXT NOT NULL      the row id
//	_source     TEXT NOT NULL      the store.Source that applied the row
//	PRIMARY KEY (_source, id): one file may hold several sources (the
//	combined cache, CFG3); an id two sources hold stays two rows and
//	ResolveID reports it as E_AMBIGUOUS_ID
//	_hlc        TEXT NOT NULL      the row's HLC
//	created_at, created_by, updated_at, updated_by   TEXT (audit, may be NULL)
//	<field>...  one column per FieldDef, affinity from the table below
//
// A tombstone removes its row from the typed tables (store.Cache contract);
// `_rows` remembers it, so an older copy can never come back: Apply skips a
// row whose HLC is older than the one already held.
//
// Column affinity per value.Kind:
//
//	kind                                             SQLite type  stored as
//	text date datetime duration ip cidr dns url
//	email ref                                        TEXT         the value's String()
//	number                                           NUMERIC      integer or real
//	bool                                             INTEGER      0 or 1
//	json list                                        TEXT         canonical JSON
//
// Datetimes are TEXT (DateTimeLayout sorts lexically), never a DATETIME
// declared type, so the driver hands back strings and nothing is converted
// on the way out.
//
// The cache never enforces anything (owner decision O2): no foreign keys,
// no NOT NULL on fields, no unique indexes. Rows may point into other
// stores or dangle by design (RES-OQ2), and uniqueness is the engine's
// check on write; a cache that refused a row the backend holds would just
// be wrong. Relations become indexes only: every ref column, every
// polymorphic (type, id) pair and both columns of a join table.
//
// A many-to-many relation (O11) names a list-of-ref field of the owning
// table; its join table (from_id, to_id, _source) is cache-only, refilled
// from that field on every upsert of the owning row.
//
// # Kinds
//
// Live `kinds` and `kind_fields` rows held in `_rows` become TableDefs via
// schema.KindDefFromRows, and each gets a typed table `res_<kind name>`
// filled from the `resources` rows of that kind (values keyed by fld_ id,
// so a field rename keeps its values), plus the stable view
// `res_<knd id>` (spec 16.6) for scripts that must survive a kind rename.
// A quarantined resource (a value breaking its field's type or allowed
// values, logic/resquar; RES 3.15) stays in `resources` but is left out of
// `res_<kind>` and of search; the allowed values are in the kind table's
// fingerprint, so changing them refills it.
//
// # Full text
//
// A def with FTS fields gets an FTS5 table `_fts_<table>` (columns `id`,
// `_source` unindexed, then `f_<field>` per FTS field: the prefix keeps
// fields named like FTS5's reserved `rank` / `rowid` legal), kept in step
// with the typed table on every upsert and delete.
//
// # References
//
// `row_refs` and `row_expanded` are exactly spec 4.5. Expansion is injected
// (Expander); a row_expanded row exists only for a field whose text holds at
// least one reference, so readers fall back to the column. Reexpand
// refreshes only the rows `ix_row_refs_ref` finds.
//
// # Lens
//
// OpenLens builds an in-memory connection that ATTACHes cache files
// read-only and creates TEMP VIEW `<table>_all` with a leading `source`
// column. modernc.org/sqlite is compiled with SQLITE_MAX_ATTACHED = 10, and
// sqlite3_limit can only lower a limit, never raise it past the compile-time
// maximum: raising SQLITE_LIMIT_ATTACHED to 125 (spec 7.4) is impossible
// with this driver, so a lens holds at most MaxLensSources (10) sources and
// refuses more with E_UNSUPPORTED. TestAttachLimit pins the real number.
// Past that limit the caller keeps one combined cache file for every
// source (Open, applying each store under its own Source) and reads it
// with OpenLensCombined, whose `source` column is each row's _source
// (CFG3 fallback, O10).
//
// # Metadata
//
// rowmeta.go (METADATA 3.16, 3.17, 4): the view row_meta over `_rows`
// lists every metadata value of every live, untrashed row (a presence row
// item_idx -1 with the whole value, then one row per scalar, list element
// and object field). Each FTS table gets a column f_meta (weight 2) with
// the row's non-sensitive values; a meta_keys row applied re-indexes the
// rows using that key. A masked lens shows row_meta value columns of a
// sensitive entry or key as MaskedValue.
//
// # Time spent
//
// The lens also builds TEMP TABLE task_time_spans and TEMP VIEW time_spent
// (timespans.go, TIME_TRACKING_DESIGN 7.4 and 9) from the tasks' time
// fields at open: tasks_all → spans (open ones end now) → counted seconds
// per worker and per owner → view joined to tasks, projects, actors.
//
// # Task tree
//
// tasktree.go (TASK_SYSTEM_DESIGN 23): the closure table task_tree
// (ancestor_id, descendant_id, depth; self pairs at 0), rebuilt from the
// live, untrashed tasks in `_rows` through logic/tasktree whenever a tasks
// row is applied, on Rebuild and on first open of an older file; a lens
// reads it as task_tree_all.
// rollup.go: the lens view task_rollup (6.6 numbers per task: children,
// leaves, estimates, subtree time, checklist) over tasks_all, task_tree_all
// and task_time_spans, computed at read time.
//
// Layer L3: imports model, schema, logic and store, never the engine.
package sqlite
