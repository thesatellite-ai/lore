package work

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// twoStores is an Env holding a few rows in each of two stores, read in the
// store order given.
type twoStores struct {
	Env
	order []store.Source
	rows  map[store.Source][]row.Row
}

func (e twoStores) Stores() []store.Source { return e.order }

func (e twoStores) List(_ context.Context, src store.Source, _ row.Table) ([]row.Row, error) {
	return slices.Clone(e.rows[src]), nil
}

// TestListAllOneOrderWhateverStore: rows interleaved across two stores list
// in one order (by id) whichever store is read first, and a store's own
// read order does not matter either (a task list showed an open instance
// and a done one of a series in either order, run to run).
func TestListAllOneOrderWhateverStore(t *testing.T) {
	mk := func(id string) row.Row {
		parsed, err := ids.ParseAny(id)
		if err != nil {
			t.Fatal(err)
		}
		return row.Row{Table: schema.TableTasks, ID: parsed}
	}
	a, b := store.Source("team"), store.SourceRepo
	t1, t2, t3, t4 := "tsk_01a1a5aca7f57659be7320c5f4b70011", "tsk_01a1a5aca7f57659be7320c5f4b70022", "tsk_01a1a5aca7f57659be7320c5f4b70033", "tsk_01a1a5aca7f57659be7320c5f4b70044"
	rows := map[store.Source][]row.Row{a: {mk(t3), mk(t1)}, b: {mk(t4), mk(t2)}}
	want := []string{t1, t2, t3, t4}
	for _, order := range [][]store.Source{{a, b}, {b, a}} {
		got, err := ListAll(context.Background(), twoStores{order: order, rows: rows}, schema.TableTasks, "")
		if err != nil {
			t.Fatal(err)
		}
		var gotIDs []string
		for _, s := range got {
			gotIDs = append(gotIDs, s.Row.ID.String())
		}
		if !slices.Equal(gotIDs, want) {
			t.Errorf("stores read %v: rows listed %v, want %v", order, gotIDs, want)
		}
	}
	// one id in both stores: both copies, the first store read first
	dup := map[store.Source][]row.Row{a: {mk(t1)}, b: {mk(t1)}}
	got, err := ListAll(context.Background(), twoStores{order: []store.Source{b, a}, rows: dup}, schema.TableTasks, "")
	if err != nil || len(got) != 2 || got[0].Source != b || got[1].Source != a {
		t.Errorf("one id in two stores: %+v %v, want the repo copy then the team copy", got, err)
	}
}
