package wire

// folderproject_refused_test.go — devco:D-740: a member's refused `rule
// add --to <org>/<another team's folder>` left a "lore: project add prj_…"
// commit in their store clone. Resolving the project of the `--to` folder
// (targetFolderProject) committed the folder's project row on its own,
// before the write's role check refused the rule, so the refusal said
// "nothing was written" while a commit had landed. The row now goes in
// the same batch as the row that names it, and is dropped with it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refuseAll is a WriteGuard that refuses every guarded write, as the role
// rule refuses a member's add outside their team's folder.
type refuseAll struct{}

func (refuseAll) GuardWrite(context.Context, store.Source, []engine.GuardedWrite) error {
	return errs.New(errs.Refused, "only an admin of the org may do this; nothing was written")
}

// TestRefusedFolderWriteLeavesNoProjectRow: the project of a `--to` folder
// with no project row yet is named without a write, and a refused write
// into that folder leaves no project row behind; an allowed write makes
// the row in its own batch.
func TestRefusedFolderWriteLeavesNoProjectRow(t *testing.T) {
	t.Parallel()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	if err := os.MkdirAll(filepath.Join(storeDir, "api"), 0o750); err != nil {
		t.Fatal(err)
	}
	folderID, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "api", rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+folderID.String()+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml += "  - name: northwind/api\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: api\n"
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
	api := store.Source("northwind/api")
	ctx := context.WithValue(background(), newFolderKey{}, newFolder{target: api, folder: "api"})
	projects := func() int {
		t.Helper()
		rows, lerr := st.eng.List(background(), api, schema.TableProjects)
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
		t.Fatalf("the api folder's project: %v %v %v", pid, ok, err)
	}
	if n := projects(); n != 0 {
		t.Fatalf("[devco:D-740] naming the project of the --to folder wrote %d project row(s) before any write was judged", n)
	}
	ref, err := value.RefOf(pid.Any())
	if err != nil {
		t.Fatal(err)
	}
	body, err := value.TextOf("api answers in json")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{"body": body, repoProjectField: ref}
	for k, v := range map[string]string{"kind": schema.MemoryKindCore, "source_kind": "manual"} {
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	st.eng.SetWriteGuard(refuseAll{})
	if _, err := a.Create(ctx, api, schema.TableMemories, fields); !errs.Has(err, errs.Refused) {
		t.Fatalf("the guarded write was not refused: %v", err)
	}
	if n := projects(); n != 0 {
		t.Fatalf("[devco:D-740] a refused write into the api folder left %d project row(s) behind", n)
	}
	st.eng.SetWriteGuard(nil)
	if _, err := a.Create(ctx, api, schema.TableMemories, fields); err != nil {
		t.Fatalf("an allowed write into the api folder: %v", err)
	}
	if n := projects(); n != 1 {
		t.Fatalf("an allowed write into the api folder made %d project rows, want its one", n)
	}
}
