package engine

// List and its trash mode (DELETION 3.2, DQ10): a list reads live rows;
// a context set by --include-deleted / --deleted-only lets the trashed
// rows of the named tables through. Tombstones and purged ids never list.
//
//	<noun> list ─ metalist.Flags.Deleted(ctx, table) ─► WithDeleted
//	  └► Engine.List(src, table)
//	       mode hide    ─► readRows (trashed rows read as tombstones) ─► live
//	       mode include ─► rawRows (purged hidden) ─► live + trashed
//	       mode only    ─► rawRows ─► trashed only

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// DeletedMode is which rows a List returns with respect to the trash.
type DeletedMode uint8

const (
	// DeletedHide lists live rows only (the default; 3.2 one read filter).
	DeletedHide DeletedMode = iota
	// DeletedInclude lists live rows and the rows in the trash (--include-deleted).
	DeletedInclude
	// DeletedOnly lists only the rows in the trash (--deleted-only).
	DeletedOnly
)

// deletedKey is the context key of the list trash mode.
type deletedKey struct{}

// deletedScope is a mode bound to the tables it applies to: a list's own
// table only, so lookups made on the way (meta keys, projects) stay live.
type deletedScope struct {
	mode   DeletedMode
	tables []row.Table
}

// WithDeleted returns ctx whose Lists of tables use mode (DeletedHide
// returns ctx unchanged).
func WithDeleted(ctx context.Context, mode DeletedMode, tables ...row.Table) context.Context {
	if mode == DeletedHide || len(tables) == 0 {
		return ctx
	}
	return context.WithValue(ctx, deletedKey{}, deletedScope{mode: mode, tables: tables})
}

// deletedFor is the trash mode of a List of table under ctx.
func deletedFor(ctx context.Context, table row.Table) DeletedMode {
	s, ok := ctx.Value(deletedKey{}).(deletedScope)
	if !ok || !slices.Contains(s.tables, table) {
		return DeletedHide
	}
	return s.mode
}

// List returns the rows of table in one store, sorted by id: the live ones,
// plus or only the trashed ones when ctx says so (WithDeleted). It reads
// the backend (the cache port has no query yet); search and sql read the
// cache lens instead. A trashed row keeps its delete fields (trash.Is).
func (e *Engine) List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	a, err := e.attached(source)
	if err != nil {
		return nil, err
	}
	mode := deletedFor(ctx, table)
	var rows []store.Row
	if mode == DeletedHide {
		rows, _, err = e.readRows(ctx, a.Name, "")
	} else {
		rows, err = e.rawRows(ctx, a.Name)
	}
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, sr := range rows {
		if sr.Table != table || sr.Deleted {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			return nil, err
		}
		if mode == DeletedOnly && !trash.Is(r) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out, nil
}

// rawRows is readRows without the trash filter: purged ids still read as
// tombstones (DQ4), trashed rows keep their body and delete fields.
func (e *Engine) rawRows(ctx context.Context, name store.Source) ([]store.Row, error) {
	st := e.stores[name]
	rows, _, err := st.Backend.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	set, err := purgedSet(ctx, st)
	if err != nil {
		return nil, err
	}
	for i, sr := range rows {
		if rows[i], _, err = hidePurged(sr, set); err != nil {
			return nil, err
		}
	}
	return rows, nil
}
