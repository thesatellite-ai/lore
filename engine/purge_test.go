package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// kbStore is the folder store a purge test attaches.
const kbStore store.Source = "kb"

// purgeFixture is setup plus a real folder store "kb" (a Purger).
func purgeFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if ierr := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-04"}); ierr != nil {
		t.Fatal(ierr)
	}
	b, err := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() }) // test teardown
	return setup(t, WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()})), dir
}

// rowPath is the file of table/id in the folder store at dir.
func rowPath(dir string, table row.Table, id ids.AnyID) string {
	return filepath.Clean(filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir, filepath.FromSlash(rowfs.RowPath(table, id))))
}

// TestPurgeNeverComesBack (DELETION_DESIGN 3.5, spec 6.1): a purge writes
// the tombstone and lists the id in the folder's _purged.json in one
// batch, audited as a purge; purging again (now a tombstone) succeeds and
// changes nothing; afterwards no write path brings the id back as a live
// row, even when an old clone has put the live file back over the
// tombstone: CreateWithID, Update, Move into the store are refused
// (E_PURGED, decision: coordinator item 1), a natural-key create of the same key takes a fresh id,
// and a tombstone write is still allowed. A store without the list cannot
// be purged in (E_UNSUPPORTED, nothing written).
func TestPurgeNeverComesBack(t *testing.T) {
	ctx := context.Background()
	f, dir := purgeFixture(t)
	prj, err := f.eng.Create(ctx, kbStore, schema.TableProjects, map[string]value.Value{"name": txt("kb-project")})
	if err != nil {
		t.Fatal(err)
	}
	tagFields := map[string]value.Value{"project_id": mustRef(t, prj.Row.ID), "name": txt("deploy")}
	tag, err := f.eng.Create(ctx, kbStore, schema.TableTags, tagFields)
	if err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(rowPath(dir, schema.TableTags, tag.Row.ID))
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{Store: kbStore, Table: schema.TableTags, ID: tag.Row.ID}
	w, err := f.eng.Purge(ctx, ref)
	if err != nil || !w.Row.Deleted {
		t.Fatalf("purge: %+v %v", w.Row, err)
	}
	purgedFile := filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir, rowfs.PurgedFileName)
	if raw := string(mustFile(t, purgedFile)); !strings.Contains(raw, tag.Row.ID.String()) {
		t.Fatalf("_purged.json does not list %s: %s", tag.Row.ID, raw)
	}
	evs := f.audit.events()
	if last := evs[len(evs)-1]; last.Action != ActionPurge || last.ID != tag.Row.ID {
		t.Fatalf("audit %+v, want a purge of %s", last, tag.Row.ID)
	}
	before := string(mustFile(t, purgedFile))
	if _, perr := f.eng.Purge(ctx, ref); perr != nil {
		t.Fatalf("purge twice: %v", perr)
	}
	if again := string(mustFile(t, purgedFile)); again != before {
		t.Fatalf("a second purge rewrote the list: %s → %s", before, again)
	}

	// an old clone brings the live file back over the tombstone
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }() // test teardown
	rel := filepath.Join(rowfs.LoreDir, rowfs.DataDir, filepath.FromSlash(rowfs.RowPath(schema.TableTags, tag.Row.ID)))
	if werr := root.WriteFile(rel, live, 0o600); werr != nil {
		t.Fatal(werr)
	}
	if _, uerr := f.eng.Update(ctx, ref, map[string]value.Value{"color": txt("red")}); !errs.Has(uerr, errs.Purged) {
		t.Fatalf("update of a purged id: %v, want E_PURGED", uerr)
	}
	if rerr := root.Remove(rel); rerr != nil {
		t.Fatal(rerr)
	}
	if _, cerr := f.eng.CreateWithID(ctx, kbStore, schema.TableTags, tag.Row.ID, tagFields); !errs.Has(cerr, errs.Purged) {
		t.Fatalf("CreateWithID of a purged id: %v, want E_PURGED", cerr)
	}
	// the same natural key: a fresh id, never the purged one
	again, err := f.eng.Create(ctx, kbStore, schema.TableTags, tagFields)
	if err != nil {
		t.Fatalf("re-create the key: %v", err)
	}
	if again.Row.ID == tag.Row.ID {
		t.Fatalf("the re-created key got the purged id %s", tag.Row.ID)
	}

	// a copy of the purged row in another store cannot be moved in
	sr, err := store.FromRow(tag.Row)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Move(ctx, Ref{Store: store.SourceRepo, Table: schema.TableTags, ID: tag.Row.ID}, kbStore); !errs.Has(err, errs.Purged) {
		t.Fatalf("move of a purged id into kb: %v, want E_PURGED", err)
	}
	if _, err := f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableTags, tag.Row.ID); err != nil {
		t.Fatalf("the refused move must leave the source row: %v", err)
	}

	// the repo store (memstore) keeps no list: no purge there
	n := len(f.audit.events())
	if _, err := f.eng.Purge(ctx, Ref{Store: store.SourceRepo, ID: f.project}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("purge in a store without a list: %v, want E_UNSUPPORTED", err)
	}
	if _, _, err := f.eng.Get(ctx, Ref{Store: store.SourceRepo, ID: f.project}); err != nil || len(f.audit.events()) != n {
		t.Fatalf("a refused purge wrote something: %v", err)
	}
}

// TestPurgeCascadesListEveryTombstone: purging a project that a cascade
// rule removes rows with lists every tombstoned id of the store.
func TestPurgeCascadesListEveryTombstone(t *testing.T) {
	ctx := context.Background()
	f, dir := purgeFixture(t)
	prj, err := f.eng.Create(ctx, kbStore, schema.TableProjects, map[string]value.Value{"name": txt("p")})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.eng.Purge(ctx, Ref{Store: kbStore, ID: prj.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(mustFile(t, filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir, rowfs.PurgedFileName)))
	for _, c := range append([]Changed{{Store: kbStore, Row: w.Row}}, w.Also...) {
		if c.Row.Deleted && !strings.Contains(raw, c.Row.ID.String()) {
			t.Fatalf("tombstone %s not listed: %s", c.Row.ID, raw)
		}
	}
}

func mustRef(t *testing.T, id ids.AnyID) value.Value {
	t.Helper()
	r, err := value.RefOf(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
