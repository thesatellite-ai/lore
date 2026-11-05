package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMachineLocalNeverMoves: a machine-local row cannot be moved to a
// store (by id alone or with its table named), and no shared row can be
// moved into the machine store; each is E_INVALID_INPUT, never
// E_NOT_FOUND, and nothing is written (P3-LOCAL-1).
func TestMachineLocalNeverMoves(t *testing.T) {
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
	if _, err = w.repo.Open(store.OpenOptions{}).Get(ctx, tableTestSessions, ses.Row.ID); err == nil {
		t.Fatal("machine-local row copied into the repo store")
	}
	if _, err = w.machine.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, prj.Row.ID); err == nil {
		t.Fatal("shared row moved into the machine store")
	}
}
