package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opsmember "github.com/thesatellite-ai/lore/ops/member"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// TestFindProject (TEAM_PROFILES 3.5): the App the member ops get is a
// ProjectFinder; a name and a prj_ id resolve across the repo and the org
// store; an unknown ref is E_NOT_FOUND; one name on two projects in two
// stores is E_PROJECT_NAME_COLLISION, and each still resolves by id.
func TestFindProject(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := rosterApp(t, folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal), false)
	var env opsmember.Env = a
	f, ok := env.(opsmember.ProjectFinder) // what upgrade.As sees at run time
	if !ok {
		t.Fatal("the member Env is not a ProjectFinder")
	}
	p, err := f.FindProject(ctx, "demo")
	if err != nil || p.Name != "demo" || p.ID.IsZero() {
		t.Fatalf("by name: %+v %v", p, err)
	}
	if byID, ierr := f.FindProject(ctx, p.ID.String()); ierr != nil || byID.ID != p.ID {
		t.Fatalf("by id: %+v %v", byID, ierr)
	}
	for _, ref := range []string{"nope", "prj_01890a5d8a2b7c3d9e0f1a2b3c4d5e6f", "Demo"} {
		if _, nerr := f.FindProject(ctx, ref); !errs.Has(nerr, errs.NotFound) {
			t.Errorf("%s: want E_NOT_FOUND, got %v", ref, nerr)
		}
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, err := value.TextOf("demo")
	if err != nil {
		t.Fatal(err)
	}
	other, err := eng.Create(ctx, rosterStore, schema.TableProjects, map[string]value.Value{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	if _, cerr := f.FindProject(ctx, "demo"); !errs.Has(cerr, errs.ProjectNameCollision) {
		t.Fatalf("two demos: want E_PROJECT_NAME_COLLISION, got %v", cerr)
	}
	for _, id := range []string{p.ID.String(), other.Row.ID.String()} {
		if got, ierr := f.FindProject(ctx, id); ierr != nil || got.ID.String() != id {
			t.Errorf("by id %s: %+v %v", id, got, ierr)
		}
	}
}

// TestProjectName: the App is the profilestore.ProjectNamer the offboard,
// rejoin and actor show previews use; a prj_ id of an attached store gives
// its name, an unknown id and a bare name (labels ask by id only) are not
// found, so the caller shows the id.
func TestProjectName(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := rosterApp(t, folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal), false)
	var env profilestore.Store = a
	n, ok := env.(profilestore.ProjectNamer) // what upgrade.As sees at run time
	if !ok {
		t.Fatal("the profile Env is not a ProjectNamer")
	}
	p, err := a.FindProject(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got, found := n.ProjectName(ctx, p.ID.String()); !found || got != "demo" {
		t.Errorf("by id: %q %v", got, found)
	}
	for _, ref := range []string{"prj_01890a5d8a2b7c3d9e0f1a2b3c4d5e6f", "demo", ""} {
		if got, found := n.ProjectName(ctx, ref); found {
			t.Errorf("%q: want not found, got %q", ref, got)
		}
	}
}
