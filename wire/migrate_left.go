package wire

// migrate_left.go — what `lore migrate run` rewrites, and the files it
// leaves at `_v: 1`, named (owner item T-2).
//
// A trial migration of a 0.1.17 repo (`lore migrate run --offline
// --no-push`) left 12 files at `_v: 1`: 8 actors, a project, a repo, an
// entity_tags link and the data root's _meta.json, and said nothing.
// Actors, projects and repos are core tables like any other (the 0.1.17
// writer that once kept them at v1 is gone with saas), so the run now
// rewrites every synced table the core defines (FormatTables). A second
// trial (M-1) still left 169 entity_tags rows of one repo at `_v: 1`:
// 0.1.17's tag links are rewritten too (after the run folds them into
// tag_ids, offline included), so a migrated repo holds no v1 row. The
// rest is named with why it stays and what converts it:
//
//	v1 row of a FormatTables table, dry run   ──▶ rewritten by the run (not listed)
//	v1 row of a FormatTables table, real run  ──▶ listed: changed while the run ran
//	machine-local table row                   ──▶ listed: never a repo row; its
//	                                              rows are copied from lore.db
//	other table of the registry               ──▶ listed: not a table the run rewrites
//	any other table                           ──▶ listed: no table in this lore
//
// _meta.json and _purged.json are folder files, not rows: their "_v" is the
// file's own format (a _purged.json is `_v: 1` in this lore too), so they
// are never a leftover (M-2: every run printed "Left 1 file at _v: 1" for
// a _meta.json that records format 2).

import (
	"context"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsmigrate.LeftReporter = (*App)(nil)

// FormatTables is every table `lore migrate run` rewrites from `_v: 1` to
// `_v: 2`: CoreTables first (their order), then every other synced,
// non-machine-local table of the built-in registry by name (actors,
// projects, repos, …), then 0.1.17's entity_tags (no table of this lore,
// but its links are repo rows: M-1). Status counts the same tables as
// pending.
func FormatTables() []row.Table {
	out := CoreTables()
	reg, err := builtinRegistry()
	if err != nil {
		return out // the built-in registry is checked by its own tests
	}
	for _, def := range reg.Tables() {
		if def.Synced && !def.MachineLocal && def.Kind.IsZero() && !slices.Contains(out, def.Name) {
			out = append(out, def.Name)
		}
	}
	return append(out, entityTagsTable)
}

// Reasons a file stays at `_v: 1` (LeftV1).
const (
	leftWhyMachineLocal = "a machine-local table: its rows never live in a shared store; `lore migrate run` copies them from .lore/lore.db into this machine's store, and these files can be removed"
	leftWhyNotRewritten = "a table lore migrate run does not rewrite (not a synced table): its rows are read as they are; nothing converts them"
	leftWhyUnknown      = "no table of this name in this lore: nothing converts it (remove the files if no lore uses the table, or upgrade lore if a newer one wrote them)"
	leftWhyRaced        = "changed while the run ran (or could not be rewritten): run `lore migrate run` again"
)

// LeftV1 implements opsmigrate.LeftReporter: the v1 row files of the
// places run rewrote that it did not rewrite, grouped per store and table.
// Folder files (_meta.json, _purged.json) are never listed (M-2).
func (a *App) LeftV1(ctx context.Context, tables []row.Table, repoOnly, dryRun bool) ([]opsmigrate.Left, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	skip := map[store.Source]bool{}
	if !repoOnly {
		if skip, err = a.flattenedUnderDryRun(dryRun); err != nil {
			return nil, err
		}
	}
	var out []opsmigrate.Left
	names := st.eng.Stores()
	for i, b := range st.backends {
		if skip[names[i]] || (repoOnly && i > 0) {
			continue
		}
		rows, _, err := b.Read(ctx, "")
		if err != nil {
			return nil, err
		}
		byTable := map[row.Table][]string{}
		for _, sr := range rows {
			r, derr := sr.Decode()
			if derr != nil || r.SourceVersion >= row.FormatVersion {
				continue // an unreadable file is a read problem doctor names
			}
			if dryRun && slices.Contains(tables, sr.Table) {
				continue // the real run rewrites it
			}
			byTable[sr.Table] = append(byTable[sr.Table], sr.Table.String()+"/"+sr.ID.String())
		}
		for _, t := range slices.Sorted(maps.Keys(byTable)) {
			what := t.String()
			if i > 0 {
				what += " (store " + string(names[i]) + ")"
			}
			why := leftWhyUnknown
			switch def, ok := reg.Table(t); {
			case slices.Contains(tables, t):
				why = leftWhyRaced
			case ok && def.MachineLocal:
				why = leftWhyMachineLocal
			case ok:
				why = leftWhyNotRewritten
			}
			out = append(out, opsmigrate.Left{What: what, Table: t.String(), Files: byTable[t], Why: why})
		}
	}
	return out, nil
}
