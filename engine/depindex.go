package engine

// depindex.go — one read of the stores for a many-root delete plan (owner
// report 2026-11-12: `lore store folder remove --purge` of a 1,000-row
// folder ran past five minutes: every planned row read and decoded every
// row of every store to find what points at it, then wrote on its own).
//
//	withDepIndex(ctx)  ─▶ dependents reads the stores once, indexes every
//	                      live row by each id its fields name, and answers
//	                      each lookup from the index (the same rows and
//	                      rules: points still decides)
//
// Only a plan uses it, before any write: the stores do not change under it.

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// depIndexKey is the context key of a plan's dependents index.
type depIndexKey struct{}

// depIndex is every live row of the attached stores, by each id its
// fields name.
type depIndex struct {
	once  sync.Once
	err   error
	byRef map[string][]depEntry
}

// depEntry is one indexed row and the store holding it.
type depEntry struct {
	source store.Source
	row    row.Row
}

// withDepIndex is ctx whose dependents lookups share one read.
func withDepIndex(ctx context.Context) context.Context {
	if _, ok := ctx.Value(depIndexKey{}).(*depIndex); ok {
		return ctx
	}
	return context.WithValue(ctx, depIndexKey{}, &depIndex{})
}

// indexedDependents answers dependents of (table, id) for the rules of
// byTable from ctx's index; ok is false when ctx carries none.
func (e *Engine) indexedDependents(ctx context.Context, byTable map[row.Table][]dependentRule, table row.Table, id ids.AnyID) ([]dependent, bool, error) {
	x, ok := ctx.Value(depIndexKey{}).(*depIndex)
	if !ok {
		return nil, false, nil
	}
	x.once.Do(func() { x.err = e.buildDepIndex(ctx, x) })
	if x.err != nil {
		return nil, true, x.err
	}
	var out []dependent
	for _, d := range x.byRef[id.String()] {
		for _, rule := range byTable[d.row.Table] {
			if points(d.row, rule, table, id) {
				out = append(out, dependent{source: d.source, row: d.row, rule: rule})
			}
		}
	}
	return out, true, nil
}

// buildDepIndex reads every attached store once (as dependents does) and
// indexes each live row under every id any of its fields names.
func (e *Engine) buildDepIndex(ctx context.Context, x *depIndex) error {
	x.byRef = map[string][]depEntry{}
	for _, name := range e.allStores() {
		rows, _, err := e.readRows(ctx, name, "")
		if err != nil {
			return err
		}
		for _, sr := range rows {
			if sr.Deleted {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				continue // an unreadable row file names nothing here; the row checks report it
			}
			seen := map[string]bool{}
			for _, v := range r.Fields {
				for _, s := range value.ItemStrings(v) {
					if !seen[s] {
						seen[s] = true
						x.byRef[s] = append(x.byRef[s], depEntry{name, r})
					}
				}
			}
		}
	}
	return nil
}
