package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// tableTestSessions is a machine-local table of these tests only (the real
// built-in machine-local tables land with their nouns, P3-LOCAL-3).
const tableTestSessions row.Table = "test_sessions"

// machineWorld is an engine with the repo store, a read-only `agency`
// store and the machine store, over a registry with one machine-local
// table.
type machineWorld struct {
	eng     *Engine
	repo    *memstore.Data
	machine *memstore.Data
	cache   *fakeCache
}

func newMachineWorld(t *testing.T, attachMachine bool) machineWorld {
	t.Helper()
	defs := append(schema.Builtin(), schema.Table(string(tableTestSessions), ids.PrefixSession, schema.MachineLocal(),
		schema.Field("tool", value.KindText, schema.Required())))
	reg, err := schema.NewRegistry(defs...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	w := machineWorld{repo: memstore.NewData(), machine: memstore.NewData(), cache: newFakeCache()}
	opts := []Option{
		WithStore(Attached{Name: store.SourceRepo, Backend: w.repo.Open(store.OpenOptions{})}),
		WithCache(w.cache), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{}),
	}
	if attachMachine {
		opts = append(opts, WithMachineStore(Attached{Name: store.SourceMachine, Backend: w.machine.Open(store.OpenOptions{})}))
	}
	if w.eng, err = New(opts...); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestMachineLocalRoutesThroughTheEnginePath: a machine-local row written
// with any target lands only in the machine store, is stamped and cached
// like any row, and is found, listed, edited and deleted there; the
// machine store is never one of Stores() (P3-LOCAL-1).
func TestMachineLocalRoutesThroughTheEnginePath(t *testing.T) {
	ctx := context.Background()
	w := newMachineWorld(t, true)
	if slices.Contains(w.eng.Stores(), store.SourceMachine) {
		t.Fatalf("Stores() = %v lists the machine store", w.eng.Stores())
	}
	for _, target := range []store.Source{"", store.SourceRepo, store.SourceMachine} {
		wr, err := w.eng.Create(ctx, target, tableTestSessions, map[string]value.Value{"tool": txt("codex")})
		if err != nil {
			t.Fatalf("create (target %q): %v", target, err)
		}
		if wr.Row.ID.Prefix() != ids.PrefixSession || wr.Row.HLC.IsZero() || wr.Row.CreatedBy != testActor {
			t.Fatalf("not stamped: %+v", wr.Row)
		}
		if _, err = w.repo.Open(store.OpenOptions{}).Get(ctx, tableTestSessions, wr.Row.ID); err == nil {
			t.Fatalf("machine-local row %s written into the repo store", wr.Row.ID)
		}
		if _, err = w.machine.Open(store.OpenOptions{}).Get(ctx, tableTestSessions, wr.Row.ID); err != nil {
			t.Fatalf("machine-local row not in the machine store: %v", err)
		}
	}
	rows, err := w.eng.List(ctx, store.SourceRepo, tableTestSessions)
	if err != nil || len(rows) != 3 {
		t.Fatalf("List = %d rows, %v; want 3", len(rows), err)
	}
	src, got, err := w.eng.Get(ctx, Ref{ID: rows[0].ID})
	if err != nil || src != store.SourceMachine {
		t.Fatalf("Get = %s, %v; want the machine store", src, err)
	}
	if _, err = w.eng.Update(ctx, Ref{ID: got.ID}, map[string]value.Value{"tool": txt("claude")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err = w.eng.Delete(ctx, Ref{ID: got.ID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rows, err = w.eng.List(ctx, store.SourceRepo, tableTestSessions); err != nil || len(rows) != 2 {
		t.Fatalf("List after delete = %d rows, %v; want 2", len(rows), err)
	}
}

// TestMachineStoreRefusesSharedTables: a shared row never lands in the
// machine store, and a machine-local table without a machine store is
// E_NOT_IMPLEMENTED, never a silent write elsewhere.
func TestMachineStoreRefusesSharedTables(t *testing.T) {
	ctx := context.Background()
	w := newMachineWorld(t, true)
	_, err := w.eng.Create(ctx, store.SourceMachine, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("shared table into the machine store: err = %v, want E_INVALID_INPUT", err)
	}
	none := newMachineWorld(t, false)
	_, err = none.eng.Create(ctx, "", tableTestSessions, map[string]value.Value{"tool": txt("codex")})
	if !errs.Has(err, errs.NotImplemented) {
		t.Fatalf("no machine store: err = %v, want E_NOT_IMPLEMENTED", err)
	}
	if rows, _, rerr := none.repo.Open(store.OpenOptions{}).Read(ctx, ""); rerr != nil || len(rows) != 0 {
		t.Fatalf("repo store got %d rows, %v; want none of the session", len(rows), rerr)
	}
}
