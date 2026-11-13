//go:build journeys && !windows

package journeys

// staff_joinline_test.go — the join line member add prints works on the
// new person's fresh machine (B2-1). Lee adds Ann; the output names the
// org's store url; on Ann's fresh laptop the org's name alone is refused
// as an unknown org (E_JOIN_ORG_UNKNOWN, pointing at the store url), and
// the printed store url is accepted.

import (
	"testing"
)

func TestJourneyStaffJoinLine(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	url := tt.store.SSHURL()

	w.Step("day1/member-add-prints-the-store-url", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, url, tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", tlAnn.Name, "--role", "member", "--yes").OK(t).
			Says(t, "added "+tlAnn.Email, "joins on their machine with: lore setup join --invite "+url+" ").
			Never(t, "lore setup join --invite <org>")
	})

	w.Step("day1/ann-on-a-fresh-laptop", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		ann.Lore(dir, "setup", "join", tlOrg, "--no-open").
			Refused(t, "E_JOIN_ORG_UNKNOWN", "no org named", "store url").
			Never(t, "E_INVITE_INVALID")
		ann.Lore(dir, "setup", "join", url, "--no-open", "--json").OK(t).Says(t, url)
	})
}
