package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestCreateInFolderTakesFolderProject (devco D-718): a task or memory
// created in a store folder from a repo carries the repo's project (the
// op resolves it before it knows the target); the row must land with the
// folder's own project. A project the folder holds is kept, and a store
// with no folder identity keeps what the writer gave.
func TestCreateInFolderTakesFolderProject(t *testing.T) {
	ctx := context.Background()
	folderAny, err := ids.ParseAny("prj_01936f0000007a1b8c2d3e4f5a6b7c9a")
	if err != nil {
		t.Fatal(err)
	}
	folderPID, err := ids.As[ids.Project](folderAny)
	if err != nil {
		t.Fatal(err)
	}
	const folder store.Source = "acme/client_a"
	const plain store.Source = "acme-plain"
	f := setup(t,
		WithStore(Attached{Name: folder, Backend: folderBackend{Backend: memstore.NewData().Open(store.OpenOptions{}), pid: folderPID}}),
		WithStore(Attached{Name: plain, Backend: memstore.NewData().Open(store.OpenOptions{})}),
	)
	// the calling repo's project (repo store), as Env.Project gives it
	repoPrj, err := f.eng.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("app")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.CreateWithID(ctx, folder, schema.TableProjects, folderAny, map[string]value.Value{"name": txt("client_a")}); err != nil {
		t.Fatal(err)
	}
	other, err := f.eng.Create(ctx, folder, schema.TableProjects, map[string]value.Value{"name": txt("client_a-side")})
	if err != nil {
		t.Fatal(err)
	}
	task := func(to store.Source, prj ids.AnyID) string {
		t.Helper()
		w, terr := f.eng.Create(ctx, to, schema.TableTasks, map[string]value.Value{
			"project_id": mustRef(t, prj), "title": txt("rotate the webhook secret"), "status": txt(schema.TaskStatusTodo),
			"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted)})
		if terr != nil {
			t.Fatal(terr)
		}
		return w.Row.Fields["project_id"].String()
	}
	if got := task(folder, repoPrj.Row.ID); got != folderAny.String() {
		t.Errorf("task added to the folder: project_id = %s, want the folder's %s (repo's is %s)", got, folderAny, repoPrj.Row.ID)
	}
	mem, err := f.eng.Create(ctx, folder, schema.TableMemories, map[string]value.Value{
		"project_id": mustRef(t, repoPrj.Row.ID), "body": txt("client_a deploys on tuesdays"), "kind": txt("retrieved"), "source_kind": txt("manual")})
	if err != nil {
		t.Fatal(err)
	}
	if got := mem.Row.Fields["project_id"].String(); got != folderAny.String() {
		t.Errorf("memory added to the folder: project_id = %s, want %s", got, folderAny)
	}
	if got := task(folder, other.Row.ID); got != other.Row.ID.String() {
		t.Errorf("a project the folder holds was replaced: %s, want %s", got, other.Row.ID)
	}
	if got := task(plain, repoPrj.Row.ID); got != repoPrj.Row.ID.String() {
		t.Errorf("a store with no folder identity changed project_id to %s", got)
	}
	// a row whose project_id is its subject (a member of that project, a
	// repo of it, a team of it) keeps the project the writer named, also
	// when it is not a project of the folder: `lore project member add
	// --project northwind-app` must not become a member of the folder's
	// project (docs profiles section, v0.1.19)
	for _, tbl := range []string{schema.TableProjectMembers, schema.TableRepos, schema.TableTeams} {
		def, derr := f.eng.tableDef(row.Table(tbl))
		if derr != nil {
			t.Fatal(derr)
		}
		in := map[string]value.Value{"project_id": mustRef(t, repoPrj.Row.ID)}
		out, perr := f.eng.placeFolderProject(ctx, folder, def, in)
		if perr != nil {
			t.Fatal(perr)
		}
		if got := out["project_id"].String(); got != repoPrj.Row.ID.String() {
			t.Errorf("%s row written into the folder: project_id = %s, want the named project %s kept", tbl, got, repoPrj.Row.ID)
		}
	}
}
