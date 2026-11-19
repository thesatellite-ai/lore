package content

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// rootEnv is an Env at the root of an org store's clone: Project refuses
// the way wire's read refusal does there (engine.ErrNoPlaceProject in the
// chain). Every other method is the nil Env's (never called here).
type rootEnv struct{ Env }

func (rootEnv) Project(context.Context) (ids.ID[ids.Project], error) {
	return ids.ID[ids.Project]{}, errs.Wrap(errs.NotFound, "store acme has no project row yet, and a read never makes one", engine.ErrNoPlaceProject)
}

// otherEnv refuses Project for any other reason.
type otherEnv struct{ Env }

func (otherEnv) Project(context.Context) (ids.ID[ids.Project], error) {
	return ids.ID[ids.Project]{}, errs.New(errs.NotFound, "no lore project here")
}

// TestProjectFilterAtStoreRoot (devco D-505): a list or search of a
// content noun with no --project at a place with no project of its own
// keeps every project's rows ("") instead of refusing, as memory list
// does there; any other Project refusal still refuses.
func TestProjectFilterAtStoreRoot(t *testing.T) {
	ctx := context.Background()
	got, err := Ops{Env: rootEnv{}, Noun: "pattern"}.ProjectFilter(ctx, "")
	if err != nil || got != "" {
		t.Fatalf("ProjectFilter at a store root = %q, %v; want every project (\"\", nil)", got, err)
	}
	if _, err := (Ops{Env: otherEnv{}, Noun: "pattern"}).ProjectFilter(ctx, ""); !errs.Has(err, errs.NotFound) {
		t.Fatalf("ProjectFilter with another Project refusal = %v; want it refused", err)
	}
}
