package sqlite

import (
	"bytes"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRowMirrorAnswersOnlyTheSameBlob: the row mirror (perf3) gives a row
// back only under the blob id it was kept under and only to its source,
// survives a reopen, and a rebuild of the source forgets it.
func TestRowMirrorAnswersOnlyTheSameBlob(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("mirror.db")
	r := f.memory(newMem(), newMem(), "kept")
	path := "memories/" + r.ID.String() + ".json"
	if err := c.Mirror(store.SourceRepo).Keep(f.ctx, []store.MirrorEntry{{Path: path, Blob: "b1", Row: r}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open("mirror.db")
	got := must(c.Mirror(store.SourceRepo).Lookup(f.ctx, schema.TableMemories, map[string]string{path: "b1"}))
	if h, ok := got[path]; !ok || !bytes.Equal(h.Doc, r.Doc) || h.ID != r.ID || h.HLC != r.HLC {
		t.Fatalf("same blob: %+v", got)
	}
	if got := must(c.Mirror(store.SourceRepo).Lookup(f.ctx, schema.TableMemories, map[string]string{path: "b2"})); len(got) != 0 {
		t.Fatal("answered under another blob id")
	}
	if got := must(c.Mirror("other").Lookup(f.ctx, schema.TableMemories, map[string]string{path: "b1"})); len(got) != 0 {
		t.Fatal("answered for another source")
	}
	if err := c.Rebuild(f.ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if got := must(c.Mirror(store.SourceRepo).Lookup(f.ctx, schema.TableMemories, map[string]string{path: "b1"})); len(got) != 0 {
		t.Fatal("a rebuild kept the mirror")
	}
}

// TestRowMirrorNeverWaitsForTheCache: a read can run inside a cache
// operation (Apply expands references by listing rows while it holds the
// cache), so the mirror must answer at once instead of waiting for the
// cache it is part of: waiting there hung every write that expanded a
// reference.
func TestRowMirrorNeverWaitsForTheCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("busy.db")
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.Mirror(store.SourceRepo).Lookup(f.ctx, schema.TableMemories, map[string]string{"memories/x.json": "b"}); err == nil {
		t.Fatal("Lookup on a busy cache answered as if it read it")
	}
	if err := c.Mirror(store.SourceRepo).Keep(f.ctx, []store.MirrorEntry{{Path: "memories/x.json", Blob: "b", Row: f.memory(newMem(), newMem(), "x")}}); err == nil {
		t.Fatal("Keep on a busy cache claimed to keep")
	}
}
