package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// KeyLister is what FindByKey reads: the attached stores in precedence
// order and one store's live rows (an ops Env, the engine itself, wire's
// App all satisfy it).
type KeyLister interface {
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// Found is one row FindByKey matched and the store holding it.
type Found struct {
	Store store.Source
	Row   row.Row
}

// FindByKey returns the live rows of table whose fields equal key (field
// name → the value's string form), nearest store first. Natural ids are
// per store (L78 + L101: the target store's namespace is part of the
// hash), so a row is found by its key, never by recomputing its id: the
// same tag name in two stores is two rows, and the first one is the
// nearest.
func FindByKey(ctx context.Context, l KeyLister, table row.Table, key map[string]string) ([]Found, error) {
	var out []Found
	for _, src := range ReadStoresOf(l) {
		rows, err := l.List(ctx, src, table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.Deleted && keyMatches(r, key) {
				out = append(out, Found{Store: src, Row: r})
			}
		}
	}
	return out, nil
}

// keyMatches reports whether r holds every value of key.
func keyMatches(r row.Row, key map[string]string) bool {
	for f, want := range key {
		v, ok := r.Fields[f]
		if !ok || v == nil || v.String() != want {
			return false
		}
	}
	return true
}
