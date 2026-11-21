package sqlite

// lens_tables.go — a lens narrowed to the tables its caller reads
// (LensOptions.Tables, devco D-813).
//
// Why: a full lens reads every column list of every table of every
// attached cache and creates a view pair per table plus the derived views
// (res__*, trash, time_spent, task_rollup) before its first statement:
// about a third of a warm `lore ask` (11 of ~50 tables read). A caller
// whose statements are fixed names the tables up front, and the lens
// builds only their `<table>` and `<table>_all` views. Masking is
// unchanged: the sensitive fields are read from every source as before
// (the kind and meta-key registries stay in the lens while it is built),
// and the masked views of the named tables are the same views a full
// lens creates.

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
)

// maskTables are the tables the mask and the kind arms read while the
// lens is built (lens_mask.go sensitive, lens_kindarms.go), kept in a
// narrowed lens until its views exist.
var maskTables = []string{schema.TableKinds, schema.TableKindFields, schema.TableMetaKeys, schema.TableResources}

// narrowed reports whether LensOptions.Tables named the tables to expose.
func (l *Lens) narrowed() bool { return len(l.tables) > 0 }

// wants reports whether the lens reads table n of its sources: every
// table for a full lens; for a narrowed one the named tables and the ones
// the mask needs while the lens is built.
func (l *Lens) wants(n string) bool {
	return !l.narrowed() || l.named(n) || slices.Contains(maskTables, n)
}

// named reports whether a narrowed lens's tables name n, case-insensitive
// as SQLite compares names (a query may write TASKS for tasks, D-837).
func (l *Lens) named(n string) bool {
	return slices.ContainsFunc(l.tables, func(t string) bool { return strings.EqualFold(t, n) })
}

// wantsKindArms reports whether a res_<kind> view is exposed, so the kind
// arms (one query per kind and source) are worth reading.
func (l *Lens) wantsKindArms() bool {
	return !l.narrowed() || slices.ContainsFunc(l.tables, func(n string) bool {
		return strings.HasPrefix(n, schema.KindTablePrefix)
	})
}

// keepNamed drops from tables every table a narrowed lens read only for
// its mask, so no view is created for it.
func (l *Lens) keepNamed(tables map[string][]srcTable) {
	if !l.narrowed() {
		return
	}
	for n := range tables {
		if !l.named(n) {
			delete(tables, n)
		}
	}
}

// derivedNames are the views and tables a full lens builds beyond each
// table's "<table>" and "<table>_all" pair (resource views, trash, time
// spans, rollup). A narrowed lens has none of them, so a statement that
// names one needs the full lens (QueryTables). TestDerivedNamesCoverFullLens
// fails when the full lens gains a view this list does not name.
var derivedNames = []string{ViewResKinds, ViewResAll, ViewResEdges, ViewResDangling, viewTrash, tblTimeSpans, tblTimeClients, viewTimeSpent, viewTaskRollup}

// QueryTables is the LensOptions.Tables of a lens opened for one SQL
// statement (`lore sql --query`, devco D-837): every word, quoted name and
// string literal of q (and the table of each "<table>_all" word), so the
// lens reads the column lists of, and builds views for, only the tables
// the statement can name instead of all ~50 of every attached cache.
// Words that are no table are passed over by the lens. nil (a full lens)
// when q names a derived view (derivedNames, res__*), SQLite's own schema
// tables or functions (sqlite_*), or a pragma (pragma_table_info,
// PRAGMA): those list or read tables the statement does not name.
func QueryTables(q string) []string {
	toks := nameTokens(q)
	out := make([]string, 0, len(toks)+1)
	for _, t := range toks {
		n := strings.ToLower(t)
		if slices.Contains(derivedNames, n) || strings.HasPrefix(n, resViewPrefix) ||
			strings.HasPrefix(n, "sqlite_") || strings.HasPrefix(n, "pragma") {
			return nil
		}
		out = append(out, n)
		if base, ok := strings.CutSuffix(n, lensViewSuffix); ok && base != "" {
			out = append(out, base)
		}
	}
	if len(out) == 0 {
		return nil // no statement: the full lens, as before
	}
	return out
}

// resViewPrefix starts every res__ view's name (ViewResKinds…): any of
// them needs the full lens.
const resViewPrefix = "res__"
