package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// folderBackend is a store backend with a data folder identity, as the
// git and folder backends have (rowfs.Folder.ProjectID).
type folderBackend struct {
	store.Backend
	pid ids.ID[ids.Project]
}

func (b folderBackend) FolderProjectID(context.Context) (ids.ID[ids.Project], error) {
	return b.pid, nil
}

// TestMovePersonalProjectRehomes (devco P-7): a task written in a repo
// with no lore data names a project row only the personal store holds;
// moving it to an org folder takes that folder's project (its row written
// there with the personal row's name) instead of carrying the personal
// pointer F15 refuses. A target with no folder identity keeps the F15
// refusal (nothing to re-home to).
func TestMovePersonalProjectRehomes(t *testing.T) {
	ctx := context.Background()
	folderAny, err := ids.ParseAny("prj_01936f0000007a1b8c2d3e4f5a6b7c8e")
	if err != nil {
		t.Fatal(err)
	}
	folderPID, err := ids.As[ids.Project](folderAny)
	if err != nil {
		t.Fatal(err)
	}
	const shared store.Source = "acme/skills"
	const root store.Source = "acme"
	f := setup(t,
		WithStore(Attached{Name: personal, Backend: memstore.NewData().Open(store.OpenOptions{})}),
		WithStore(Attached{Name: shared, Backend: folderBackend{Backend: memstore.NewData().Open(store.OpenOptions{}), pid: folderPID}}),
		WithStore(Attached{Name: root, Backend: memstore.NewData().Open(store.OpenOptions{})}),
	)
	prj, err := f.eng.Create(ctx, personal, schema.TableProjects, map[string]value.Value{"name": txt("skills")})
	if err != nil {
		t.Fatal(err)
	}
	task := func(title string) ids.AnyID {
		w, terr := f.eng.Create(ctx, personal, schema.TableTasks, map[string]value.Value{
			"project_id": mustRef(t, prj.Row.ID), "title": txt(title), "status": txt(schema.TaskStatusTodo),
			"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted)})
		if terr != nil {
			t.Fatal(terr)
		}
		return w.Row.ID
	}
	moved := task("share later")
	if _, err = f.eng.Move(ctx, Ref{Table: schema.TableTasks, ID: moved}, shared); err != nil {
		t.Fatalf("move to the org folder: %v", err)
	}
	_, got, err := f.eng.Get(ctx, Ref{Store: shared, Table: schema.TableTasks, ID: moved})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Fields["project_id"].String(); p != folderAny.String() {
		t.Fatalf("project_id = %s, want the folder's %s", p, folderAny)
	}
	_, proj, err := f.eng.Get(ctx, Ref{Store: shared, Table: schema.TableProjects, ID: folderAny})
	if err != nil || proj.Fields["name"].String() != "skills" {
		t.Fatalf("folder project row: %+v, %v; want one named skills", proj, err)
	}
	if _, err = f.eng.Move(ctx, Ref{Table: schema.TableTasks, ID: task("stays")}, root); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("move to a store root: %v, want the F15 refusal", err)
	}
}
