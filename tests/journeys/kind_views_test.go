//go:build journeys && !windows

package journeys

// kind_views_test.go — a person keeps people, projects and companies as
// resources and reads them through the per-kind views (devco:D-794). A
// kind table is refilled from every resource of the cache when its shape
// changes (a field added, a migrated or adopted folder whose resources
// arrive before their kinds); before the fix that refill put every
// resource into res_person, res_project and res_company alike, so
// `lore sql` on a per-kind view listed the whole store.

import (
	"strings"
	"testing"
)

func TestJourneyKindViewsKeepTheirKind(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mia := w.Persona("Mia Torres", "mia@larkfield.example")
	lap := mia.Laptop()
	repo := lap.Clone(w.Remote("github.com/larkfield/orbit"), "orbit")

	counts := func(step string) {
		t.Helper()
		out := lap.LoreIn(repo, "sql", "--query",
			"SELECT (SELECT COUNT(*) FROM res_person) || '/' || (SELECT COUNT(*) FROM res_project) || '/' || (SELECT COUNT(*) FROM res_company) AS n").OK(t).Stdout
		if !strings.Contains(out, "3/2/1") {
			t.Errorf("%s: res_person/res_project/res_company counts, want 3/2/1:\n%s", step, out)
		}
	}

	w.Step("day1/people-projects-companies", func() {
		lap.LoreIn(repo, "init").OK(t)
		for _, k := range []string{"person", "project", "company"} {
			lap.LoreIn(repo, "resource", "kind", "add", "--kind", k).OK(t)
		}
		for _, p := range []string{"alice_ng", "bob_li", "cara_ok"} {
			lap.LoreIn(repo, "resource", "add", "--kind", "person", "--name", p).OK(t)
		}
		lap.LoreIn(repo, "resource", "add", "--kind", "project", "--name", "orbit").OK(t)
		lap.LoreIn(repo, "resource", "add", "--kind", "project", "--name", "nova").OK(t)
		lap.LoreIn(repo, "resource", "add", "--kind", "company", "--name", "acme_co").OK(t)
		counts("day1")
	})

	w.AdvanceDays(1)
	w.Step("day2/a-field-added-refills-only-its-kind", func() {
		lap.LoreIn(repo, "resource", "field", "add", "--kind", "person", "--field", "role", "--type", "text").OK(t)
		lap.LoreIn(repo, "resource", "field", "add", "--kind", "project", "--field", "status", "--type", "text").OK(t)
		counts("day2")
		lap.LoreIn(repo, "sql", "--query", "SELECT name FROM res_person ORDER BY name").OK(t).
			Says(t, "alice_ng", "bob_li", "cara_ok").Never(t, "orbit", "nova", "acme_co")
	})
}
