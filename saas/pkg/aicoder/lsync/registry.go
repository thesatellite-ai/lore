// Package lsync shares lore's knowledge between developers and git branches
// by mirroring every synced row as one canonical JSON file under
// `.lore/data/<table>/<id>.json` (design: LORE_SYNC_SPEC.md at the repo root).
//
// The files are the source of truth and are committed to git; `.lore/lore.db`
// becomes a cache that Reconcile keeps in step with them in BOTH directions:
//
//   - files → DB: whatever git did since the last command (pull, checkout,
//     merge, rebase, stash, revert) is imported before the command runs.
//   - DB → files: every write to a synced table, by ANY writer (the lore CLI,
//     `lore tui`, raw SQL, sqlite3 by hand), is captured by triggers into a
//     dirty set and exported to files.
//
// This file is the table registry: which tables sync, which columns inside
// them are machine-local, and what natural keys exist. It is derived from
// the generated ent migration metadata so a schema change is picked up
// automatically, and registry_test.go fails the build when a NEW table is
// added without an explicit synced/local decision.
package lsync

import (
	"fmt"
	"sort"

	"dbent/gen/ent/migrate"

	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
)

// idColumn is the primary-key column every lore table carries (AicoderBaseMixin).
const idColumn = "id"

// projectsTable holds the project identity row(s); _meta.json pins which
// one the repo uses (E1). Its row files live in .lore/data/<projectsTable>/.
const projectsTable = "projects"

// ProjectsTable is projectsTable for callers outside the package (doctor
// counts project files to flag parallel bootstraps).
const ProjectsTable = projectsTable

// updatedAtColumn is bumped by ent on every update, including updates that
// only touch volatile columns. The exporter ignores a change that is ONLY
// this column so reads that bump an access counter never churn files.
const updatedAtColumn = "updated_at"

// syncedTables is the closed set of tables whose rows travel through git.
// Each entry is shared team knowledge or work tracking. Adding a table here
// makes it sync; it must then NOT appear in localTables.
// ds:def id=sync-syncedtables-rgmtjeph owner=@khanakia stability=stable desc="tables that sync through git"
var syncedTables = map[string]string{
	"actors":             "who created/validated knowledge; ids referenced by every synced row",
	"architecture_notes": "knowledge",
	"behaviours":         "knowledge",
	"comments":           "discussion attached to synced rows",
	"commit_links":       "links knowledge to commits that are themselves in git",
	"cookbook_recipes":   "knowledge",
	"decisions":          "knowledge",
	"entity_tags":        "tag bindings of synced rows",
	"external_sources":   "project-level source registry",
	"handoffs":           "work tracking",
	"hotfixes":           "knowledge",
	"incidents":          "knowledge",
	"knowledge_refs":     "links between synced rows",
	"memories":           "knowledge",
	"missions":           "work tracking",
	"patterns":           "knowledge",
	"plans":              "work tracking",
	"playbooks":          "knowledge",
	"project_configs":    "per-project settings shared by the team",
	"projects":           "the project identity every row hangs off",
	"prompts":            "knowledge",
	"reminders":          "work tracking",
	"repos":              "repo scoping referenced by repo_id on knowledge rows",
	"rule_verifier_refs": "rule verification wiring",
	"rules":              "knowledge",
	"snapshots":          "knowledge",
	"suggestions":        "pending proposals the team reviews",
	"tags":               "classification shared by the team",
	"task_lists":         "work tracking",
	"tasks":              "work tracking",
	"taste_prefs":        "knowledge",
	"tech_docs":          "registry of documentation sources (pages are re-fetched locally)",
	"workflows":          "knowledge",
	"workspaces":         "knowledge",
}

// localTables never leave the machine. The value says why, so the decision
// is reviewable and nobody "fixes" it by syncing telemetry.
// ds:def id=sync-localtables-dcpb69pj owner=@khanakia stability=stable desc="tables that never leave the machine"
var localTables = map[string]string{
	"activity_archives":    "local archive of activity feed",
	"assemble_citations":   "telemetry of context assembly runs",
	"assemble_runs":        "telemetry of context assembly runs",
	"audit_logs":           "per-machine hash-chained audit trail; chains cannot merge",
	"bench_evals":          "local benchmarking tooling",
	"bench_results":        "local benchmarking output",
	"bench_runs":           "local benchmarking output",
	"code_files":           "derived from the working tree by the local indexer",
	"code_symbols":         "derived from the working tree by the local indexer",
	"compress_runs":        "telemetry",
	"config":               "DB-level settings (FTS fingerprint etc.) of this cache",
	"drafts":               "unpublished, personal work in progress",
	"identity_profiles":    "per-machine identity resolution",
	"intervention_metrics": "telemetry",
	"knowledge_revisions":  "per-entity sequence numbers collide across branches; git history of the row file is the shared history (E5)",
	"learn_candidates":     "unreviewed local proposals",
	"learn_runs":           "telemetry",
	"memory_code_refs":     "points at code_files, which are local",
	"mount_alias":          "per-machine mount renames",
	"pii_patterns":         "per-machine scanner configuration",
	"query_logs":           "telemetry",
	"render_histories":     "telemetry of local renders",
	"run_steps":            "telemetry; per-run sequence numbers",
	"runs":                 "telemetry",
	"schema_migrations":    "describes this DB file",
	"sessions":             "telemetry",
	"task_views":           "personal saved filters",
	"tech_doc_pages":       "fetched page bodies; large and re-fetchable",
	"trusted_plugins":      "trust decisions must be made per machine",
	"users":                "legacy auth table, unused by the lore CLI",
}

