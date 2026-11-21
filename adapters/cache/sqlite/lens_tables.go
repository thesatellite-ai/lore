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
	return !l.narrowed() || slices.Contains(l.tables, n) || slices.Contains(maskTables, n)
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
		if !slices.Contains(l.tables, n) {
			delete(tables, n)
		}
	}
}
