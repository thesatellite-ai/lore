//go:build stories && !windows

package stories

// O_alias_memberjoin_test.go — devco D-644 and D-645 on the owner-shaped
// machine (ownershape.go, story O): a member the admin added with `member
// add` joins on a fresh machine by the store url in the org account's ssh
// alias spelling (git@<alias>:<owner>/<repo>.git), the first org entry on
// that machine:
//
//	D-644: `setup apply --step join` failed E_INTERNAL "org import of
//	       git@<alias>:… made no org entry" after writing the entry (url in
//	       the github.com spelling) and the store clone, the setup not
//	       saved; the re-run said "pass --as <another name>", a flag setup
//	       apply does not have. Now the join finds the entry its import
//	       made, a refused join leaves the home as it was, and no hint
//	       names a flag the command lacks.
//	D-645: the setup name defaulted to the store url's GitHub owner and
//	       join said "name from the roster of <owner>": it is the org's
//	       name (the store's lore-org.yaml), and join says so.

import (
	"strings"
	"testing"
)

// oHomeOutsideState is oHomeFiles without state/ and machine.json (this
// machine's id and machine-local rows such as a setup's progress, never an
// org entry, a clone or a store).
func oHomeOutsideState(t testing.TB, m *OwnerMachine) string {
	t.Helper()
	var keep []string
	for _, l := range strings.Split(oHomeFiles(t, m), "\n") {
		if !strings.HasPrefix(l, "state/") && !strings.HasPrefix(l, "machine.json ") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func TestStoryOAliasMemberJoin(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	aliasURL := "git@" + oAlias + ":" + oOwner + "/lore-team.git"

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))

	// devco:D-644 (refused): a join of someone the roster does not have,
	// on a home with no org entry and no clone yet, leaves every file of
	// the home as it was (no org entry, no clone, no personal store) but
	// the setup's own progress row in state/ (the refused step's check,
	// which setup list shows)
	s.Step("D-644/refused-member-join-writes-nothing", func() {
		stranger := oMemOrg
		stranger.Email = "nobody@acme.example"
		oJoinSteps(t, mo, mwc.Dir, "acme-stranger", stranger)
		before := oHomeOutsideState(t, mo)
		r := oJoin(t, mo, mwc.Dir, "acme-stranger", aliasURL)
		if r.Code == 0 {
			t.Fatalf("a join of an email not on the roster was saved: %s", r)
		}
		r.Never(t, "--as ")
		if after := oHomeOutsideState(t, mo); after != before {
			t.Errorf("a refused join changed the home:\nbefore\n%s\nafter\n%s", before, after)
		}
	})

	// devco:D-655: the join plan (`setup join --invite <url>`, before any
	// apply) reads the roster to prefill the name; that read imported the
	// org and took the entry back but kept the store clone under stores/.
	// A plan that is never applied leaves every file of the home as it was,
	// state/ and machine.json included.
	s.Step("D-655/join-plan-leaves-home-byte-identical", func() {
		before := oHomeFiles(t, mo)
		mo.Lore(mwc.Dir, "setup", "join", "--invite", aliasURL, "--no-open").OK(t).Says(t, "roster of "+oOrg)
		if after := oHomeFiles(t, mo); after != before {
			t.Errorf("a join plan that was never applied changed the home:\nbefore\n%s\nafter\n%s", before, after)
		}
	})

	// devco:D-645: the setup the join lines make is named for the org, not
	// for the store url's owner, and the prefill note names the org
	s.Step("D-645/join-setup-name-is-the-org", func() {
		r := mo.Lore(mwc.Dir, "setup", "join", "--invite", aliasURL, "--no-open").OK(t)
		r.Says(t, "--setup "+oOrg+" ", "roster of "+oOrg).Never(t, "--setup "+oOwner, "roster of "+oOwner)
		if !strings.Contains(r.Out(), "setup name "+oOrg) {
			t.Errorf("join does not say where the setup name comes from:\n%s", r.Out())
		}
	})

	// devco:D-644: the alias join is done in one run: the entry its import
	// made (url in the github.com spelling) is found, the setup is saved
	s.Step("D-644/alias-member-join-in-one-run", func() {
		oJoinSteps(t, mo, mwc.Dir, oOrg, oMemOrg)
		r := oJoin(t, mo, mwc.Dir, oOrg, aliasURL)
		r.Never(t, "made no org entry", "--as ").OK(t)
		mo.Lore(mo.Home, "setup", "list").OK(t).Says(t, oOrg+"  member").Never(t, "not saved yet: "+oOrg+" ")
		// a re-run of the finished step picks up the entry, never "--as"
		oJoin(t, mo, mwc.Dir, oOrg, aliasURL).Never(t, "--as ", "already exists").OK(t)
	})
}
