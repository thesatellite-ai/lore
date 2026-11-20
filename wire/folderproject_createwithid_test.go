package wire

// folderproject_createwithid_test.go — devco:D-746: `lore task add --to
// <org>/<folder>` in a repo that does not attach the folder failed exit 27
// "[E_NOT_FOUND] task_lists refers to rows no attached store holds:
// project_id". task add makes its default list with CreateWithID (the
// list's natural id), and only Create staged the folder's project row
// first (folderProjectFirst); once D-740 stopped naming the project from
// committing that row on the side, a list into a folder with no project
// row yet named a row nobody held. Both create paths now share
// createRowsFirst.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestCreateWithIDIntoFolderStagesItsProjectRow: a row created with a
// pinned id (task add's default list) into a `--to` folder with no
// project row yet lands, with the folder's project row in its batch.
func TestCreateWithIDIntoFolderStagesItsProjectRow(t *testing.T) {
	t.Parallel()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	if err := os.MkdirAll(filepath.Join(storeDir, "web"), 0o750); err != nil {
		t.Fatal(err)
	}
	folderID, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "web", rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+folderID.String()+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml += "  - name: northwind/web\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: web\n"
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	st, err := a.writeState(background())
	if err != nil {
		t.Fatal(err)
	}
	web := store.Source("northwind/web")
	ctx := context.WithValue(background(), newFolderKey{}, newFolder{target: web, folder: "web"})
	live := func(table row.Table) int {
		t.Helper()
		rows, lerr := st.eng.List(background(), web, table)
		if lerr != nil {
			t.Fatal(lerr)
		}
		n := 0
		for _, r := range rows {
			if !r.Deleted {
				n++
			}
		}
		return n
	}
	pid, ok, err := a.targetFolderProject(ctx)
	if err != nil || !ok || pid.IsZero() {
		t.Fatalf("the web folder's project: %v %v %v", pid, ok, err)
	}
	ref, err := value.RefOf(pid.Any())
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{repoProjectField: ref}
	for k, v := range map[string]string{"title": "default", "status_str": "active"} {
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	// the natural id task add gives a folder's default list
	listID, err := ids.Natural(ids.PrefixTaskList, "default-tasklist", pid.String(), string(web))
	if err != nil {
		t.Fatal(err)
	}
	if p := live(schema.TableProjects); p != 0 {
		t.Fatalf("fixture: the web folder already holds %d project row(s)", p)
	}
	if _, err := a.CreateWithID(ctx, web, schema.TableTasklists, listID, fields); err != nil {
		t.Fatalf("[devco:D-746] a CreateWithID into a folder with no project row yet: %v", err)
	}
	if p, l := live(schema.TableProjects), live(schema.TableTasklists); p != 1 || l != 1 {
		t.Fatalf("an allowed CreateWithID into the web folder made %d project and %d list rows, want 1 and 1", p, l)
	}
}
