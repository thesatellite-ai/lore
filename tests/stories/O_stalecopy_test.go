//go:build stories && !windows

package stories

// O_stalecopy_test.go — devco D-853 (hunt H-110, owner org-setup item 10)
// on the owner-shaped machine (story O): the org store stops answering
// (the machine is offline). Reads in the linked repo take the store's
// local copy, as they should, but search, ask, memory search and an empty
// answer's searched: line showed it with no word that the copy may be a
// day old. Now each names the store as unreachable with the date of the
// copy, --json carries it in "unreachable", and a sync that reaches the
// store again takes the line away.

import (
	"testing"
	"time"
)

func TestStoryOStaleCopySaysUnreachable(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	wc := ada.CloneTo(web, ada.OrgDir("web"))
	ada.LoreIn(wc, "org", "link", "--org", oOrg, "--project", oFolder, "--yes").OK(t)
	ada.LoreIn(wc, "memory", "add", "--body", "The vendor contract of web renews in march.", "--yes").OK(t)
	ada.LoreIn(wc, "sync").OK(t)

	const stale = ": unreachable, results from the copy of "
	s.Step("D-853/online-no-line", func() {
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "unreachable")
	})
	s.Step("D-853/offline-every-read-names-the-stale-copy", func() {
		// past live.fetch_ttl (the first read tries the store), within
		// link_index.max_age (the repo's link is still read from the copy)
		s.Advance(30 * time.Minute)
		ada.Offline()
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march", stale)
		ada.LoreIn(wc, "search", "--query", "vendor contract", "--json").OK(t).Says(t, `"unreachable":`, `"copy_of":`)
		ada.LoreIn(wc, "ask", "--query", "when does the vendor contract renew", "--store", oOrg+"/"+oFolder).OK(t).Says(t, "renews in march", stale)
		ada.LoreIn(wc, "memory", "search", "--query", "vendor").OK(t).Says(t, "renews in march", stale)
		ada.LoreIn(wc, "search", "--query", "kubernetes migration").OK(t).Says(t, "(no matches)", "  searched: ", stale)
		ada.LoreIn(wc, "search", "--query", "vendor contract", "--store", oOrg+"/"+oFolder).OK(t).Says(t, "renews in march", stale)
	})
	s.Step("D-853/back-online-the-line-goes", func() {
		ada.Online()
		ada.LoreIn(wc, "sync").OK(t)
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "unreachable")
	})
}
