package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestStagedReadsSeeTheStage (devco:D-1002): a stage made under
// WithStagedReads reads its own rows back (Get and List) and takes a
// second write of a row it holds (a create, then an edit): Commit writes
// the last version as one create, and the cache holds it. Break-verified:
// before, a real stage's rows were not read back (Get E_NOT_FOUND, List
// without them) and the edit was ErrBatch, so task import committed its
// batch before every task-tree record.
func TestStagedReadsSeeTheStage(t *testing.T) {
	t.Parallel()
	f, _, _, _ := journalEngine(t)
	ctx := Staging(WithStagedReads(context.Background()))
	m, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("widget v1"))
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{ID: m.Row.ID}
	if _, got, gerr := f.eng.Get(ctx, ref); gerr != nil || got.Fields["body"].String() != "widget v1" {
		t.Fatalf("Get of a staged row: %v (%v), want widget v1", got.Fields["body"], gerr)
	}
	listed, err := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range listed {
		seen = seen || r.ID == m.Row.ID
	}
	if !seen {
		t.Fatal("List under WithStagedReads lacks the staged row")
	}
	if _, err = f.eng.Update(ctx, ref, map[string]value.Value{"body": txt("widget v2")}); err != nil {
		t.Fatalf("edit of a staged row: %v", err)
	}
	if storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[m.Row.ID] {
		t.Fatal("a staged row reached the store before Commit")
	}
	if _, err = Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, got, err := f.eng.Get(context.Background(), ref)
	if err != nil || got.Fields["body"].String() != "widget v2" {
		t.Fatalf("after Commit body %v (%v), want widget v2", got.Fields["body"], err)
	}
	listed, err = f.eng.List(context.Background(), store.SourceRepo, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range listed {
		if r.ID == m.Row.ID && r.Fields["body"].String() != "widget v2" {
			t.Fatalf("the cache lists body %v, want widget v2", r.Fields["body"])
		}
	}
}

// TestStagedReadsOffKeepsTheOldStage: without WithStagedReads a real
// stage still reads nothing back and refuses a second write (FB-RES-1).
func TestStagedReadsOffKeepsTheOldStage(t *testing.T) {
	t.Parallel()
	f, _, _, _ := journalEngine(t)
	ctx := Staging(context.Background())
	m, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("gadget"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, gerr := f.eng.Get(ctx, Ref{ID: m.Row.ID}); gerr == nil {
		t.Fatal("a plain stage read its own row back")
	}
	Discard(ctx)
}
