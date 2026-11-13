//go:build userscen

package userscen

import "testing"

// Two people retitle one live task while offline (bug hunt B2-8): the
// second sync merges both into the row with conflict markers. Before, every
// sync printed only the store lines and exited 0; now the sync that leaves
// the conflict names the row and its resolve number and exits E_STALE, and
// resolving it lets the next sync pass. `lore sync conflicts` shows both
// versions of the field (B2-23).
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
	r := b.Lore("sync")
	r.Fails(t, 23, "E_STALE", "conflict #1 live tasks/"+id, "lore sync resolve 1 --take kept|other", "lore sync conflicts")
	// B2-23: both versions of the clashing field, named by the side resolve keeps
	b.MustLore("sync", "conflicts").Says(t, "title kept (first):", "title other (second):", `"alice title"`, `"bob title"`)
	b.MustLore("sync", "resolve", "1", "--take", "other").Says(t, "resolved #1")
	b.MustLore("sync")
}
