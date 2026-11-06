package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// apStagedAgent is an agent no store holds before the test's batch.
var apStagedAgent = mustActor("act_dc2709b365b373e688fd52f62c6f5d99")

// TestAgentOwnerSeesStagedActorRow (G-ENGINE-6, g-orgmig finding): in a
// client store whose org keeps agent details internal, an agent's first
// command stages its actor row in the client store together with its first
// row. That first row and every later one are stamped the same way: the
// owner when the internal roster names one (the roster wins over the
// client projection), nobody when no store names an owner; never the
// agent's own id.
func TestAgentOwnerSeesStagedActorRow(t *testing.T) {
	cases := []struct {
		name  string
		agent ids.ID[ids.Actor]
		want  ids.ID[ids.Actor] // zero = nobody
	}{
		{"agent the internal roster owns", apAgent, apPerson},
		{"agent only in this batch", apStagedAgent, ids.ID[ids.Actor]{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := apSetup(t, true)
			call := call{caller: op.Caller{Actor: c.agent, Yes: true}}
			write := func(name string, withActor bool) {
				t.Helper()
				ctx := Staging(context.WithValue(context.Background(), callKey{}, call))
				if withActor { // the client projection of the agent, staged first
					fields := map[string]value.Value{schema.FieldActorKind: txt(schema.ActorKindAgent), "display_name": txt("bot")}
					if _, err := f.eng.CreateWithID(ctx, store.SourceRepo, schema.TableActors, c.agent.Any(), fields); err != nil {
						t.Fatal(err)
					}
				}
				w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMetaKeys, metaKeyFields(name))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = Commit(ctx); err != nil {
					t.Fatal(err)
				}
				_, got, err := f.eng.Get(context.Background(), Ref{ID: w.Row.ID})
				if err != nil {
					t.Fatal(err)
				}
				for slot, v := range map[string]ids.ID[ids.Actor]{"created_by": got.CreatedBy, "updated_by": got.UpdatedBy} {
					if v != c.want {
						t.Fatalf("%s write: %s = %q, want %q (the agent is %s)", name, slot, v, c.want, c.agent)
					}
				}
				if got.HLC.Actor == c.agent {
					t.Fatalf("%s write: the HLC names the agent", name)
				}
			}
			write("first_write", true)
			write("later_write", false)
		})
	}
}
