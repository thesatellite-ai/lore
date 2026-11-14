//go:build journeys && !windows

package journeys

// staff_joinline_test.go — the join line member add prints works on the
// new person's fresh machine (B2-1). Lee adds Ann; the output names the
// org's store url; on Ann's fresh laptop the org's name alone is refused
// as an unknown org (E_JOIN_ORG_UNKNOWN, pointing at the store url), and
// the printed store url is accepted. Run with no terminal (B-10), the
// printed join line answers with the `lore setup apply` lines of a member
// setup named after the store's owner, never a page that waits 30 idle
// minutes nor a "solo setup personal"; running those lines joins.

import (
	"encoding/json"
	"strings"
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
		tt.lore(tlLee, "sync").OK(t) // C5: the roster write reaches the team store with Lee's sync
	})

	w.Step("day1/ann-on-a-fresh-laptop", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		ann.Lore(dir, "setup", "join", tlOrg, "--no-open").
			Refused(t, "E_JOIN_ORG_UNKNOWN", "no org named", "store url").
			Never(t, "E_INVITE_INVALID")
		ann.Lore(dir, "setup", "join", url, "--no-open", "--json").OK(t).Says(t, url)
	})

	w.Step("day1/ann-joins-without-a-terminal", func() {
		ann := tt.m[tlAnn.Email]
		dir := tt.repo[tlAnn.Email].Dir
		const setup = "acme-co" // the store url's owner (github.com/acme-co/lore-team)
		ann.Lore(dir, "setup", "join", "--invite", url).OK(t).
			Says(t, "lore setup apply --setup "+setup+" --step kind --answers '{\"kind\":\"member\",\"name\":\""+setup+"\"}'",
				"--step join --answers '{\"invite\":\""+url+"\"}'").
			Never(t, "lore setup is open at", "idle minutes", "solo setup")
		var out struct {
			Commands []string `json:"commands"`
		}
		if err := json.Unmarshal([]byte(ann.Lore(dir, "setup", "join", "--invite", url, "--json").OK(t).Stdout), &out); err != nil || len(out.Commands) < 4 {
			t.Fatalf("setup join --json: commands %v (%v)", out.Commands, err)
		}
		for _, line := range out.Commands {
			ann.Lore(dir, append(joinLineArgs(t, line), "--yes")...).OK(t)
		}
		ann.Lore(dir, "setup", "list").OK(t).Says(t, setup, "member", tlOrg)
	})
}

// joinLineArgs splits one printed `lore setup apply … --answers '<json>'`
// line into lore's arguments (the answers JSON single-quoted, as a shell
// reads it).
func joinLineArgs(t testing.TB, line string) []string {
	t.Helper()
	head, quoted, ok := strings.Cut(line, " --answers ")
	if !ok || !strings.HasPrefix(head, "lore setup apply ") || len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
		t.Fatalf("not a lore setup apply line: %q", line)
	}
	answers := strings.ReplaceAll(quoted[1:len(quoted)-1], `'\''`, "'")
	return append(strings.Fields(strings.TrimPrefix(head, "lore ")), "--answers", answers)
}
