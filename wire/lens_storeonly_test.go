package wire

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestStoreOnlyLensManyFolders (devco D-478): a store-only open (a store
// clone root, a setup folder, --setup, --store) over more folders than
// SQLite attaches reads them through one combined cache, so sql sees every
// folder's rows under its own name. Before, the lens was asked to attach
// one file per folder and refused: "16 sources, at most 10 can be
// attached".
func TestStoreOnlyLensManyFolders(t *testing.T) {
	t.Parallel()
	const n = 16
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	stores := make([]attach.Store, 0, n)
	for i := range n {
		id := "sto_01a1a5aca7f57659be7320c5f4b7" + strconv.FormatInt(int64(0x2000+i), 16)
		stores = append(stores, attach.Store{Name: store.Source("acme/app" + strconv.Itoa(i)), URL: folderStoreAt(t, id, rowfs.VisibilityInternal)})
	}
	if err = a.openStoreOnlyOn(background(), stores[0], "", stores[1:]...); err != nil {
		t.Fatal(err)
	}
	if !a.Combined() {
		t.Fatal("a store-only open over 16 folders has no combined cache")
	}
	want := make([]string, 0, n)
	for _, s := range stores {
		name, verr := value.TextOf("prj-" + string(s.Name))
		if verr != nil {
			t.Fatal(verr)
		}
		if _, err = a.Create(background(), s.Name, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
		want = append(want, string(s.Name)+"=prj-"+string(s.Name))
	}
	slices.Sort(want)
	got := sqlRows(t, a, `SELECT source || '=' || name FROM projects_all ORDER BY 1`)
	if !slices.Equal(got, want) {
		t.Fatalf("projects_all = %v, want %v", got, want)
	}
	if _, err = a.SQL(background(), opssql.Request{Query: `SELECT count(*) FROM projects_all`, IncludeDeleted: true}); err != nil {
		t.Fatalf("--include-deleted over 16 folders: %v", err)
	}
}
