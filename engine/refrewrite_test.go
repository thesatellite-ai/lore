package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refList is a list-of-refs value of ids.
func refList(t *testing.T, list ...ids.AnyID) value.Value {
	t.Helper()
	items := make([]value.Value, 0, len(list))
	for _, id := range list {
		items = append(items, mustRef(t, id))
	}
	l, err := value.ListOf(value.KindRef, items...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestMergeRowsRepointsEveryReferrer (DELETION 3.12 merge-rows): a ref
// list holding both ids keeps one, a list holding only drop gets keep, an
// id inside text is re-pointed; drop goes to the trash with "merged into
// <keep>" in one merge batch, restorable by it; a re-run is E_IN_TRASH and
// nothing points at drop any more. A dry run writes nothing.
func TestMergeRowsRepointsEveryReferrer(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	mkTag := func(name string) ids.AnyID {
		w, err := f.eng.Create(ctx, "", schema.TableTags, map[string]value.Value{"project_id": mustRef(t, f.project), "name": txt(name)})
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID
	}
	keep, drop := mkTag("deploy"), mkTag("deploys")
	mkMem := func(body string, tags ...ids.AnyID) ids.AnyID {
		fields := f.memFields(body)
		if len(tags) > 0 {
			fields[schema.FieldTagIDs] = refList(t, tags...)
		}
		w, err := f.eng.Create(ctx, "", schema.TableMemories, fields)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID
	}
	both := mkMem("both tags", drop, keep)
	only := mkMem("drop tag only", drop)
	text := mkMem("see tag " + drop.String() + " for details")
	other := mkMem("unrelated", keep)

	dry, err := f.eng.MergeRows(ctx, nil, schema.TableTags, keep, drop, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []ids.AnyID
	for _, r := range dry.Referrers {
		got = append(got, r.ID)
	}
	for _, want := range []ids.AnyID{both, only, text} {
		if !slices.Contains(got, want) {
			t.Fatalf("dry run referrers %v miss %s", got, want)
		}
	}
	if slices.Contains(got, other) || len(got) != 3 || !dry.Batch.IsZero() {
		t.Fatalf("dry run = %+v", dry)
	}
	if _, r, _ := f.eng.Get(ctx, Ref{ID: drop}); trash.Is(r) {
		t.Fatal("a dry run trashed drop")
	}

	res, err := f.eng.MergeRows(ctx, nil, schema.TableTags, keep, drop, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Batch.IsZero() || len(res.Referrers) != 3 {
		t.Fatalf("merge = %+v", res)
	}
	tagsOf := func(id ids.AnyID) []string {
		_, r, gerr := f.eng.Get(ctx, Ref{ID: id})
		if gerr != nil {
			t.Fatal(gerr)
		}
		return value.ItemStrings(r.Fields[schema.FieldTagIDs])
	}
	if g := tagsOf(both); !slices.Equal(g, []string{keep.String()}) {
		t.Fatalf("both = %v, want only keep", g)
	}
	if g := tagsOf(only); !slices.Equal(g, []string{keep.String()}) {
		t.Fatalf("only = %v, want keep", g)
	}
	if _, r, _ := f.eng.Get(ctx, Ref{ID: text}); !strings.Contains(r.Fields["body"].String(), keep.String()) || strings.Contains(r.Fields["body"].String(), drop.String()) {
		t.Fatalf("text body = %q", r.Fields["body"].String())
	}
	_, dr, err := f.eng.Get(ctx, Ref{ID: drop})
	if err != nil {
		t.Fatal(err)
	}
	all, err := f.eng.Trashed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var in trash.Info
	for _, tr := range all {
		if tr.Row.ID == drop {
			in, _ = trash.Read(tr.Row)
		}
	}
	if !dr.Deleted || in.Reason != MergedIntoReason+keep.String() || in.Batch != res.Batch.String() {
		t.Fatalf("drop in trash = %+v (batch %s)", in, res.Batch)
	}
	if left, _ := f.eng.Referrers(ctx, drop); len(left) != 0 {
		t.Fatalf("rows still name drop: %+v", left)
	}
	if _, err := f.eng.MergeRows(ctx, nil, schema.TableTags, keep, drop, false); !errs.Has(err, errs.InTrash) {
		t.Fatalf("re-run = %v, want E_IN_TRASH", err)
	}
	if _, err := f.eng.Restore(ctx, res.Batch.String()); err != nil {
		t.Fatalf("restore the merge batch: %v", err)
	}
	if _, r, _ := f.eng.Get(ctx, Ref{ID: drop}); r.Deleted || trash.Is(r) {
		t.Fatal("the merge batch did not restore drop")
	}
}

// TestMergeRowsRefusals: the same id twice, a missing row, and a self
// reference on keep (keep.superseded_by_id = drop) is cleared, not turned
// into keep pointing at itself.
func TestMergeRowsRefusals(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	a, err := f.eng.Create(ctx, "", schema.TableMemories, f.memFields("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.MergeRows(ctx, nil, schema.TableMemories, a.Row.ID, a.Row.ID, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("same id = %v, want E_INVALID_INPUT", err)
	}
	ghost, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.MergeRows(ctx, nil, schema.TableMemories, a.Row.ID, ghost.Any(), false); !errs.Has(err, errs.NotFound) {
		t.Fatalf("missing drop = %v, want E_NOT_FOUND", err)
	}
	fields := f.memFields("b supersedes a")
	fields["superseded_by_id"] = mustRef(t, a.Row.ID)
	b, err := f.eng.Create(ctx, "", schema.TableMemories, fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.MergeRows(ctx, nil, schema.TableMemories, b.Row.ID, a.Row.ID, false); err != nil {
		t.Fatal(err)
	}
	_, kept, err := f.eng.Get(ctx, Ref{Store: store.SourceRepo, ID: b.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := kept.Fields["superseded_by_id"]; ok && v.Kind() != value.KindNull {
		t.Fatalf("keep's self reference = %v, want cleared", v)
	}
}
