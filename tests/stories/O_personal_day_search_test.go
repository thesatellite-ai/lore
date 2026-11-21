//go:build stories && !windows

package stories

// O_personal_day_search_test.go — story O, devco D-804 (bug hunt H-62,
// the story step of D-796 / owner P-13): an owner keeps an old journal repo
// whose memories carry day metadata (--meta day=<date>) and adopts it into
// a folder of the personal store. `lore search --query "day:<date>"
// --store me` printed "(no matches)": a key:value metadata condition read
// the repo's rows only, never a store's, while the same rows' words were
// found. Here an owner-shaped laptop adopts such a journal into me/journal
// on a local bare repo and asks for a day from the home folder, from an
// org repo set up with lore and with --store me/journal: every place finds
// that day's note, labelled with its folder, and only that day's.

import (
	"strings"
	"testing"
)

// TestStoryOPersonalDaySearch: day:<date> finds the adopted journal's row
// of that day in the personal store, wherever it is asked (devco D-804).
func TestStoryOPersonalDaySearch(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	journal := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/old-journal"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	jc := ada.CloneTo(journal, ada.Code("old-journal"))
	wc := ada.CloneTo(web, ada.OrgDir("web"))

	s.Step("D-804/an-old-journal-with-day-metadata", func() {
		ada.LoreIn(jc, "init").OK(t)
		ada.LoreIn(jc, "memory", "add", "--body", "Met alice-ng about the orbit launch", "--meta", "day=2026-11-18").OK(t)
		ada.LoreIn(jc, "memory", "add", "--body", "Quiet day, read the orbit notes", "--meta", "day=2026-11-17").OK(t)
		jc.CommitAll("journal")
	})

	s.Step("D-804/adopt-it-into-the-personal-store", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "journal").OK(t)
		ada.LoreIn(jc, "store", "adopt", "--target", "me/journal", "--yes").OK(t)
		ada.LoreIn(wc, "init").OK(t)
	})

	s.Step("D-804/the-day-finds-the-adopted-row", func() {
		for _, c := range []struct {
			cell, dir, store string
		}{
			{"D-804/day-search-from-home", ada.Home, "me"},
			{"D-804/day-search-from-org-repo", wc.Dir, "me"},
			{"D-804/day-search-one-folder", ada.Home, "me/journal"},
		} {
			r := ada.Lore(c.dir, "search", "--query", "day:2026-11-18", "--store", c.store)
			out := r.Out()
			ok := r.Code == 0 && strings.Contains(out, "Met alice-ng about the orbit launch") &&
				strings.Contains(out, "(day: 2026-11-18)") && strings.Contains(out, "[me/journal]") &&
				!strings.Contains(out, "(no matches)") && !strings.Contains(out, "Quiet day")
			oCheck(t, c.cell, ok, "`lore search --query \"day:2026-11-18\" --store %s` in %s: want only the adopted journal's note of that day, labelled [me/journal]:\n%s", c.store, c.dir, r)
		}
	})
}
