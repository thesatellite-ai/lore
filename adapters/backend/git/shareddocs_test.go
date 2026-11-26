package git

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestSharedDocsReadsCopyNoDocument (bug hunt H-277, devco:D-1015): a
// full read made with the shared-documents promise (store.WithSharedDocs,
// the engine's read snapshot) hands out the documents the backend keeps
// for its read cache instead of a copy of each, kept and served alike;
// a read without the promise still gets its own copy (storetest: a
// backend never hands out its own buffer). Before, every document was
// copied into the cache and again for each reader: ~120 MB of lore
// doctor's memory at 100k commits.
func TestSharedDocsReadsCopyNoDocument(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	mem := mustID[ids.Memory](t)
	o.h.commit(o.boss.email, map[string]string{orgPath("memories", mem): stampedRow(mem, o.boss.actor, "2026-01-02T00:00:00.000Z", "shared bytes")})
	b := orgFolderClone(t, o.h.remote)
	shared := store.WithSharedDocs(context.Background())
	first, _, err := b.Read(shared, "") // read and kept
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := b.Read(shared, "") // served from the cache
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := b.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	docOf := func(rows []store.Row) []byte {
		for _, r := range rows {
			if r.ID.String() == mem.String() {
				return r.Doc
			}
		}
		t.Fatal("the memory is not read")
		return nil
	}
	a, c, p := docOf(first), docOf(second), docOf(plain)
	if &a[0] != &c[0] {
		t.Error("a shared read from the cache copied the document, want the kept bytes")
	}
	if &p[0] == &a[0] {
		t.Error("a read without the promise got the kept bytes, want its own copy")
	}
	if string(p) != string(a) {
		t.Errorf("the copy differs:\n%s\n%s", p, a)
	}
}
