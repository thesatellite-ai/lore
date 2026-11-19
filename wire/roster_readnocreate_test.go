package wire

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// TestReadNeverCreatesAProjectRow (devco:T-11): a read that needs a store
// folder's project and finds none (lore search in a repo with no lore
// data reached the personal store's root this way) answers E_NOT_FOUND
// and writes nothing: no row, no file in the store. A write still makes
// the row.
func TestReadNeverCreatesAProjectRow(t *testing.T) {
	t.Parallel()
	url := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	a := rosterApp(t, url, false)
	dir := fileuri.LocalPath(url)
	if dir == "" {
		t.Fatalf("no local folder for %s", url)
	}
	files := func() string {
		var out []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	read := engine.WithAccess(background(), op.AccessRead)
	st, err := a.state(read)
	if err != nil {
		t.Fatal(err)
	}
	before := files()
	if _, err := a.rosterProject(read, st, rosterStore, "org"); !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "a read never makes one") {
		t.Fatalf("a read with no project row: err = %v, want E_NOT_FOUND", err)
	}
	if _, err := a.folderProject(read, st, rosterStore); err != nil && !errs.Has(err, errs.NotFound) {
		t.Fatalf("folderProject on a read: %v", err)
	}
	rows, err := st.eng.List(read, rosterStore, schema.TableProjects)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a read wrote project rows: %v %v", rows, err)
	}
	if after := files(); after != before {
		t.Fatalf("a read changed the store's files:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	write := engine.WithAccess(background(), op.AccessWrite)
	if _, err := a.rosterProject(write, st, rosterStore, "org"); err != nil {
		t.Fatalf("a write did not make the project row: %v", err)
	}
	if files() == before {
		t.Fatal("the file check sees nothing: a write left the store's files unchanged")
	}
}
