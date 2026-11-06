package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRestoreUnderPurgedListedParent (DELETION 4.2, DQ4): a trashed
// dependent whose parent's file is gone but whose id is on a purge list
// (a live ref's purge drops the tombstone and keeps the id) is refused
// E_PARENT_PURGED with the --parent/--no-parent hint, never E_NOT_FOUND,
// and stays in the trash; with no list entry it stays E_NOT_FOUND.
func TestRestoreUnderPurgedListedParent(t *testing.T) {
	for _, listed := range []bool{true, false} {
		ctx := context.Background()
		f, dir := purgeFixture(t)
		key, err := f.eng.Create(ctx, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
		if err != nil {
			t.Fatal(err)
		}
		val, err := f.eng.Create(ctx, kbStore, schema.TableMetaValues, map[string]value.Value{"key_id": txt(key.Row.ID.String()), "name": txt("acme")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.eng.Delete(ctx, Ref{ID: val.Row.ID}); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(rowPath(dir, schema.TableMetaKeys, key.Row.ID)); err != nil {
			t.Fatal(err)
		}
		if listed {
			b, oerr := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
			if oerr != nil {
				t.Fatal(oerr)
			}
			werr := b.WritePurge(ctx, nil, []ids.AnyID{key.Row.ID})
			_ = b.Close() // test teardown
			if werr != nil {
				t.Fatal(werr)
			}
		}
		_, rerr := f.eng.Restore(ctx, val.Row.ID.String())
		want := errs.NotFound
		if listed {
			want = errs.ParentPurged
		}
		if !errs.Has(rerr, want) || !strings.Contains(rerr.Error(), key.Row.ID.String()) {
			t.Fatalf("listed=%v: restore = %v, want %s naming %s", listed, rerr, want, key.Row.ID)
		}
		if all, _ := f.eng.Trashed(ctx); len(all) != 1 {
			t.Fatalf("listed=%v: trash after the refusal = %d rows, want 1", listed, len(all))
		}
	}
}
