//go:build stories && !windows

package stories

// O_team_store_test.go — the ownershape cells of a store kept to one
// roster team (devco D-727, story O). On the owner-shaped laptops the org
// keeps a second store, lore-secret, for team @secret only (`audience:
// team:@secret`). The admin reads and writes it; Mo, a member outside the
// team, never clones, fetches or hears of it (no clone dir, nothing in his
// org entry, no mention in whoami, doctor or sync); once the admin puts
// him on @secret his next sync brings it in and he reads and writes it.
// Every name, email and url is made up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryOTeamStore(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	secret := s.Remote("github.com/" + oOwner + "/lore-secret")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
	ada.LoreIn(pc, "team", "add", "--handle", "@secret", "--members", oAdminOrg.Email, "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)

	s.Step("D-727/admin-keeps-a-store-to-a-team-and-reads-writes-it", func() {
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", secret.SSHURL(), "--audience", "team:@secret", "--push", "--yes").OK(t).Says(t, "team:@secret")
		ada.LoreIn(pc, "store", "folder", "add", secret.SSHURL(), "secret").OK(t)
		r := ada.LoreIn(pc, "memory", "add", "--body", "SECRETNOTE the client deploys from the blue cluster", "--to", "lore-secret/secret", "--yes")
		oCheck(t, "D-727/admin-writes-team-store", r.Code == 0, "the admin's write into the team store was refused:\n%s", r)
		ada.LoreIn(pc, "sync").OK(t)
		r = ada.Lore(filepath.Dir(pc.Dir), "search", "blue cluster") // the setup's folder reads every store of the org entry
		oCheck(t, "D-727/admin-reads-team-store", r.Code == 0 && strings.Contains(r.Out(), "SECRETNOTE"), "the admin's search does not find the team store's note:\n%s", r)
	})

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mo.TraceGit(t) // every git process on Mo's machine, from his setup on
	_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email)
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-727/member-outside-the-team-never-touches-it", func() {
		var said []string
		for _, args := range [][]string{{"sync"}, {"whoami"}, {"doctor"}, {"search", "blue cluster"}} {
			if r := mo.LoreIn(mwc, args...); strings.Contains(r.Out(), "lore-secret") || strings.Contains(r.Out(), "SECRETNOTE") {
				said = append(said, strings.Join(args, " ")+":\n"+r.Out())
			}
			if r := mo.Lore(filepath.Dir(mwc.Dir), args...); strings.Contains(r.Out(), "lore-secret") || strings.Contains(r.Out(), "SECRETNOTE") {
				said = append(said, strings.Join(args, " ")+":\n"+r.Out())
			}
		}
		oCheck(t, "D-727/member-outside-team-no-mention", len(said) == 0, "a member outside @secret is told about the team store:\n%s", strings.Join(said, "\n"))
		// a clone found by its origin url anywhere under ~/.lore/stores
		// (the folder drops the host and follows the alias, so a guessed
		// path passes vacuously) and every git process of his (trace2)
		clones := mo.ClonesOfStore(secret)
		cfg, _ := os.ReadFile(mo.LorePath("config.yaml"))
		oCheck(t, "D-727/member-outside-team-no-clone", len(clones) == 0 && !strings.Contains(string(cfg), "lore-secret"), "a member outside @secret has the team store (clones %v) or it is in his org entry:\n%s", clones, cfg)
		touched := mo.GitTouched(t, secret)
		oCheck(t, "D-727/member-outside-team-no-fetch", len(touched) == 0, "a member outside @secret had git reach for the team store:\n%s", strings.Join(touched, "\n"))
	})

	s.Step("D-727/member-in-the-team-reads-writes-it", func() {
		ada.LoreIn(pc, "team", "member", "add", "--handle", "@secret", "--members", oMemOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		mo.LoreIn(mwc, "sync").OK(t)
		mo.LoreIn(mwc, "sync").OK(t)
		r := mo.Lore(filepath.Dir(mwc.Dir), "search", "blue cluster")
		oCheck(t, "D-727/member-in-team-reads", r.Code == 0 && strings.Contains(r.Out(), "SECRETNOTE"), "a member on @secret does not read the team store after his sync:\n%s", r)
		r = mo.LoreIn(mwc, "memory", "add", "--body", "member note for the client", "--to", "lore-secret/secret", "--yes")
		oCheck(t, "D-727/member-in-team-writes", r.Code == 0, "a member on @secret cannot write into the team store's folder:\n%s", r)
		// whoami and doctor name the team store beside the main one, with
		// who uses it (they named only the main store before)
		w := mo.LoreIn(mwc, "whoami")
		oCheck(t, "D-727/member-in-team-whoami-lists-team-store", strings.Contains(w.Out(), "lore-secret.git on branch main  (audience team:@secret)"), "a member on @secret does not see the team store with its audience in whoami:\n%s", w)
		d := mo.LoreIn(mwc, "doctor")
		oCheck(t, "D-727/member-in-team-doctor-lists-team-store", strings.Contains(d.Out(), "lore-secret (team:@secret)"), "a member on @secret does not see the team store with its audience in doctor:\n%s", d)
	})

	s.Step("D-727/member-leaves-the-team-told-once", func() {
		ada.LoreIn(pc, "team", "member", "remove", "--handle", "@secret", "--members", oMemOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		first := mo.LoreIn(mwc, "sync")
		oCheck(t, "D-727/member-left-team-note", first.Code == 0 && strings.Contains(first.Out(), "lore-secret: no longer read: audience team:@secret, you are not on it") &&
			strings.Contains(first.Out(), "lore store forget --url "), "the sync that drops the team store does not say so once:\n%s", first)
		again := mo.LoreIn(mwc, "sync")
		oCheck(t, "D-727/member-left-team-note-once", again.Code == 0 && !strings.Contains(again.Out(), "no longer read"), "the dropped team store is said again:\n%s", again)
	})
}
