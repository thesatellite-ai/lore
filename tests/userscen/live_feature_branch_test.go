//go:build userscen

package userscen

// live_feature_branch_test.go — landed live rows on a feature branch
// (owner decision 2026-11-08: keep the behaviour, say it). Rows that land
// on main are read from origin/main only while the checkout is on main;
// on any other branch they drop out of this checkout's lists once they
// leave live, until main is merged. So a write that lands rows while HEAD
// is on another branch must say so on stderr, and the same write on main
// must not.
//
// Matrix: checkout branch (main, feature) × the line on stderr; on the
// feature branch, the rows show again after `git merge origin/main`.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestLiveLand_FeatureBranchSaysWhenRowsShow(t *testing.T) {
	scenParallel(t)
	for _, tc := range []struct {
		branch string
		says   bool
	}{{"main", false}, {"feature", true}} {
		t.Run(tc.branch, func(t *testing.T) {
			w := newWorld(t)
			app := w.Remote(syncSlugApp)
			p := w.Human("alice", "alice@example.com")
			p.WriteConfig(th.Config{Live: th.FreshFetch()})
			c := p.Clone(app, "app")
			c.MustLore("init", "--non-interactive", "--name=app")
			c.CommitAll("lore")
			c.AdminPush("origin", "main")
			if tc.branch != "main" {
				c.Git("checkout", "-q", "-b", tc.branch)
			}
			tag := trashID(t, c.MustLore("tag", "add", "--name", "ftag", "--json"))
			note := "landed on main; this checkout is on " + tc.branch + ", so those rows show here after you merge main"
			var landed, said bool
			var ids []string
			for i, title := range []string{"one", "two", "three"} {
				id := trashID(t, c.MustLore("architecturenote", "add", "--title", "arch "+title, "--body", "body "+title, "--json"))
				r := c.MustLore("comment", "add", "--on-table", "architecture_notes", "--on-id", id, "--body", "comment "+title, "--json")
				ids = append(ids, trashID(t, r))
				r.Never(t, "landed on main") // a write never lands by itself (U-2)
				s := c.MustLore("sync")      // landing waits for sync (U-2)
				landed = landed || strings.Contains(s.Out(), "landed on main (")
				said = said || strings.Contains(s.Out(), note)
				if strings.Contains(r.Out(), "this checkout is on") {
					t.Fatalf("write %d: a branch note on a write that landed nothing:\n%s", i, r.Out())
				}
				if strings.Contains(s.Out(), "this checkout is on") && !tc.says {
					t.Fatalf("sync %d on %s: a branch note on the default branch:\n%s", i, tc.branch, s.Out())
				}
			}
			if !landed {
				t.Fatal("no comment add landed on main: the scenario did not reach the land it tests")
			}
			if said != tc.says {
				t.Fatalf("on %s: stderr said %q = %v, want %v", tc.branch, note, said, tc.says)
			}
			if !tc.says {
				return
			}
			// The line's promise: what left live shows after merging main.
			c.MustLore("live", "show")
			c.Git("merge", "-q", "--no-edit", "origin/main")
			c.MustLore("tag", "list").Says(t, "ftag")
			c.MustLore("show", tag).Never(t, "E_NOT_FOUND")
			for _, id := range ids {
				c.MustLore("show", id).Never(t, "E_NOT_FOUND")
			}
		})
	}
}
