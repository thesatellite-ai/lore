package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// trailerSpy is a folder backend that records the per-write commit
// trailers each write carried (what the git backend would commit).
type trailerSpy struct {
	*folder.Backend
	got [][]string
}

func (s *trailerSpy) Write(ctx context.Context, changes []store.Change) error {
	s.got = append(s.got, store.CommitTrailers(ctx))
	return s.Backend.Write(ctx, changes)
}

// WriteAudited: trash, restore and delete write their audit records in
// the rows' own batch (later 542), so they reach the backend here.
func (s *trailerSpy) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	s.got = append(s.got, store.CommitTrailers(ctx))
	return s.Backend.WriteAudited(ctx, changes, recs)
}

func (s *trailerSpy) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	s.got = append(s.got, store.CommitTrailers(ctx))
	return s.Backend.WritePurge(ctx, changes, purged)
}

// TestTrashTrailers (DELETION 2.1, 2.6, 3.3): a soft delete commits with
// Lore-Delete-Batch: <bat>, its restore with Lore-Restore: <bat>, and a
// purge from the trash with Lore-Purge: <day> <bat>.
func TestTrashTrailers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sid, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-04"}); err != nil {
		t.Fatal(err)
	}
	b, err := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() }) // test teardown
	spy := &trailerSpy{Backend: b}
	f := setup(t, WithStore(Attached{Name: kbStore, Backend: spy, KeyNamespace: sid.String()}))
	key, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	last := func() string { return strings.Join(spy.got[len(spy.got)-1], "|") }
	w, err := f.eng.Delete(ctx, Ref{ID: key.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	in, _ := trash.Read(w.Row)
	if got, want := last(), DeleteBatchTrailer+": "+in.Batch; got != want {
		t.Fatalf("delete trailers = %q, want %q", got, want)
	}
	if _, err = f.eng.Restore(ctx, in.Batch); err != nil {
		t.Fatal(err)
	}
	if got, want := last(), RestoreTrailer+": "+in.Batch; got != want {
		t.Fatalf("restore trailers = %q, want %q", got, want)
	}
	w, err = f.eng.Delete(ctx, Ref{ID: key.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	in, _ = trash.Read(w.Row)
	if _, err = f.eng.PurgeTrash(ctx, in.Batch); err != nil {
		t.Fatal(err)
	}
	if got := last(); !strings.HasPrefix(got, PurgeTrailer+": ") || !strings.HasSuffix(got, " "+in.Batch) {
		t.Fatalf("purge trailers = %q", got)
	}
}
