package engine

// More trash reads (DELETION 3.5, 3.7, 4.5):
//
//	TrashStats ─► Trashed + raw Backend.Read per store (tombstones) ─► counts
//	Overruled  ─► raw Backend.Read: alive rows carrying delete_overruled (3.7)

import (
	"bytes"
	"context"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// overruledKey is the bytes a row file carrying delete_overruled holds.
var overruledKey = []byte(`"` + trash.FieldDeleteOverruled + `"`)

// TrashStats is `lore trash stats` (DELETION 4.5): counts of the rows in
// the trash per table, per store and per deleter, how many are past their
// store's trash.retention_days, and the tombstones per store (kept
// forever, DQ4).
type TrashStats struct {
	Rows       int
	Dependents int
	ByTable    map[string]int
	ByStore    map[string]int
	ByDeleter  map[string]int
	Expired    int
	Tombstones map[string]int
}

// TrashStats counts the trash of every attached store.
func (e *Engine) TrashStats(ctx context.Context) (TrashStats, error) {
	all, err := e.Trashed(ctx)
	if err != nil {
		return TrashStats{}, err
	}
	out := TrashStats{ByTable: map[string]int{}, ByStore: map[string]int{}, ByDeleter: map[string]int{}, Tombstones: map[string]int{}}
	now := e.clock.Now(hlc.HLC{}).Wall
	days := map[store.Source]int64{}
	for _, t := range all {
		out.Rows++
		if t.Dependent {
			out.Dependents++
		}
		out.ByTable[t.Row.Table.String()]++
		out.ByStore[string(t.Store)]++
		out.ByDeleter[t.Info.By]++
		d, ok := days[t.Store]
		if !ok {
			if set, _, perr := e.settings.Policy(ctx, t.Store); perr == nil {
				d, _ = set.Limit(policy.KeyTrashRetentionDays)
			}
			days[t.Store] = d
		}
		if trash.Expired(t.Info.At, now, d) {
			out.Expired++
		}
	}
	err = e.eachStoreOnce(func(name store.Source, st Attached) error {
		rows, _, rerr := readAll(ctx, st)
		if rerr != nil {
			return rerr
		}
		for _, sr := range rows {
			if sr.Deleted {
				out.Tombstones[string(name)]++
			}
		}
		return nil
	})
	return out, err
}

// Overruled is one alive row a merge brought back from a delete because the
// other side edited it (DELETION 3.7): the row and its delete_overruled
// object as stored (JSON text).
type Overruled struct {
	Store store.Source
	Row   row.Row
	Note  string
}

// Overruled lists the alive rows carrying delete_overruled (`lore trash
// --overruled`); deleting such a row again clears the field (trash.Mark).
func (e *Engine) Overruled(ctx context.Context) ([]Overruled, error) {
	var out []Overruled
	err := e.eachStoreOnce(func(name store.Source, st Attached) error {
		set, err := purgedSet(ctx, st)
		if err != nil {
			return err
		}
		rows, _, err := readAll(ctx, st)
		if err != nil {
			return err
		}
		for _, sr := range rows {
			if sr.Deleted || set[sr.ID.String()] || !bytes.Contains(sr.Doc, overruledKey) {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				return derr
			}
			v, ok := r.Fields[trash.FieldDeleteOverruled]
			if trash.Is(r) || !ok || v == nil || v.Kind() == value.KindNull {
				continue
			}
			out = append(out, Overruled{Store: name, Row: r, Note: v.String()})
		}
		return nil
	})
	return out, err
}

// eachStoreOnce calls fn for every attached store in precedence order, then
// the machine store when attached (P3-LOCAL-1: a machine-local row is in the
// trash, archived or overruled by the same rules as any other), once per
// place (a place attached under two names is read once, as Trashed does).
func (e *Engine) eachStoreOnce(fn func(store.Source, Attached) error) error {
	seen := map[string]bool{}
	for _, name := range e.allStores() {
		st := e.stores[name]
		if st.Location != "" {
			if seen[st.Location] {
				continue
			}
			seen[st.Location] = true
		}
		if err := fn(name, st); err != nil {
			return err
		}
	}
	return nil
}
