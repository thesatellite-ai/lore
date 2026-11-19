//go:build journeys && !windows

package journeys

// twoorgs_secondself_test.go — an admin of a second org under another
// address writes to that org's store from a repo linked in the first org
// (devco D-694 follow-up). The write guard judged the caller as the
// command's first-org identity, so the second org's roster had no such
// admin: the write was refused as a non-admin's and the refusal named the
// person themselves as the admin to ask.

import (
	"path/filepath"
	"testing"
)

func TestJourneyAdminOfSecondOrgUnderAnotherEmailWritesThere(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeApp := tt.repo[tlLee.Email].Dir
	beta := w.Remote(jSecondSlug)
	betaApp := w.Remote(toBetaAppSlug)
	c := lee.Clone(betaApp, "beta")
	leeBeta := filepath.Join(lee.Home, "code", "beta")
	lee.MoveClone(t, c, leeBeta)

	w.Step("day1/lee-runs-two-orgs-with-two-emails", func() {
		tlStartTeam(t, lee, leeApp, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{toAppFolder})
		tlStartTeam(t, lee, leeBeta, jSecondSetup, jSecondOrg, beta.SSHURL(), tlPerson{Name: tlLee.Name, Email: jSecondEmail, Login: tlLee.Login}, []string{toBetaFolder})
	})

	w.Step("day1/d694-the-guard-judges-the-second-orgs-own-identity", func() {
		lee.Lore(leeApp, "init").OK(t)
		lee.Lore(leeApp, "store", "init", "--name", jSecondOrg, "--url", beta.SSHURL(), "--yes").OK(t)
		lee.Lore(leeApp, "org", "link", "--org", tlOrg, "--project", "app", "--yes").OK(t)
		lee.Lore(leeApp, "rule", "add", "--body", "Invoices go out on the first", "--severity", "must", "--to", jSecondOrg, "--yes").
			OK(t).Never(t, "only an admin", "ask an admin", "E_REFUSED")
		lee.Lore(leeApp, "policy", "set", "--key", "branches.shared", "--value", "main", "--to", jSecondOrg, "--yes").
			OK(t).Never(t, "only an admin", "ask an admin", "E_REFUSED")
	})
}
