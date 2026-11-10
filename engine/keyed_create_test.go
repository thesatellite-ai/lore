package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestNaturalIDKeyedAddMatrix (CLEANUP Qs, memory add --key): the id of a
// keyed add is a function of its parts only: the same parts give the same
// id, any other part (project, key, case, part boundary, unicode
// normalisation by bytes) gives another id.
func TestNaturalIDKeyedAddMatrix(t *testing.T) {
	t.Parallel()
	const kind = "memory.add.key"
	p1, p2 := "prj_01936f0000007a1b8c2d3e4f5a6b7c8d", "prj_01936f0000007a1b8c2d3e4f5a6b7c8e"
	base, err := NaturalID(ids.PrefixMemory, kind, p1, "deploy-notes")
	if err != nil {
		t.Fatal(err)
	}
	if base.Prefix() != ids.PrefixMemory {
		t.Fatalf("prefix %s, want mem", base.Prefix())
	}
	same, err := NaturalID(ids.PrefixMemory, kind, p1, "deploy-notes")
	if err != nil || same != base {
		t.Fatalf("same parts gave %s (%v), want %s", same, err, base)
	}
	others := map[string][]string{
		"other project":       {kind, p2, "deploy-notes"},
		"other key":           {kind, p1, "deploy-note"},
		"case differs":        {kind, p1, "Deploy-Notes"},
		"part boundary moved": {kind, p1 + "deploy", "-notes"},
		"unicode key":         {kind, p1, "déploy-notes"},
		"other kind":          {"tag", p1, "deploy-notes"},
		"empty key":           {kind, p1, ""},
	}
	seen := map[ids.AnyID]string{base: "base"}
	for name, parts := range others {
		id, nerr := NaturalID(ids.PrefixMemory, parts...)
		if nerr != nil {
			t.Fatalf("%s: %v", name, nerr)
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("%s collides with %s (%s)", name, prev, id)
		}
		seen[id] = name
	}
}

// TestCreateWithIDKeyedRace: two adds racing with one natural id store one
// row; every loser gets ErrStale (E_STALE), which memory add turns into
// "existing" (CLEANUP Qs).
func TestCreateWithIDKeyedRace(t *testing.T) {
	t.Parallel()
	f := setup(t)
	id, err := NaturalID(ids.PrefixMemory, "memory.add.key", f.project.String(), "race")
	if err != nil {
		t.Fatal(err)
	}
	const racers = 8
	var wg sync.WaitGroup
	results := make(chan error, racers)
	for range racers {
		wg.Go(func() {
			_, cerr := f.eng.CreateWithID(context.Background(), store.SourceRepo, schema.TableMemories, id, f.memFields("same key"))
			results <- cerr
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for r := range results {
		switch {
		case r == nil:
			won++
		case errors.Is(r, store.ErrStale) || errs.Has(r, errs.Stale):
		default:
			t.Errorf("loser error %v, want E_STALE", r)
		}
	}
	if won != 1 {
		t.Fatalf("%d racers stored the keyed row, want exactly 1", won)
	}
	all, _, err := f.repo.Open(store.OpenOptions{}).Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range all {
		if r.Table == schema.TableMemories {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("memories stored = %d, want 1", n)
	}
}

// TestCreateWithIDOverDeletedKeyRefused: a keyed row that was deleted
// (soft delete: in the trash) is never silently re-created under its id.
func TestCreateWithIDOverDeletedKeyRefused(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	id, err := NaturalID(ids.PrefixMemory, "memory.add.key", f.project.String(), "gone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.CreateWithID(ctx, store.SourceRepo, schema.TableMemories, id, f.memFields("first")); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Delete(ctx, Ref{ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.CreateWithID(ctx, store.SourceRepo, schema.TableMemories, id, f.memFields("again")); err == nil {
		t.Fatal("re-create over a deleted keyed row succeeded")
	}
	got, inTrash, err := f.eng.rawTrashed(ctx, store.SourceRepo, schema.TableMemories, id)
	if err != nil || !inTrash {
		t.Fatalf("the trashed row is gone: in trash %v, %v", inTrash, err)
	}
	if b, ok := got.Fields["body"]; !ok || b.String() != "first" {
		t.Fatalf("the trashed row was overwritten: %v", got.Fields["body"])
	}
}
