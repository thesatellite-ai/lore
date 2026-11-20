//go:build stories && !windows

package stories

// GRID_live_moves_test.go — feature grid cells live x moves (GRID.md): a
// person in their own repo has open live tasks (on the hidden live ref, not
// committed) and one live task they deleted. A single `lore row move` and a
// bulk `lore move --from repo` must take the live copy of an open task (the
// target holds it once, the repo's live ref keeps only a tombstone) and
// must never move a deleted live row.

import (
	"strings"
	"testing"
)

// gridLiveRepo is a solo person's own repo on main with lore set up and
// pushed, so new tasks are live rows. It returns the machine and the clone.
func gridLiveRepo(s *Story) (*gridMachine, func(title string) string) {
	t := s.T
	m, h := blSolo(s)
	m.LoreIn(h, "init").OK(t)
	h.CommitAll("lore: adopt")
	h.Git("push", "-q", "origin", "HEAD:main")
	g := &gridMachine{m: m, h: h}
	g.personalStore(t)
	add := func(title string) string {
		id := idIn(t, blTaskID, m.LoreIn(h, "task", "add", "--title", title).OK(t))
		if !strings.Contains(g.liveTree(), id) {
			t.Fatalf("task %q (%s) is not on the live ref: the cell would prove nothing", title, id)
		}
		return id
	}
	return g, add
}

func TestStoryGRIDLiveSingleMove(t *testing.T) {
	s := New(t, "GRID")
	g, add := gridLiveRepo(s)
	open := add("rotate the staging certs")
	gone := add("renew the old domain")

	s.Step("live-x-move/deleted-live-row-is-never-moved", func() {
		g.lore("task", "delete", "--id", gone, "--yes").OK(t)
		r := g.lore("row", "move", "--id", gone, "--to", "me", "--yes")
		if r.Code == 0 {
			t.Errorf("a deleted live task was moved:\n%s", r.Out())
		}
		if strings.Contains(g.lore("task", "list", "--store", "me", "--json").Out(), gone) {
			t.Errorf("the deleted live task is in the personal store")
		}
	})

	s.Step("live-x-move/open-live-row-moves-once", func() {
		g.lore("row", "move", "--id", open, "--to", "me", "--yes").OK(t)
		if g.liveOpen(open) {
			t.Errorf("the moved task is still open on the repo's live ref")
		}
		g.lore("show", "--id", open).OK(t).Says(t, "rotate the staging certs").Never(t, "E_AMBIGUOUS_ID", "two live rows")
		if n := strings.Count(g.lore("task", "list", "--json").Out(), open); n > 1 {
			t.Errorf("the moved task is listed %d times", n)
		}
	})
}

func TestStoryGRIDLiveBulkMove(t *testing.T) {
	s := New(t, "GRID")
	g, add := gridLiveRepo(s)
	open := []string{add("archive the invoices"), add("check the backup restore")}
	gone := add("close the old tickets")

	s.Step("live-x-bulk/delete-one", func() {
		g.lore("task", "delete", "--id", gone, "--yes").OK(t)
		g.lore("task", "list").OK(t).Never(t, "close the old tickets")
	})

	s.Step("live-x-bulk/dry-run-lists-open-only", func() {
		r := g.lore("move", "--from", "repo", "--to", "me", "--where", "table=tasks", "--dry-run").OK(t)
		for _, id := range open {
			r.Says(t, id)
		}
		r.Never(t, gone)
	})

	s.Step("live-x-bulk/move-takes-live-copies-never-the-deleted", func() {
		r := g.lore("move", "--from", "repo", "--to", "me", "--where", "table=tasks", "--yes").OK(t)
		r.Never(t, gone)
		for _, id := range open {
			if g.liveOpen(id) {
				t.Errorf("the moved task %s is still open on the repo's live ref", id)
			}
		}
		mine := g.lore("task", "list", "--store", "me", "--json").OK(t).Out()
		for _, id := range open {
			if !strings.Contains(mine, id) {
				t.Errorf("the open live task %s did not reach the personal store:\n%s", id, mine)
			}
		}
		if strings.Contains(mine, gone) {
			t.Errorf("the deleted live task %s was moved to the personal store", gone)
		}
	})
}
