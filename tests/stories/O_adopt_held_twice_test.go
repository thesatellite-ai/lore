//go:build stories && !windows

package stories

// O_adopt_held_twice_test.go — story O, devco D-939 (bug hunt H-200, a
// follow-up of H-195 / D-934): a plain `lore store adopt` (no
// --remove-source) into a folder of a store the repo attaches whole left
// every adopted row live twice, so each command by id on one answered
// E_AMBIGUOUS_ID and doctor was DEGRADED (copied_id), while adopt said
// nothing about it. Its dry run and its run now count those rows and print
// the one command that settles them all; that command, run exactly as
// printed, leaves no copied_id and no actor_roster warning (it used to
// take the actor row the repo's project row was saved by).

import (
	"regexp"
	"strings"
	"testing"
)

// oSettleLine is the settle command adopt prints at the end of its
// held-twice line.
var oSettleLine = regexp.MustCompile(`(?m)(lore store adopt --target \S+ --remove-source --yes)$`)

func TestStoryOAdoptSaysWhatItLeavesHeldTwice(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing-app"))
	notes := s.Remote("github.com/" + oOwner + "/notes")
	const folder = "invoices"
	settle := "lore store adopt --target notes/" + folder + " --remove-source --yes"
	var id string

	s.Step("D-939/adopt-says-what-it-leaves-held-twice", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		ada.Lore(bc.Dir, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		ada.Lore(bc.Dir, "store", "folder", "add", "--store", "notes", "--folder", folder).OK(t)
		r := ada.Lore(bc.Dir, "memory", "add", "--body", "The invoice queue drains every ten minutes.", "--to", "repo", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		ada.Lore(bc.Dir, "decision", "add", "--title", "Queue in postgres", "--body", "One table, no broker.", "--to", "repo").OK(t)
		bc.CommitAll("lore: invoice notes")
		ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder, "--dry-run").OK(t).
			Says(t, "3 rows would stay live in", "held twice with notes/"+folder, "E_AMBIGUOUS_ID", settle)
		ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder, "--yes").OK(t).
			Says(t, "3 rows now held twice", "notes/"+folder, "copied_id", settle)
		ada.Lore(bc.Dir, "show", "--id", id).Refused(t, "E_AMBIGUOUS_ID")
		ada.Lore(bc.Dir, "doctor").Says(t, "copied_id: 3 row(s)")
	})

	s.Step("D-939/the-printed-command-settles-them", func() {
		r := ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder).OK(t) // nothing new to write: the run answers with the count again
		m := oSettleLine.FindStringSubmatch(r.Out())
		if m == nil || m[1] != settle {
			t.Fatalf("adopt printed no settle command (want %q):\n%s", settle, r.Out())
		}
		ada.Lore(bc.Dir, strings.Fields(strings.TrimPrefix(m[1], "lore "))...).OK(t).
			Says(t, "removed 3 row files").Never(t, "held twice")
		ada.Lore(bc.Dir, "show", "--id", id).OK(t).Says(t, "ten minutes")
		ada.Lore(bc.Dir, "lint", "--code", "copied_id").OK(t).Never(t, "copied_id "+id)
		ada.Lore(bc.Dir, "doctor").Never(t, "copied_id", "actor_roster")
		ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder, "--dry-run").OK(t).Never(t, "held twice")
	})
}
