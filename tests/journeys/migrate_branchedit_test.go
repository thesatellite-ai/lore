//go:build journeys && !windows

package journeys

// migrate_branchedit_test.go — a teammate's edit made on lore 0.1.17 on a
// branch survives the branch being rebased, merged or cherry-picked across
// `lore migrate run` on main (E-4). Bea archives three rules on three
// branches with 0.1.17 (each rule tagged, so the migration's tags step
// sets its tag_ids); Olga migrates main and pushes; Bea upgrades and
// brings each branch over the migration one way: each rule stays archived.
// Before, the migration stamped every tagged row with Olga's name and the
// run's time, and the merge driver saw the v1 file's "archived_at": null
// and the migrated file's missing key as two edits, so the newer stamp won
// and the archive was dropped ("both sides changed archived_at").

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestJourneyMigrateKeepsBranchEdits(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	repo := w.Remote("github.com/acme-mb/app")
	bc := bea.Clone(repo, "app")
	rulesDir := filepath.Join(bc.Dir, ".lore", "data", "rules")
	ruleIDs := func() []string {
		ents, _ := os.ReadDir(rulesDir)
		var out []string
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".json") {
				out = append(out, strings.TrimSuffix(e.Name(), ".json"))
			}
		}
		slices.Sort(out)
		return out
	}
	modes := []string{"rebase", "merge", "cherry-pick"}
	ids := map[string]string{}

	w.Step("day1/three-rules-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "tag", "add", "--name", "review").OK(t)
		for _, m := range modes {
			before := ruleIDs()
			bea.Old(bc, "rule", "add", "--body", "never skip review on "+m).OK(t)
			for _, id := range ruleIDs() {
				if !slices.Contains(before, id) {
					ids[m] = id
				}
			}
			if ids[m] == "" {
				t.Fatalf("setup: no rule file for %s", m)
			}
			// tagged: the migration folds the link into tag_ids, the step
			// that stamped the row as a new edit
			bea.Old(bc, "tag", "attach", "--tag", "review", "--on-table", "rules", "--on-id", ids[m]).OK(t)
		}
		fpCommitPush(t, bc, "lore data")
	})

	w.Step("day2/bea-archives-on-branches", func() {
		w.AdvanceDays(1)
		for _, m := range modes {
			bc.Git("checkout", "-q", "-b", "feat-"+m, "main")
			bea.Old(bc, "rule", "archive", ids[m]).OK(t)
			bc.CommitAll("archive the " + m + " rule")
			bc.Git("checkout", "-q", "main")
		}
	})

	oc := olga.Clone(repo, "app")
	w.Step("day3/olga-migrates-main", func() {
		w.AdvanceDays(1)
		olga.LoreIn(oc, "migrate", "run").OK(t)
		fpCommitPush(t, oc, "lore: migrate rows to format 2")
	})

	w.Step("day4/bea-upgrades-and-brings-each-branch-over", func() {
		w.AdvanceDays(1)
		bea.Upgrade()
		bc.Git("fetch", "-q", "origin")
		check := func(m string) {
			t.Helper()
			b, err := os.ReadFile(filepath.Join(rulesDir, ids[m]+".json"))
			if err != nil {
				t.Fatalf("%s: %v", m, err)
			}
			doc := string(b)
			if !strings.Contains(doc, `"archived_at": "`) {
				t.Fatalf("%s across the migration dropped the archive:\n%s", m, doc)
			}
			if !fpNewRow.MatchString(doc) {
				t.Fatalf("%s left the rule at _v: 1 beside the migrated rows:\n%s", m, doc)
			}
		}
		for _, m := range modes {
			var r = bc.TryGit("status")
			switch m {
			case "rebase":
				bc.Git("checkout", "-q", "feat-rebase")
				r = bc.TryGit("rebase", "origin/main")
			case "merge":
				bc.Git("checkout", "-q", "feat-merge")
				r = bc.TryGit("merge", "--no-edit", "origin/main")
			case "cherry-pick":
				bc.Git("checkout", "-q", "-B", "pick", "origin/main")
				r = bc.TryGit("cherry-pick", "feat-cherry-pick")
			}
			if r.Code != 0 {
				t.Fatalf("git %s: %s", m, r)
			}
			if strings.Contains(r.Stderr, "kept the newer edit") {
				t.Fatalf("git %s: the merge driver dropped a side: %s", m, r.Stderr)
			}
			check(m)
		}
	})
}