// volatileColumns are machine-local columns inside synced tables: access
// counters and caches that change on reads. They are not written to files,
// not compared, not overwritten on import, and excluded from the change
// triggers. Keyed by column name because the meaning is uniform across
// tables (LifecycleMixin et al.).
// ds:def id=sync-volatile-4t6jsc7v owner=@khanakia stability=stable desc="machine-local columns inside synced tables"
var volatileColumns = map[string]string{
	"last_accessed_at":   "bumped by retrieval (decay buffer)",
	"last_seen_at":       "bumped whenever an actor runs a command",
	"last_active_at":     "bumped whenever a project is used",
	"embedding_model_id": "derived from body by a local embedding model",
	"embedding_dim":      "derived from body by a local embedding model",
}

// Column describes one synced column.
type Column struct {
	Name     string
	Type     field.Type
	Nullable bool
	// Default is the schema default, used to fill NOT NULL columns that an
	// older file version does not carry. Typed `any` because it is copied
	// from ent's schema.Column.Default, which is; toSQLValue narrows it.
	Default any
}

// Table describes one synced table.
type Table struct {
	Name string
	// Columns are the synced columns in schema order; the first is "id".
	Columns []Column
	// Volatile are NOT NULL machine-local columns that still need a value
	// when a row is first inserted from a file.
	Volatile []Column
	// NaturalKeys lists every unique key other than the primary key, each as
	// its column names in schema order.
	NaturalKeys [][]string
}

// column returns the synced column named name.
func (t *Table) column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// Registry is the immutable set of synced tables.
type Registry struct {
	tables map[string]*Table
	names  []string
}

// Table returns the synced table named name.
func (r *Registry) Table(name string) (*Table, bool) {
	t, ok := r.tables[name]
	return t, ok
}

// Names returns synced table names, sorted.
func (r *Registry) Names() []string { return append([]string(nil), r.names...) }

// NewRegistry builds the registry from the generated ent schema.
//
// Unclassified tables are treated as local (never silently synced) and
// reported in the returned list so callers can warn; registry_test.go turns
// that into a build failure.
func NewRegistry() (*Registry, []string) {
	return buildRegistry(migrate.Tables)
}

func buildRegistry(all []*schema.Table) (*Registry, []string) {
	r := &Registry{tables: map[string]*Table{}}
	var unclassified []string
	for _, st := range all {
		if _, ok := syncedTables[st.Name]; !ok {
			if _, local := localTables[st.Name]; !local {
				unclassified = append(unclassified, st.Name)
			}
			continue
		}
		t := &Table{Name: st.Name}
		for _, c := range st.Columns {
			col := Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Default: c.Default}
			if _, vol := volatileColumns[c.Name]; vol || c.Type == field.TypeBytes {
				// Binary columns (embeddings) are derived caches: never
				// committed as base64 noise.
				if !c.Nullable {
					t.Volatile = append(t.Volatile, col)
				}
				continue
			}
			t.Columns = append(t.Columns, col)
		}
		for _, c := range st.Columns {
			if c.Unique && c.Name != idColumn {
				t.NaturalKeys = append(t.NaturalKeys, []string{c.Name})
			}
		}
		for _, idx := range st.Indexes {
			if !idx.Unique {
				continue
			}
			var cols []string
			for _, c := range idx.Columns {
				cols = append(cols, c.Name)
			}
			t.NaturalKeys = append(t.NaturalKeys, cols)
		}
		r.tables[t.Name] = t
		r.names = append(r.names, t.Name)
	}
	sort.Strings(r.names)
	sort.Strings(unclassified)
	return r, unclassified
}

// validate is a sanity guard: every synced table must start with the id
// column (file paths derive from it) and carry updated_at (merge ordering).
func (r *Registry) validate() error {
	for _, n := range r.names {
		t := r.tables[n]
		if len(t.Columns) == 0 || t.Columns[0].Name != idColumn {
			return fmt.Errorf("lsync: table %s has no leading id column", n)
		}
		if _, ok := t.column(updatedAtColumn); !ok {
			return fmt.Errorf("lsync: table %s has no updated_at column", n)
		}
	}
	return nil
}
