package wire

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// leftoverFixture is a checkout whose cache file holds every row of the
// repo store twice: under its own name and under kb/app, the name an
// older attachment applied it under (what a `lore store remove` and a
// `lore store add` under a new name left, public issue #142). It returns
// the repo, the lore home and the cache file's path.
func leftoverFixture(t *testing.T) (repo, home, path string) {
	t.Helper()
	ctx := background()
	repo, home = gitRepo(t), t.TempDir()
	a := newTestAppHome(t, repo, home)
	withProject(t, a)
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{"body": "pelican invoices are exported nightly", "kind": schema.MemoryKindCore, "source_kind": "test"} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	if fields["project_id"], err = value.RefOf(pid.Any()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Create(ctx, store.SourceRepo, schema.TableMemories, fields); err != nil {
		t.Fatal(err)
	}
	rows, err := eng.CurrentRows(ctx, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.caches.of(store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ctx, leftoverName, rows, ""); err != nil {
		t.Fatal(err)
	}
	if srcs, err := f.Sources(ctx); err != nil || !slices.Contains(srcs, leftoverName) {
		t.Fatalf("the fixture holds no rows under %s: %v %v", leftoverName, srcs, err)
	}
	st.caches.mu.Lock()
	path = st.caches.paths[store.SourceRepo]
	st.caches.mu.Unlock()
	return repo, home, path
}

// leftoverName is the name the fixture's folder was attached under before.
const leftoverName = store.Source("kb/app")

// sourcesAfterOpen opens repo afresh over home and lists the names its
// repo cache file holds rows under once the open is done.
func sourcesAfterOpen(t *testing.T, repo, home string) []store.Source {
	t.Helper()
	ctx := background()
	b := newTestAppHome(t, repo, home)
	st, err := b.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.caches.of(store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := f.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return srcs
}

// TestOpenDropsRowsUnderANameNoAttachmentUses (devco D-930, hunt H-188,
// public issue #142): the next open drops the rows a cache file holds
// under a name no attachment uses any more, with no `lore search
// rebuild`. Before, those copies stayed in the file: no edit reached them,
// and search and ask kept finding their old text.
func TestOpenDropsRowsUnderANameNoAttachmentUses(t *testing.T) {
	t.Parallel()
	repo, home, _ := leftoverFixture(t)
	if srcs := sourcesAfterOpen(t, repo, home); slices.Contains(srcs, leftoverName) {
		t.Fatalf("the open left the rows under %s: %v", leftoverName, srcs)
	}
}

// TestOpenKeepsANameAnotherCheckoutUses (devco D-930): a cache file is
// shared by every checkout using the folder, each under its own name; a
// name another live checkout's last open used (its cache use record) is
// that checkout's copy and stays, so the two checkouts never empty each
// other's rows.
func TestOpenKeepsANameAnotherCheckoutUses(t *testing.T) {
	t.Parallel()
	repo, home, path := leftoverFixture(t)
	other := gitRepo(t)
	root := filepath.Join(home, cacheDir)
	if err := sqlite.RecordUse(root, other, []sqlite.Use{sqlite.UseOf(root, path, string(leftoverName), false)}); err != nil {
		t.Fatal(err)
	}
	if srcs := sourcesAfterOpen(t, repo, home); !slices.Contains(srcs, leftoverName) {
		t.Fatalf("the open dropped the rows another checkout uses: %v", srcs)
	}
}
