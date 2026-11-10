package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// TestActorRowCountsByWriter: 6.4's merge preview counts, per table, the
// live rows whose created_by is the actor, across the attached stores; an
// actor that wrote nothing has no counts.
func TestActorRowCountsByWriter(t *testing.T) {
	a := orgPeriodsApp(t)
	writer, err := a.Actor(background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.ActorRowCounts(background(), writer.Any())
	if err != nil {
		t.Fatal(err)
	}
	actors := -1
	for _, c := range got {
		if c.Table == schema.TableActors {
			actors = c.Rows
		}
	}
	// the fixture's three roster actors were written by this caller
	if actors != 3 {
		t.Fatalf("counts %+v: want actors 3", got)
	}
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.ActorRowCounts(background(), mira); err != nil || len(got) != 0 {
		t.Fatalf("an actor who wrote nothing: %+v, %v", got, err)
	}
}
