package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// handEditRow rewrites the stored row id of table in the repo backend
// through edit, as a person editing the trashed file would (no engine
// checks).
func handEditRow(t *testing.T, f *fixture, table row.Table, id ids.AnyID, edit func(*row.Row)) {
	t.Helper()
	ctx := context.Background()
	b := f.repo.Open(store.OpenOptions{})
	sr, err := b.Get(ctx, table, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := sr.Decode()
	if err != nil {
		t.Fatal(err)
	}
	edit(&r)
	r.HLC = hlc.Next(sr.HLC, f.eng.wall(), testActor)
	next, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(ctx, []store.Change{{Row: next, Expect: sr.HLC}}); err != nil {
		t.Fatal(err)
	}
}

// sizeLimits is a policy set with resources.max_bytes and meta.max_bytes
// at the repo level.
func sizeLimits(t *testing.T, resources, metaBytes int64) policy.Set {
	t.Helper()
	s, err := policy.Resolve([]policy.Entry{
		{Level: policy.LevelRepo, Key: policy.KeyResourcesMaxBytes, Value: value.Int(resources)},
		{Level: policy.LevelRepo, Key: policy.KeyMetaMaxBytes, Value: value.Int(metaBytes)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestRestoreRecheckSizeLimits (DELETION 3.3 step 3, P3-DEL-1): a restore
// re-runs the write-time size limits on the row as it comes back. A
// trashed resource grown past resources.max_bytes is E_RESOURCE_INVALID,
// a trashed row whose metadata grew past meta.max_bytes is E_META_SIZE;
// on a refusal the whole batch stays in the trash. A row within the
// limits restores (the delete fields are not measured).
func TestRestoreRecheckSizeLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bigMeta := func(t *testing.T, r *row.Row) {
		t.Helper()
		kid, err := ids.New[ids.MetaKey]()
		if err != nil {
			t.Fatal(err)
		}
		j, err := meta.Entry{Name: "notes", Scope: "repo", V: canon.String(strings.Repeat("m", 400))}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		r.Meta = map[ids.ID[ids.MetaKey]]value.JSON{kid: j}
	}
	cases := []struct {
		name string
		// grow hand-edits the trashed row; nil leaves it as deleted.
		grow func(t *testing.T, f *fixture, res func(int) map[string]value.Value, id ids.AnyID)
		want errs.Code
	}{
		{name: "within the limits restores"},
		{name: "resource grown past resources.max_bytes", want: errs.ResourceInvalid,
			grow: func(t *testing.T, f *fixture, res func(int) map[string]value.Value, id ids.AnyID) {
				handEditRow(t, f, "resources", id, func(r *row.Row) { r.Fields["fields"] = res(2000)["fields"] })
			}},
		{name: "metadata grown past meta.max_bytes", want: errs.MetaSize,
			grow: func(t *testing.T, f *fixture, _ func(int) map[string]value.Value, id ids.AnyID) {
				handEditRow(t, f, "resources", id, func(r *row.Row) { bigMeta(t, r) })
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t, WithSettings(fixedSettings{set: sizeLimits(t, 1024, 256)}))
			res := resourceFixture(t, f)
			w, err := f.eng.Create(ctx, "", "resources", res(100))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.eng.Delete(ctx, Ref{ID: w.Row.ID}); err != nil {
				t.Fatal(err)
			}
			if c.grow != nil {
				c.grow(t, f, res, w.Row.ID)
			}
			_, err = f.eng.Restore(ctx, w.Row.ID.String())
			if c.want == "" {
				if err != nil {
					t.Fatalf("restore: %v", err)
				}
				_, got, gerr := f.eng.Get(ctx, Ref{ID: w.Row.ID})
				if gerr != nil {
					t.Fatal(gerr)
				}
				if _, deleted := trash.Read(got); deleted {
					t.Fatal("restored row still in the trash")
				}
				return
			}
			if !errs.Has(err, c.want) {
				t.Fatalf("restore: %v, want %s", err, c.want)
			}
			all, terr := f.eng.Trashed(ctx)
			if terr != nil || len(all) != 1 {
				t.Fatalf("trash after a refused restore = %d rows (%v), want the row still there", len(all), terr)
			}
		})
	}
}
