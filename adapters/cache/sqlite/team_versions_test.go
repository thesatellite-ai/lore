package sqlite

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// stamped is f.memory with a `_w {v, m}` stamp.
func (f *fixture) stamped(id, prj ids.AnyID, v string, m ids.MachineID) store.Row {
	f.t.Helper()
	stamp := f.clock.Now()
	w := must(origin.Stamp(v, m, m.IsZero()))
	return must(store.FromRow(row.Row{
		Table: schema.TableMemories, ID: id, HLC: stamp, CreatedAt: stamp.Wall, CreatedBy: f.actor,
		UpdatedAt: stamp.Wall, UpdatedBy: f.actor,
		Fields: map[string]value.Value{
			"project_id":  must(value.RefOf(prj)),
			"kind":        text(f.t, schema.MemoryKindCore),
			"body":        text(f.t, "body "+v),
			"source_kind": text(f.t, "test"),
		},
		Reserved: map[string]value.Value{origin.KeyWrite: w},
	}))
}

func TestTeamVersions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	m := must(ids.ParseMachine("mch_ab12" + strings.Repeat("0", 60)))
	a, b := newMem(), newMem()
	f.apply(c, store.SourceRepo, "1",
		f.stamped(a, prj, "0.1.18", m),
		f.stamped(b, prj, "0.1.19", m), // newer HLC: wins for (actor, machine)
		f.memory(newMem(), prj, "unstamped"),
	)
	f.apply(c, store.SourceRepo, "2", f.stamped(newMem(), prj, "0.1.18", ids.MachineID{}))
	got, err := c.TeamVersions(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 (machine and repo-only)", got)
	}
	byMachine := map[string]TeamVersion{}
	for _, tv := range got {
		byMachine[tv.Machine] = tv
		if tv.Actor != f.actor.String() || tv.LastSeen == "" {
			t.Errorf("row %+v", tv)
		}
	}
	if byMachine[m.String()].Version != "0.1.19" || byMachine[""].Version != "0.1.18" {
		t.Fatalf("versions %+v", byMachine)
	}
}
