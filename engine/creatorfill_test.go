package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestCreateFillsCreatorColumn (devco D-439): an add op that keeps
// created_by_actor_id (WithCreatorColumn) gets it filled with the writing
// actor when it could not set it itself (its actor row was not cached
// yet): rule rows written into a new store folder had it null. A write
// that does not ask keeps the column unset, as v0.1.17 rows are.
func TestCreateFillsCreatorColumn(t *testing.T) {
	t.Parallel()
	f := apSetup(t, false)
	fields := func() map[string]value.Value {
		return map[string]value.Value{"body": txt("use go 1.22"), "project_id": txt(f.project.String()), "activation": txt(schema.RuleActivationAlways), "severity": txt("must"), "source_kind": txt("manual")}
	}
	w, err := f.eng.Create(WithCreatorColumn(context.Background()), store.SourceRepo, schema.TableRules, fields())
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Row.Fields["created_by_actor_id"]; got == nil || got.String() != apAgent.String() {
		t.Fatalf("created_by_actor_id = %v, want the writer %s", got, apAgent)
	}
	w, err = f.eng.Create(context.Background(), store.SourceRepo, schema.TableRules, fields())
	if err != nil {
		t.Fatal(err)
	}
	if got, set := w.Row.Fields["created_by_actor_id"]; set {
		t.Fatalf("a write that did not ask filled created_by_actor_id: %v", got)
	}
}
