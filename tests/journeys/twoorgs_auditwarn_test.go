//go:build journeys && !windows

package journeys

// twoorgs_auditwarn_test.go — a write to another org's store from a repo
// linked in the first org (devco D-694 follow-up): the write guard reads
// the other org's roster through a second App, which took the command's
// audit sink; once closed, the command's own audit record failed and every
// such write printed "warning: the audit record of … will be added to the
// audit log by a later write: wire: the app is closed (W_AUDIT_DEFERRED)".

import (
	"path/filepath"
	"testing"
)

func TestJourneyWriteToAnotherOrgPrintsNoAuditWarning(t *testing.T) {
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

	w.Step("day1/lee-runs-two-orgs", func() {
		tlStartTeam(t, lee, leeApp, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{toAppFolder})
		tlStartTeam(t, lee, leeBeta, jSecondSetup, jSecondOrg, beta.SSHURL(), tlLee, []string{toBetaFolder})
	})

	w.Step("day1/d694-a-write-to-the-other-org-from-the-first-orgs-repo", func() {
		// the docs shape (docs/reference/flags.md): a lore project with both
		// orgs' stores attached, linked in the first org
		lee.Lore(leeApp, "init").OK(t)
		lee.Lore(leeApp, "store", "init", "--name", jSecondOrg, "--url", beta.SSHURL(), "--yes").OK(t)
		lee.Lore(leeApp, "org", "link", "--org", tlOrg, "--project", "app", "--yes").OK(t)
		lee.Lore(leeApp, "memory", "add", "--body", "The beta client pays in 30 days", "--to", jSecondOrg).
			OK(t).Never(t, "W_AUDIT_DEFERRED", "the app is closed", "audit record", "machine profile not refreshed")
	})
}
