package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// OV62: store.RowHistory over a store clone: one version per commit that
// touched the row file, oldest first, bytes as written; an unknown row
// and a row of another table have no versions.
func TestVersionsClone(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	id := newID(t)
	v1 := g.row(id, "one", hlc.HLC{})
	write(t, b, store.Change{Row: v1})
	write(t, b, store.Change{Row: g.row(newID(t), "other", hlc.HLC{})}) // does not touch the row
	v2 := g.row(id, "two", v1.HLC)
	write(t, b, store.Change{Row: v2, Expect: v1.HLC})
	vers, err := b.Versions(ctx, memories, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(vers) != 2 || string(vers[0].Row.Doc) != string(v1.Doc) || string(vers[1].Row.Doc) != string(v2.Doc) {
		t.Fatalf("versions = %d %+v", len(vers), vers)
	}
	if vers[0].Commit == vers[1].Commit || vers[1].At.Before(vers[0].At) || vers[1].Author == "" {
		t.Fatalf("version headers %+v", vers)
	}
	if head := gitT(t, b.Dir(), "rev-parse", "HEAD"); vers[1].Commit != head {
		t.Fatalf("last version %s, head %s", vers[1].Commit, head)
	}
	if none, err := b.Versions(ctx, memories, newID(t)); err != nil || len(none) != 0 {
		t.Fatalf("unknown row: %v %v", none, err)
	}
	if _, err := b.Versions(ctx, "", id); err == nil {
		t.Fatal("no table accepted")
	}
}

// OV62: repo mode reads the commits the person made, from a lore project
// in a monorepo subfolder ("./" paths), and skips the uncommitted edit; an
// unborn branch has no history and no error.
func TestVersionsRepoSubfolder(t *testing.T) {
	t.Parallel()
	top, g, ctx := newRepo(t), newGen(t), context.Background()
	sub := filepath.Join(top, "svc")
	if err := os.MkdirAll(filepath.Join(sub, rowfs.LoreDir), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := OpenRepo(ctx, sub, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := newID(t)
	v1 := g.row(id, "one", hlc.HLC{})
	write(t, b, store.Change{Row: v1})
	if vers, verr := b.Versions(ctx, memories, id); verr != nil || len(vers) != 0 {
		t.Fatalf("unborn branch: %v %v", vers, verr)
	}
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "--quiet", "-m", "c1")
	write(t, b, store.Change{Row: g.row(id, "two", v1.HLC), Expect: v1.HLC}) // not committed
	vers, err := b.Versions(ctx, memories, id)
	if err != nil || len(vers) != 1 || string(vers[0].Row.Doc) != string(v1.Doc) || vers[0].Author != "t" {
		t.Fatalf("repo versions %+v %v", vers, err)
	}
}
