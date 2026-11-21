//go:build stories && !windows

package stories

// O_linkunknown_test.go — devco D-860 (hunt H-128, follow-up of H-110)
// on the owner-shaped machine (story O): a repo linked to an org folder by
// its remote (`lore org link`, no lore data of its own) is offline for
// longer than link_index.max_age. The copy of the store is then too old
// to decide the link by, so the folder is not read (fail closed, an owner
// decision whether it should be), but search, ask, memory search, an
// empty answer and doctor said nothing about it: the answer looked like
// the folder held nothing. Each now names the folder as "link unknown",
// says it was not searched and how to get it back (text and --json
// link_unknown), and a sync that reaches the store takes the line away.

import (
	"testing"
	"time"
)

func TestStoryOLinkUnknownSaysNotSearched(t *testing.T) {
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

	unknown := oOrg + "/" + oFolder + ": link unknown (offline since "
	const notSearched = "not searched; reconnect, then run `lore sync`"
	s.Step("D-860/online-no-line", func() {
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "link unknown")
	})
	s.Step("D-860/offline-past-max-age-every-read-names-the-dropped-link", func() {
		// past link_index.max_age (1 h): the copy no longer decides the
		// link, so the folder is not read, and every read says so
		s.Advance(2 * time.Hour)
		ada.Offline()
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, unknown, "link index older than 1h", notSearched)
		ada.LoreIn(wc, "search", "--query", "vendor contract", "--json").OK(t).Says(t, `"link_unknown":`, `"since":`, `"max_age": "1h"`)
		ada.LoreIn(wc, "ask", "--query", "when does the vendor contract renew").OK(t).Says(t, unknown, notSearched)
		ada.LoreIn(wc, "memory", "search", "--query", "vendor").OK(t).Says(t, unknown, notSearched)
		// a repo with no lore data refuses context; it names the dropped
		// link instead of telling a linked repo to lore init / org link
		ada.LoreIn(wc, "context", "--for-prompt", "vendor contract").Refused(t, unknown, notSearched).Never(t, "lore org link", "lore init")
		ada.LoreIn(wc, "doctor").Says(t, unknown, notSearched)
	})
	s.Step("D-860/back-online-the-line-goes", func() {
		ada.Online()
		ada.LoreIn(wc, "sync").OK(t)
		ada.LoreIn(wc, "search", "--query", "vendor contract").OK(t).Says(t, "renews in march").Never(t, "link unknown")
	})
}
