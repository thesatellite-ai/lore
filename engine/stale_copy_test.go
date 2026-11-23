package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// putCopy writes one version of memory id (created at created, body,
// version at) straight into store n's backend, as `lore store adopt` or a
// copied row file leaves it.
func (w *f15World) putCopy(t *testing.T, n store.Source, id ids.AnyID, created, at time.Time, body string) {
	t.Helper()
	r := row.Row{Table: schema.TableMemories, ID: id, HLC: hlc.Next(hlc.HLC{}, at, testActor), CreatedAt: created, CreatedBy: testActor,
		UpdatedAt: at, UpdatedBy: testActor, Fields: map[string]value.Value{"body": txt(body)}}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.data[n].Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
}

// listedBodies is the body of each listed row of id, by store, through
// List and ListIDs of every attached store.
func listedBodies(t *testing.T, ctx context.Context, w *f15World, id ids.AnyID) (list, byID map[store.Source]string) {
	t.Helper()
	list, byID = map[store.Source]string{}, map[store.Source]string{}
	for _, n := range []store.Source{store.SourceRepo, agency, personal} {
		rs, err := w.eng.List(ctx, n, schema.TableMemories)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.ID == id {
				list[n] = r.Fields["body"].String()
			}
		}
		rs, err = w.eng.ListIDs(ctx, n, schema.TableMemories, []ids.AnyID{id})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			byID[n] = r.Fields["body"].String()
		}
	}
	return list, byID
}

// TestListShowsOneCopyOfARowTwoStoresHold pins devco:D-929 (bug hunt
// H-187): a row live in two stores (store adopt keeps ids) is listed once,
// at its newest copy, by List and ListIDs (search reads hits through
// ListIDs), so a word only the stale copy holds finds nothing. Two rows
// that only share an id (another created_at: a natural id an older lore
// derived) both stay, equal versions keep the nearest store's copy, and
// WithEveryCopy (lint, bulk moves) sees both copies.
func TestListShowsOneCopyOfARowTwoStoresHold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	t.Run("newest copy listed", func(t *testing.T) {
		w := newF15World(t, false)
		id := mustID(t, ids.PrefixMemory)
		w.putCopy(t, store.SourceRepo, id, created, created.Add(time.Hour), "drains every hourly batch")
		w.putCopy(t, agency, id, created, created, "drains every ten minutes")
		list, byID := listedBodies(t, ctx, w, id)
		for name, got := range map[string]map[store.Source]string{"List": list, "ListIDs": byID} {
			if len(got) != 1 || got[store.SourceRepo] != "drains every hourly batch" {
				t.Errorf("%s = %v, want only the edited copy in repo", name, got)
			}
		}
		list, byID = listedBodies(t, WithEveryCopy(ctx), w, id)
		if len(list) != 2 || len(byID) != 2 {
			t.Errorf("WithEveryCopy: List %v ListIDs %v, want both copies", list, byID)
		}
	})
	t.Run("the newer copy in the farther store", func(t *testing.T) {
		w := newF15World(t, false)
		id := mustID(t, ids.PrefixMemory)
		w.putCopy(t, store.SourceRepo, id, created, created, "old words")
		w.putCopy(t, agency, id, created, created.Add(time.Hour), "new words")
		list, _ := listedBodies(t, ctx, w, id)
		if len(list) != 1 || list[agency] != "new words" {
			t.Errorf("List = %v, want only agency's newer copy", list)
		}
	})
	t.Run("equal versions keep the nearest", func(t *testing.T) {
		w := newF15World(t, false)
		id := mustID(t, ids.PrefixMemory)
		w.putCopy(t, store.SourceRepo, id, created, created, "same")
		w.putCopy(t, agency, id, created, created, "same")
		list, byID := listedBodies(t, ctx, w, id)
		if len(list) != 1 || list[store.SourceRepo] == "" || len(byID) != 1 || byID[store.SourceRepo] == "" {
			t.Errorf("List %v ListIDs %v, want the repo copy once", list, byID)
		}
	})
	t.Run("two rows sharing an id stay", func(t *testing.T) {
		w := newF15World(t, false)
		id := mustID(t, ids.PrefixMemory)
		w.putCopy(t, store.SourceRepo, id, created, created.Add(time.Hour), "one row")
		w.putCopy(t, agency, id, created.Add(time.Minute), created, "another row")
		list, _ := listedBodies(t, ctx, w, id)
		if len(list) != 2 {
			t.Errorf("List = %v, want both rows (different created_at)", list)
		}
	})
}
