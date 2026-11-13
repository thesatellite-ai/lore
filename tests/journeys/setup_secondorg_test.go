//go:build journeys && !windows

package journeys

// setup_secondorg_test.go — a second org on one laptop, started from a
// folder the first org's setup covers (B-13). Lee's admin setup covers all
// of ~; from ~ Lee saves a second admin setup's steps for another org. The
// team step used to fail E_SETUP_ORG_MISMATCH half-way (the org entry and
// its store clone written, the setup not saved): the folder's setup
// answered for the new org's roster writes although --setup named the
// other one.

import "testing"

// Names of the second-org journey (placeholders).
const (
	jSecondOrg   = "betaorg"
	jSecondSetup = "beta"
	jSecondSlug  = "github.com/beta-co/lore-beta"
	jSecondEmail = "lee@beta.example.test"
	jSecondAll   = "~/**"
)

func TestJourneySetupSecondOrgInFirstOrgsFolder(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	beta := w.Remote(jSecondSlug)

	w.Step("day1/first-org-covers-home", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{jSecondAll})
	})

	w.Step("day2/second-org-from-home", func() {
		tlFirstSteps(t, lee, lee.Home, jSecondSetup, "admin", tlPerson{Name: tlLee.Name, Email: jSecondEmail, Login: tlLee.Login})
		r := lee.Lore(lee.Home, "setup", "apply", jSecondSetup, "--step", "team", "--answers",
			`{"org":"`+jSecondOrg+`","store_url":"`+beta.SSHURL()+`"}`, "--yes")
		r.OK(t).Never(t, "E_SETUP_ORG_MISMATCH")
		lee.Lore(lee.Home, "setup", "list").OK(t).Says(t, jSecondSetup, jSecondOrg, tlLeadSetup, tlOrg)
	})
}
