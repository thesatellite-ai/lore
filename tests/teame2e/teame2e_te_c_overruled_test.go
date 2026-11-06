//go:build teame2e

package teame2e

// te-c delete overruled by an edit (later 525, OV63; DELETION 3.7): dev1
// moves a row to the trash while dev2 edits the same row. When the two
// meet in a real merge the edit wins, the row stays alive with a
// delete_overruled note, the merge says so, and `lore trash list
// --overruled` lists the row on both clones while the trash does not.
// Branch rows meet in `git pull` through lore merge-driver; live rows meet
// in `lore sync`.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teCOverruledIDs runs trash list --overruled --json in c and returns the
// ids it lists.
func teCOverruledIDs(t *testing.T, c *th.Clone, extra ...string) []string {
	t.Helper()
	r := c.MustLore(append([]string{"trash", "list", "--json"}, extra...)...)
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("trash list --json: %v %s", err, r)
	}
	var out []string
	for _, it := range v.Data {
		out = append(out, it.ID)
	}
	return out
}

// teCListed reports whether id is in ids.
func teCListed(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestTeC_OverruledBranchRowThroughMergeDriver: a rule (a branch row).
func TestTeC_OverruledBranchRowThroughMergeDriver(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("overruled", "trash", "list"))
	rid := mustID(t, rt.c1.MustLore("rule", "add", "--body", "contested rule", "--no-live"), "rul")
	rt.c1.CommitAll("rule")
	rt.c1.AdminPush("origin", "main")
	rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")

	// dev2 edits first, on the branch only; dev1 then trashes the row
	// (a later stamp) and pushes. A delete never beats an edit it did not
	// see, whichever stamp is higher (3.7).
	rt.c2.With(autopushOff).MustLore("rule", "edit", rid, "--body", "contested rule, still needed", "--no-live")
	rt.c2.CommitAll("edit the rule")
	rt.c1.With(autopushOff).MustLore("rule", "delete", rid, "--reason", "obsolete", "--yes")
	rt.c1.CommitAll("trash the rule")
	rt.c1.AdminPush("origin", "main")

	pull := rt.c2.TryGit("pull", "-q", "--no-rebase", "--no-edit", "origin", "main")
	if pull.Code != 0 {
		t.Fatalf("delete versus edit must merge cleanly through lore merge-driver: %s", pull)
	}
	if !strings.Contains(pull.Out(), "kept alive") {
		t.Errorf("the merge must say the delete lost to the edit: %s", pull)
	}
	rt.c2.MustLore("rule", "show", rid).Says(t, "still needed")
	if body, _ := rt.c2.ReadFile(ruleFile(rid)); !strings.Contains(body, "delete_overruled") {
		t.Fatalf("the merged row must carry delete_overruled: %s", body)
	}
	rt.c2.AdminPush("origin", "main")
	rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

	for name, c := range map[string]*th.Clone{"dev1": rt.c1, "dev2": rt.c2} {
		if !teCListed(teCOverruledIDs(t, c, "--overruled"), rid) {
			t.Errorf("%s: trash list --overruled must list %s", name, rid)
		}
		if teCListed(teCOverruledIDs(t, c), rid) {
			t.Errorf("%s: the kept-alive rule must not be in the trash", name)
		}
	}
}

// TestTeC_OverruledLiveRowThroughSync: a task (a live row).
func TestTeC_OverruledLiveRowThroughSync(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("overruled", "trash", "list"))
	id := rt.addTask(t, rt.c1, "contested task")
	rt.c2.MustLore("task", "list")
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--title", "contested task, edited by dev2")
	rt.c1.MustLore("task", "delete", id, "--reason", "duplicate", "--yes")
	out := rt.c2.MustLore("sync")
	if !strings.Contains(out.Stdout+out.Stderr, "kept alive") {
		t.Errorf("sync must say the delete lost to the edit: %s", out)
	}
	rt.c1.MustLore("sync")
	for name, c := range map[string]*th.Clone{"dev1": rt.c1, "dev2": rt.c2} {
		c.MustLore("task", "show", id).Says(t, "edited by dev2")
		if !teCListed(teCOverruledIDs(t, c, "--overruled"), id) {
			t.Errorf("%s: trash list --overruled must list %s", name, id)
		}
		if teCListed(teCOverruledIDs(t, c), id) {
			t.Errorf("%s: the kept-alive task must not be in the trash", name)
		}
	}
}
