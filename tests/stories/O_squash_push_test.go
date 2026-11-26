//go:build stories && !windows

package stories

// O_squash_push_test.go — devco D-1000 (bug hunt H-255, store growth
// SG-4) on the owner-shaped machine (story O): every write was one commit
// on the team store's remote, so a busy store grew by ~600 commits a day.
// Now a teammate who works through the day (push manual, the default)
// sends 20 writes with one `lore sync` as ONE remote commit, the other
// admin sees all 20 rows, and an edit both made of one row is kept as a
// conflict as before (never hidden in a batch) while the rest goes out.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oSqRemoteCommits is the commit count of the team remote's main.
func oSqRemoteCommits(t *testing.T, r *teamharness.Remote) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", r.Dir, "rev-list", "--count", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list: %v\n%s", err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStoryOSquashPush(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	// mo is a second admin, so both write the company folder
	ada.LoreIn(pc, "member", "add", "--email", oMemOrg.Email, "--name", oMemOrg.Name, "--role", "admin", "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)
	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	oJoinSteps(t, mo, mwc.Dir, oOrg, oMemOrg)
	oJoin(t, mo, mwc.Dir, oOrg, "git@"+oAlias+":"+oOwner+"/lore-team.git").OK(t)
	// mo's first write also adds rows about mo and the machine (a roster
	// row keeps a commit of its own); it goes out before the count
	mo.Lore(mwc.Dir, "memory", "add", "--body", "widget day note: the first one", "--to", oOrg+"/org").OK(t)
	mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)

	s.Step("D-1000/twenty-writes-one-remote-commit", func() {
		for i := range 20 {
			mo.Lore(mwc.Dir, "memory", "add", "--body", fmt.Sprintf("widget day note %d: the export runs at noon", i), "--to", oOrg+"/org").OK(t)
		}
		before := oSqRemoteCommits(t, team)
		mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)
		if d := oSqRemoteCommits(t, team) - before; d != 1 {
			t.Fatalf("[O D-1000] 20 writes and one lore sync gave the team remote %d commits, want 1", d)
		}
		out, err := exec.Command("git", "--git-dir", team.Dir, "log", "-1", "--format=%s|%ae", "main").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "lore: batch 20 rows") {
			t.Fatalf("[O D-1000] the pushed commit is %q (%v), want one batch of the 20 rows", out, err)
		}
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(pc.Dir, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget day note 0:", "widget day note 19:")
	})

	s.Step("D-1000/a-clashing-edit-stays-a-conflict", func() {
		out := ada.Lore(pc.Dir, "memory", "add", "--body", "widget day note: both edit this", "--to", oOrg+"/org", "--json").OK(t).Out()
		id := regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out)
		if id == "" {
			t.Fatalf("no memory id in %s", out)
		}
		ada.LoreIn(pc, "sync").OK(t)
		mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)
		ada.Lore(pc.Dir, "memory", "edit", "--id", id, "--body", "widget day note: edited by ada").OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		mo.Lore(mwc.Dir, "--setup", oOrg, "memory", "edit", "--id", id, "--body", "widget day note: edited by mo").OK(t)
		mo.Lore(mwc.Dir, "memory", "add", "--body", "widget day note: one more", "--to", oOrg+"/org").OK(t)
		// as before the squash: the clash on the same field is kept as a
		// conflict (never hidden in a batch), the other row goes out
		r := mo.Lore(mwc.Dir, "sync", "--setup", oOrg)
		moOrg := filepath.Join(mo.Home, ".lore", "stores", oOwner, "lore-team", "org") // the org folder of mo's store clone
		if r.Code == 0 {
			t.Fatalf("[O D-1000] the clashing edit was not kept as a conflict: %s", r)
		}
		r.Says(t, "both edits are kept", id, "lore sync conflicts")
		mo.Lore(moOrg, "sync", "conflicts").OK(t).Says(t, id, "edited by ada", "edited by mo")
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(pc.Dir, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget day note: one more")
		mo.Lore(moOrg, "sync", "resolve", "--id", "1", "--take", "kept", "--yes").OK(t)
		mo.Lore(mwc.Dir, "sync", "--setup", oOrg).OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		a := ada.Lore(pc.Dir, "show", "--id", id).OK(t).Out()
		m := mo.Lore(mwc.Dir, "--setup", oOrg, "show", "--id", id).OK(t).Out()
		if !strings.Contains(a, "edited by mo") || !strings.Contains(m, "edited by mo") {
			t.Fatalf("[O D-1000] the resolved edit is not on both machines:\nada: %s\nmo: %s", a, m)
		}
		ada.Lore(pc.Dir, "memory", "list", "--store", oOrg).OK(t).Says(t, "widget day note: one more")
	})
}
