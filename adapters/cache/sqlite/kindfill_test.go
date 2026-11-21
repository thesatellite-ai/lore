package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestKindRefillKeepsOwnKind (devco:D-794): the refill of a res_<kind>
// table reads every resource row of the cache, and each table keeps only
// the resources of its own kind. Two ways reach a refill: resources applied
// before their kind rows (a migrated or adopted folder), and a field added
// to a kind that already has rows. Before the fix both put every resource
// of the cache into each kind table.
func TestKindRefillKeepsOwnKind(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	person, project := must(ids.New[ids.Kind]()), must(ids.New[ids.Kind]())
	none := map[ids.ID[ids.KindField]][2]string{}
	// resources first, kinds after: every kind table is created (and so
	// refilled) with all resources already in the cache
	f.apply(c, store.SourceRepo, "r1",
		f.resource(must(ids.New[ids.Resource]()), person, "alice_ng", none),
		f.resource(must(ids.New[ids.Resource]()), person, "bob_li", none),
		f.resource(must(ids.New[ids.Resource]()), person, "cara_ok", none),
		f.resource(must(ids.New[ids.Resource]()), project, "orbit", none),
		f.resource(must(ids.New[ids.Resource]()), project, "nova", none))
	f.apply(c, store.SourceRepo, "k1", f.kindRow(person, "person"), f.kindRow(project, "project"))
	want := func(step string) {
		t.Helper()
		if n := count(t, c, `SELECT COUNT(*) FROM "res_person"`); n != 3 {
			t.Fatalf("%s: res_person has %d rows, want the 3 persons", step, n)
		}
		if n := count(t, c, `SELECT COUNT(*) FROM "res_project"`); n != 2 {
			t.Fatalf("%s: res_project has %d rows, want the 2 projects", step, n)
		}
		if n := count(t, c, `SELECT COUNT(*) FROM "res_`+person.String()+`" WHERE name IN ('orbit', 'nova')`); n != 0 {
			t.Fatalf("%s: the person kind's id view lists %d projects", step, n)
		}
	}
	want("kinds applied after their resources")
	// a field added to person changes the table, so it is refilled
	f.apply(c, store.SourceRepo, "k2", f.fieldRow(must(ids.New[ids.KindField]()), person, "role", "text", ids.ID[ids.Kind]{}))
	want("a field added to person")
}
