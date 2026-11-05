package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// storedIDs is every live row id of table in the attached store name, read
// from the backend itself (not the cache, not the engine).
func storedIDs(t *testing.T, e *Engine, name store.Source, table row.Table) map[ids.AnyID]bool {
	t.Helper()
	rows, _, err := e.stores[name].Backend.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[ids.AnyID]bool{}
	for _, r := range rows {
		if r.Table == table && !r.Deleted {
			got[r.ID] = true
		}
	}
	return got
}

// metaKeyFields is a meta_keys row named name (no project needed).
func metaKeyFields(name string) map[string]value.Value {
	return map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")}
}

// TestStagingCommitBothBackends (FB-RES-1, later 565): staged creates
// reach neither the store, the cache nor the audit before Commit; Commit
// writes them as one batch, then the cache holds every row and the audit
// one create event each; on a journaled backend (folder) nothing stays
// pending afterwards. Without Commit nothing is ever written.
func TestStagingCommitBothBackends(t *testing.T) {
	cases := []struct {
		name   string
		source store.Source
		table  row.Table
		fields func(f *fixture, i int) map[string]value.Value
	}{
		{"memory store", store.SourceRepo, schema.TableMemories, func(f *fixture, i int) map[string]value.Value {
			return f.memFields("staged " + string(rune('a'+i)))
		}},
		{"folder store with journal", kbStore, schema.TableMetaKeys, func(_ *fixture, i int) map[string]value.Value {
			return metaKeyFields("staged-" + string(rune('a'+i)))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, sink, _, j := journalEngine(t)
			ctx := Staging(context.Background())
			var made []ids.AnyID
			for i := range 3 {
				w, err := f.eng.Create(ctx, c.source, c.table, c.fields(f, i))
				if err != nil {
					t.Fatal(err)
				}
				made = append(made, w.Row.ID)
			}
			for _, id := range made {
				if storedIDs(t, f.eng, c.source, c.table)[id] {
					t.Fatalf("%s is in the store before Commit", id)
				}
				if _, ok := f.cache.rows[c.source][store.Key{Table: c.table, ID: id}]; ok {
					t.Fatalf("%s is in the cache before Commit", id)
				}
				if sink.recorded(id) != 0 {
					t.Fatalf("%s audited before Commit", id)
				}
			}
			if _, err := Commit(ctx); err != nil {
				t.Fatal(err)
			}
			got := storedIDs(t, f.eng, c.source, c.table)
			for _, id := range made {
				if !got[id] {
					t.Fatalf("%s not stored after Commit", id)
				}
				if _, ok := f.cache.rows[c.source][store.Key{Table: c.table, ID: id}]; !ok {
					t.Fatalf("%s not in the cache after Commit", id)
				}
				if n := sink.recorded(id); n != 1 {
					t.Fatalf("%s audited %d times, want 1", id, n)
				}
			}
			if p := pendingIDs(t, j); len(p) != 0 {
				t.Fatalf("journal pending after Commit: %v", p)
			}

			// a stage dropped without Commit writes nothing
			lost := Staging(context.Background())
			w, err := f.eng.Create(lost, c.source, c.table, c.fields(f, 9))
			if err != nil {
				t.Fatal(err)
			}
			if storedIDs(t, f.eng, c.source, c.table)[w.Row.ID] || sink.recorded(w.Row.ID) != 0 {
				t.Fatalf("an uncommitted stage wrote %s", w.Row.ID)
			}
		})
	}
}

// TestStagingTwoStoresRefused: a stage that spans two stores is refused at
// Commit (no backend batch covers two stores) with nothing written in
// either store, and nothing audited.
func TestStagingTwoStoresRefused(t *testing.T) {
	f, sink, _, _ := journalEngine(t)
	ctx := Staging(context.Background())
	m, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("repo half"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, metaKeyFields("kb-half"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("two-store Commit: %v, want E_INVALID_INPUT", err)
	}
	if storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[m.Row.ID] || storedIDs(t, f.eng, kbStore, schema.TableMetaKeys)[k.Row.ID] {
		t.Fatal("a refused two-store stage wrote a row")
	}
	if sink.recorded(m.Row.ID)+sink.recorded(k.Row.ID) != 0 {
		t.Fatal("a refused two-store stage was audited")
	}
}

// TestStagingDuplicateCreateDropped: a second create of a row the stage
// already creates (two staged writes ensuring one row) is dropped, so the
// store gets one row and the audit one create event.
func TestStagingDuplicateCreateDropped(t *testing.T) {
	f, sink, _, _ := journalEngine(t)
	id, err := NaturalID(ids.PrefixMemory, "memory.add.key", f.project.String(), "twice")
	if err != nil {
		t.Fatal(err)
	}
	ctx := Staging(context.Background())
	for _, body := range []string{"first", "second"} {
		if _, err = f.eng.CreateWithID(ctx, store.SourceRepo, schema.TableMemories, id, f.memFields(body)); err != nil {
			t.Fatalf("staged create %q: %v", body, err)
		}
	}
	if _, err = Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, got, err := f.eng.Get(context.Background(), Ref{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Fields["body"].String(); b != "first" {
		t.Fatalf("body %q, want the first create to stand", b)
	}
	if n := sink.recorded(id); n != 1 {
		t.Fatalf("create audited %d times, want 1", n)
	}
}

// TestStagingSecondWriteIsErrBatch: the stage cannot read its own rows
// back, so a second edit of a row it already writes is ErrBatch
// (E_INVALID_INPUT) and Commit still writes the first edit only.
func TestStagingSecondWriteIsErrBatch(t *testing.T) {
	f, _, _, _ := journalEngine(t)
	base, err := f.eng.Create(context.Background(), store.SourceRepo, schema.TableMemories, f.memFields("v1"))
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{ID: base.Row.ID}
	ctx := Staging(context.Background())
	if _, err = f.eng.Update(ctx, ref, map[string]value.Value{"body": txt("v2")}); err != nil {
		t.Fatal(err)
	}
	_, err = f.eng.Update(ctx, ref, map[string]value.Value{"body": txt("v3")})
	if !errors.Is(err, store.ErrBatch) || !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("second staged edit: %v, want ErrBatch / E_INVALID_INPUT", err)
	}
	if _, err = Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, got, err := f.eng.Get(context.Background(), ref)
	if err != nil || got.Fields["body"].String() != "v2" {
		t.Fatalf("after Commit body %v (%v), want v2", got.Fields["body"], err)
	}
}

// TestCommitTwiceAndAfter: a second Commit is a no-op; a write made with
// the committed ctx goes straight to the store; Commit on a ctx without a
// stage, or on an empty stage, does nothing.
func TestCommitTwiceAndAfter(t *testing.T) {
	f, sink, _, _ := journalEngine(t)
	if w, err := Commit(context.Background()); err != nil || w != nil {
		t.Fatalf("Commit without a stage: %v %v", w, err)
	}
	if w, err := Commit(Staging(context.Background())); err != nil || w != nil {
		t.Fatalf("Commit of an empty stage: %v %v", w, err)
	}
	ctx := Staging(context.Background())
	m, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("once"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx); err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if n := sink.recorded(m.Row.ID); n != 1 {
		t.Fatalf("committed twice: audited %d times, want 1", n)
	}
	after, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("after"))
	if err != nil {
		t.Fatal(err)
	}
	if !storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[after.Row.ID] || sink.recorded(after.Row.ID) != 1 {
		t.Fatal("a write after Commit was held instead of written")
	}
}
