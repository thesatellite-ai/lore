package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestStampAsMapsActorFields pins D8/Q11 beyond the envelope: in a store
// with ActorAs (a client repo) every top-level field holding the writer's
// actor id — a Text field such as tasks.done_by, a Ref such as
// assigned_to_actor_id — names the mapped actor, other actors and other
// values are left alone, and the caller's field map is never mutated.
func TestStampAsMapsActorFields(t *testing.T) {
	t.Parallel()
	parse := func(s string) ids.ID[ids.Actor] {
		id, err := ids.Parse[ids.Actor](s)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	agent := parse("act_99b9c361cefa759ea234105c1ea7ef83")
	owner := parse("act_02638083ff857bcb918d37651f695705")
	other := parse("act_dc2709b365b373e688fd52f62c6f5d12")
	as := func(a ids.ID[ids.Actor]) ids.ID[ids.Actor] {
		if a == agent {
			return owner
		}
		return a
	}
	text := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ref := func(a ids.ID[ids.Actor]) value.Value {
		v, err := value.RefOf(a.Any())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name string
		in   value.Value
		want string
		kind value.Kind
	}{
		{"text field naming the writer", text(agent.String()), owner.String(), value.KindText},
		{"ref field naming the writer", ref(agent), owner.String(), value.KindRef},
		{"text field naming another actor", text(other.String()), other.String(), value.KindText},
		{"ref field naming another actor", ref(other), other.String(), value.KindRef},
		{"text that only contains the id", text("by " + agent.String()), "by " + agent.String(), value.KindText},
		{"plain text", text("fix rounding"), "fix rounding", value.KindText},
		{"empty text", text(""), "", value.KindText},
		{"a number", value.Int(88), "88", value.KindNumber},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := map[string]value.Value{"f": c.in}
			r := stampAs(Attached{ActorAs: as}, row.Row{CreatedBy: agent, UpdatedBy: agent, Fields: in})
			got := r.Fields["f"]
			if got.String() != c.want || got.Kind() != c.kind {
				t.Fatalf("got %s (%v), want %s (%v)", got.String(), got.Kind(), c.want, c.kind)
			}
			if in["f"] != c.in {
				t.Fatal("stampAs must not mutate the caller's field map")
			}
			if r.CreatedBy != owner || r.UpdatedBy != owner {
				t.Fatalf("envelope still maps: %s %s", r.CreatedBy, r.UpdatedBy)
			}
		})
	}
	// A store without ActorAs stamps nothing.
	in := map[string]value.Value{"done_by": text(agent.String())}
	if r := stampAs(Attached{}, row.Row{Fields: in}); r.Fields["done_by"].String() != agent.String() {
		t.Fatal("no ActorAs: the field is kept")
	}
}
