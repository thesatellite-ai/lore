package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMachineLocalNeverMoves: a machine-local row cannot be moved to a
// store (by id alone or with its table named), and no shared row can be
// moved into the machine store; each is E_INVALID_INPUT and nothing is
// written (P3-LOCAL-1). A machine-local id with no live row answers as a
// lookup does (G-CORE-2).
func TestMachineLocalNeverMoves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newMachineWorld(t, true)
	ses, err := w.eng.Create(ctx, "", tableTestSessions, map[string]value.Value{"tool": txt("codex")})
	if err != nil {
		t.Fatal(err)
	}
	prj, err := w.eng.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		ref Ref
		to  store.Source
	}{
		"machine row by id":    {Ref{ID: ses.Row.ID}, store.SourceRepo},
		"machine row by table": {Ref{Table: tableTestSessions, ID: ses.Row.ID}, store.SourceRepo},
		"shared row into it":   {Ref{ID: prj.Row.ID}, store.SourceMachine},
	} {
		if _, merr := w.eng.Move(ctx, c.ref, c.to); !errs.Has(merr, errs.InvalidInput) {
			t.Errorf("%s: err = %v, want E_INVALID_INPUT", name, merr)
		}
	}
	// G-CORE-2: the rule is said only for a live row; an id naming no row
	// is E_NOT_FOUND and a trashed one E_IN_TRASH, as every lookup answers.
	missing, err := ids.ParseAny(string(ses.Row.ID.Prefix()) + "_01a1a5aca7f57659be7320c5f4b7f00d")
	if err != nil {
		t.Fatal(err)
	}
	if _, merr := w.eng.Move(ctx, Ref{ID: missing}, store.SourceRepo); !errs.Has(merr, errs.NotFound) {
		t.Errorf("missing machine id: err = %v, want E_NOT_FOUND", merr)
	}
	gone, err := w.eng.Create(ctx, "", tableTestSessions, map[string]value.Value{"tool": txt("codex")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.eng.Delete(ctx, Ref{ID: gone.Row.ID}); err != nil {
		t.Fatal(err)
	}
	if _, merr := w.eng.Move(ctx, Ref{ID: gone.Row.ID}, store.SourceRepo); !errs.Has(merr, errs.InTrash) && !errs.Has(merr, errs.NotFound) {
		t.Errorf("trashed machine row: err = %v, want E_IN_TRASH or E_NOT_FOUND", merr)
	}
	if _, err = w.repo.Open(store.OpenOptions{}).Get(ctx, tableTestSessions, ses.Row.ID); err == nil {
		t.Fatal("machine-local row copied into the repo store")
	}
	if _, err = w.machine.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, prj.Row.ID); err == nil {
		t.Fatal("shared row moved into the machine store")
	}
}
