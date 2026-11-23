package batch

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRosterIncidental pins devco:D-925: a create batch whose first write
// into a store made the writer's machine (or actor) row beside its notes
// keeps that roster row on undo; a batch of roster rows only undoes them.
func TestRosterIncidental(t *testing.T) {
	ev := func(table row.Table) Event { return Event{Table: table} }
	for _, c := range []struct {
		name string
		evs  []Event
		want bool
	}{
		{"notes and the machine row", []Event{ev(schema.TableMemories), ev(schema.TableMachines)}, true},
		{"notes and the actor row", []Event{ev(schema.TableActors), ev(schema.TableRules)}, true},
		{"roster rows only", []Event{ev(schema.TableActors), ev(schema.TableMachines)}, false},
		{"notes only", []Event{ev(schema.TableMemories)}, false},
	} {
		if got := rosterIncidental(c.evs); got != c.want {
			t.Errorf("%s: rosterIncidental = %v, want %v", c.name, got, c.want)
		}
	}
}
