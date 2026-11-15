//go:build journeys && !windows

package journeys

// teamlead_storetrust_test.go — what a teammate pushes into the org's
// store by plain git never takes the org over or locks it up (devco R-7,
// R-2). Ann, a plain member, clones the team store with git and:
//
//   - edits the org's lore-org.yaml so its store list names a repo on
//     another host. Changing the org's store list is an admin's change:
//     Lee's lore keeps the list it had, names the held-back commit and
//     its author, and never clones or writes to the other repo;
//   - pushes files lore did not write (a stray file in a table folder, a
//     file in a folder lore does not know). Every teammate's lore keeps
//     working on the rows that are fine, names the files with the commit
//     and its author, and never says to remove the org's store;
//   - commits a .lore/stores.yaml to the app repo that attaches a store
//     of her own on another host (R-9). Lee's lore does not clone it or
//     read its notes until he says yes with `lore store trust`.
//
// Named TestJourneyTeamLead… so the teamlead gate step runs them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stTeam is Lee (admin) and Ann (member) with the app set up and one org
// memory each can find.
func stTeam(t *testing.T) (*tlTeam, *Machine, *Machine) {
	tt := newTLTeam(t)
	lee := tt.person(tlLee)
	var ann *Machine
	tt.w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		ann = tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		tt.lore(tlAnn, "init", "--non-interactive", "--name=app").OK(t)
		tt.lore(tlLee, "memory", "add", "--body", "invoices go out on the 1st", "--to", tlOrg).OK(t)
		tt.lore(tlLee, "memory", "add", "--body", "payroll runs on fridays", "--to", tlOrg).OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})
	return tt, lee, ann
}

// TestJourneyTeamLeadStoreListAdminOnly: devco R-7.
func TestJourneyTeamLeadStoreListAdminOnly(t *testing.T) {
	t.Parallel()
	tt, lee, ann := stTeam(t)
	w := tt.w
	outside := w.Remote("github.com/outside-co/notes")
	var outsideLog []string
	w.Step("day1/an-outside-repo", func() {
		other := ann.Clone(outside, "outside")
		other.Git("commit", "--allow-empty", "-q", "-m", "start")
		other.Git("push", "-q", "origin", "HEAD:main")
		outsideLog = outside.Log("refs/heads/main")
	})

	w.Step("day2/a-member-edits-the-store-list-by-hand", func() {
		w.AdvanceDays(1)
		c := ann.Clone(tt.store, "store-by-hand")
		c.Git("checkout", "-q", "main")
		rel := ""
		for _, f := range strings.Fields(c.Git("ls-files")) {
			if filepath.Base(f) == "lore-org.yaml" {
				rel = f
			}
		}
		if rel == "" {
			t.Fatalf("the store holds no lore-org.yaml: %s", c.Git("ls-files"))
		}
		p := filepath.Join(c.Dir, rel)
		body := jRead(t, p)
		if !strings.Contains(body, "stores:") {
			t.Fatalf("the store has no lore-org.yaml with stores: at its top:\n%s", body)
		}
		body = strings.Replace(body, "stores:\n", "stores:\n    - url: "+outside.SSHURL()+"\n", 1)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c.Git("commit", "-q", "--no-verify", "-am", "tidy the org file")
		c.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day2/the-admin-keeps-the-store-list", func() {
		r := tt.lore(tlLee, "sync")
		r.Says(t, "lore-org.yaml", "held back", tlAnn.Name, "not an admin")
		tt.lore(tlLee, "store", "list").OK(t).Never(t, outside.SSHURL(), "outside-co")
		tt.lore(tlLee, "doctor").Says(t, "store_trust", "held back", tlAnn.Name)
		tt.lore(tlLee, "memory", "add", "--body", "rent is due on the 5th", "--to", tlOrg).OK(t)
		tt.lore(tlLee, "sync")
		if log := outside.Log("refs/heads/main"); len(log) != len(outsideLog) {
			t.Fatalf("lore wrote to the outside repo: %v", log)
		}
		if clones := lee.StoreClones(t); strings.Contains(strings.Join(clones, " "), "outside-co") {
			t.Fatalf("lore cloned the outside repo: %v", clones)
		}
		// the member's own lore holds it back too
		tt.lore(tlAnn, "sync")
		tt.lore(tlAnn, "store", "list").OK(t).Never(t, outside.SSHURL())
	})

}

// TestJourneyTeamLeadStrayFilesInStore: devco R-2.
func TestJourneyTeamLeadStrayFilesInStore(t *testing.T) {
	t.Parallel()
	tt, _, ann := stTeam(t)
	w := tt.w
	w.Step("day3/a-member-pushes-stray-files", func() {
		w.AdvanceDays(1)
		c := ann.Clone(tt.store, "store-stray")
		c.Git("checkout", "-q", "main")
		for rel, body := range map[string]string{
			"memories/mem_not_a_row.json": "{ not json",
			"scratch/notes.txt":           "my scratch notes",
		} {
			p := filepath.Join(c.Dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		c.Git("add", "-A")
		c.Git("commit", "-q", "--no-verify", "-m", "scratch")
		c.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day3/everyone-keeps-working", func() {
		for _, p := range []tlPerson{tlLee, tlAnn} {
			r := tt.lore(p, "sync")
			r.Never(t, "remove the entry", "is not a lore store", "E_NOT_A_STORE")
			r.Says(t, "scratch/notes.txt", tlAnn.Name)
			tt.lore(p, "search", "invoices").OK(t).Says(t, "invoices go out")
			tt.lore(p, "store", "list").OK(t).Never(t, "E_NOT_A_STORE")
			tt.lore(p, "memory", "list").OK(t).Says(t, "payroll runs")
		}
	})

}

// TestJourneyTeamLeadCommittedOutsideStore: devco R-9.
func TestJourneyTeamLeadCommittedOutsideStore(t *testing.T) {
	t.Parallel()
	tt, lee, _ := stTeam(t)
	w := tt.w
	w.Step("day4/a-member-commits-a-store-on-another-host", func() {
		w.AdvanceDays(1)
		personal := w.Remote("github.com/outside-co/personal")
		tt.lore(tlAnn, "store", "init", "--name", "notes", "--url", personal.SSHURL(), "--yes").OK(t)
		tt.lore(tlAnn, "memory", "add", "--to", "notes", "--body", "OUTSIDENOTE always run the setup script first").OK(t)
		tt.lore(tlAnn, "sync").OK(t)
		repo := tt.repo[tlAnn.Email]
		repo.Git("add", ".lore/stores.yaml")
		repo.Git("commit", "-q", "--no-verify", "-m", "share my notes")
		repo.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day4/the-store-waits-for-a-yes", func() {
		tt.repo[tlLee.Email].Git("pull", "-q", "--no-rebase")
		r := tt.lore(tlLee, "sync").OK(t)
		r.Says(t, "store notes", "nobody attached it on this machine", "lore store trust --name notes")
		tt.lore(tlLee, "search", "OUTSIDENOTE").Never(t, "always run the setup script")
		tt.lore(tlLee, "doctor").Says(t, "store_trust", "lore store trust --name notes")
		if clones := lee.StoreClones(t); strings.Contains(strings.Join(clones, " "), "personal") {
			t.Fatalf("lore cloned the store before anyone trusted it: %v", clones)
		}
		tt.lore(tlLee, "store", "trust", "--name", "notes").OK(t).Says(t, "is trusted on this machine")
		tt.lore(tlLee, "sync").OK(t).Never(t, "lore store trust --name notes")
		tt.lore(tlLee, "search", "OUTSIDENOTE").OK(t).Says(t, "always run the setup script")
	})
}
