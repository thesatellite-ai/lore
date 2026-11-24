//go:build stories && !windows

package stories

// BL_sync_promote_merge_test.go — lore sync promote --to <branch> merges a
// repo note's copy with the target branch's newer edits through the same
// field merge as lore branch promote (#155 class, H-207, devco:D-948).

import (
	"testing"
)

// TestStoryBLSyncPromoteKeepsTargetEdits (#155 class, devco:D-948): repo
// notes reworded on a feature branch, then archived, given metadata and a
// newer body on main after the branch was cut. lore sync promote --to
// main brings the branch's wording in and keeps main's archive and
// metadata; the body both sides changed refuses with E_LAND_CONFLICT
// naming the row and field, main unmoved, and the hint's --take branch
// promote runs as printed. The old promote committed the checkout's whole
// file over main's and said only "committed 1 file(s)".
func TestStoryBLSyncPromoteKeepsTargetEdits(t *testing.T) {
	s := New(t, "BL")
	app := s.Seed(s.Remote("github.com/promote-co/ledger"), func(dir string) { PlainRepo(t, dir) })
	dev := s.Person("Dana Dev", "dana@promote-co.test")
	c := dev.Clone(app, "ledger")
	var m1, m2 string
	s.Step("day1/notes-on-main", func() {
		dev.LoreIn(c, "init").OK(t)
		m1 = idIn(t, blMemID, dev.LoreIn(c, "memory", "add", "--body", "pelican invoices export nightly").OK(t))
		m2 = idIn(t, blMemID, dev.LoreIn(c, "memory", "add", "--body", "heron receipts export hourly").OK(t))
		c.CommitAll("lore: notes")
	})
	s.Step("day1/reworded-on-the-branch", func() {
		c.Git("switch", "-q", "-c", "feat/wording")
		dev.LoreIn(c, "memory", "edit", "--id", m1, "--body", "pelican invoices export nightly (branch wording)").OK(t)
		dev.LoreIn(c, "memory", "edit", "--id", m2, "--body", "heron receipts export hourly (branch wording)").OK(t)
		c.CommitAll("lore: branch wording")
	})
	s.Step("day1/changed-on-main-after-the-cut", func() {
		c.Git("switch", "-q", "main")
		dev.LoreIn(c, "memory", "archive", "--id", m1, "--yes").OK(t)
		dev.LoreIn(c, "meta", "set", "--id", m2, "--pair", "team_owner=billing").OK(t)
		dev.LoreIn(c, "memory", "edit", "--id", m2, "--body", "heron receipts export every 30 minutes").OK(t)
		c.CommitAll("lore: main edits")
	})
	onMain := func(check func()) {
		t.Helper()
		c.Git("switch", "-q", "main")
		check()
		c.Git("switch", "-q", "feat/wording")
	}
	s.Step("day1/only-the-branch-reworded-merges-in", func() {
		c.Git("switch", "-q", "feat/wording")
		dev.LoreIn(c, "sync", "promote", "--ids", m1, "--to", "main").OK(t).
			Says(t, "committed 1 file(s) onto main", "1 merged field by field with newer edits on main")
		onMain(func() {
			dev.LoreIn(c, "show", "--id", m1, "--json").OK(t).Says(t, `"archived_at"`, "(branch wording)")
		})
	})
	var before string
	s.Step("day1/the-same-text-refuses", func() {
		before = c.Git("rev-parse", "main")
		dev.LoreIn(c, "sync", "promote", "--ids", m2, "--to", "main").
			Refused(t, "E_LAND_CONFLICT", m2+" (body)", "nothing was committed", "lore sync promote --ids "+m2+" --to main --take branch")
		if got := c.Git("rev-parse", "main"); got != before {
			t.Fatalf("a refused promote moved main: %s -> %s", before, got)
		}
		onMain(func() {
			dev.LoreIn(c, "memory", "show", "--id", m2).OK(t).Says(t, "every 30 minutes").Never(t, "hourly (branch wording)")
		})
	})
	s.Step("day1/the-printed-take-branch-runs", func() {
		dev.LoreIn(c, "sync", "promote", "--ids", m2, "--to", "main", "--take", "branch").OK(t).Says(t, "1 settled by --take")
		onMain(func() {
			dev.LoreIn(c, "memory", "show", "--id", m2).OK(t).Says(t, "hourly (branch wording)")
			dev.LoreIn(c, "meta", "get", "--id", m2).OK(t).Says(t, "team_owner", "billing")
		})
	})
}
