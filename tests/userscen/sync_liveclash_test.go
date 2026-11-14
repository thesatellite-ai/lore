//go:build userscen

package userscen

import (
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Two people retitle one live task while offline (bug hunt B2-8, devco
// D-219 / L-19). The second sync settles the clash and says so: it names
// the row, that the newer edit was kept and the title it replaced. It never
// writes conflict markers into the shared live ref (they reached every
// clone and failed the sync of people who never touched the row), so both
// syncs exit 0, `lore sync conflicts` is empty and both clones show the
// newer title.
func TestSyncTeam_LiveTitleClashIsReportedBySync(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	id := st.addTask(t, a, "draft title")
	b.MustLore("task", "list")
	for i, title := range []string{"alice title", "bob title"} {
		st.people[i].Offline()
		st.clones[i].MustLore("task", "edit", id, "--title", title)
	}
	st.people[0].Online()
	a.MustLore("sync")
	st.people[1].Online()
	b.MustLore("sync").Says(t, id, "both sides edited title", "kept the newer edit", `title was "alice title"`).Never(t, "conflict markers")
	b.MustLore("sync", "conflicts").Says(t, "no conflicts")
	a.MustLore("sync")
	for _, c := range []*th.Clone{a, b} {
		c.MustLore("task", "show", id).Says(t, "bob title").Never(t, "<<<<<<<", "alice title")
	}
}
