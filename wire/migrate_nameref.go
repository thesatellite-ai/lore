package wire

// migrate_nameref.go — every conversion of old-format rows (migrate run's
// format rewrite and tag fold, store adopt, the Mode B import) resolves a
// ref field that holds a name instead of an id (devco D-766, finding
// H-25: a task with tasklist_id "Backlog"): to the id of the one row of
// that name, else cleared, each with a warning line (engine/nameref.go).

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// nameRefTargets is the tables a ref field of tables points at: the only
// rows a name ref can resolve to, so only they are decoded for the index.
func nameRefTargets(reg *schema.Registry, tables []row.Table) map[row.Table]bool {
	out := map[row.Table]bool{}
	for _, t := range tables {
		def, ok := reg.Table(t)
		if !ok {
			continue
		}
		for _, f := range def.Fields {
			if f.Kind == value.KindRef && f.RefTable != "" {
				out[f.RefTable] = true
			}
		}
	}
	return out
}

// nameRefIndexOf indexes the live rows of srs that a ref of tables can
// point at (a row that does not decode is skipped: its own rewrite
// reports it).
func nameRefIndexOf(reg *schema.Registry, srs []store.Row, tables []row.Table) engine.NameRefIndex {
	targets := nameRefTargets(reg, tables)
	var rows []row.Row
	for _, sr := range srs {
		if sr.Deleted || !targets[sr.Table] {
			continue
		}
		if r, err := sr.Decode(); err == nil {
			rows = append(rows, r)
		}
	}
	return engine.NewNameRefIndex(rows)
}

// resolveNameRefs is engine.ResolveNameRefs on r, each fix said as a
// warning line under ctx (opsmigrate.Warn: migrate run prints them with
// its steps, store adopt as its refs lines).
func resolveNameRefs(ctx context.Context, reg *schema.Registry, x engine.NameRefIndex, r row.Row) row.Row {
	r, fixes := engine.ResolveNameRefs(reg, x, r)
	for _, f := range fixes {
		opsmigrate.Warn(ctx, f.Line())
	}
	return r
}
