//go:build stories && !windows

package stories

// GRID_helpers_test.go — the small machine wrapper the feature grid cells
// (GRID.md, GRID_*_test.go) share: one person's machine and the clone the
// cell works in.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// gridMachine is one person's laptop and the clone a grid cell runs lore in.
type gridMachine struct {
	m *journeys.Machine
	h *teamharness.Clone
}

// lore runs lore in the cell's clone.
func (g *gridMachine) lore(args ...string) teamharness.Result { return g.m.LoreIn(g.h, args...) }

// personalStore makes the person's local personal store `me`, the target
// of the grid's moves.
func (g *gridMachine) personalStore(t testing.TB) {
	t.Helper()
	g.lore("store", "init", "--name", "me").OK(t)
}

// liveTree lists the files on the clone's local copy of the live ref ("" when
// there is none yet).
func (g *gridMachine) liveTree() string {
	r := g.h.TryGit("ls-tree", "-r", "--name-only", "refs/lore/live")
	if r.Code != 0 {
		return ""
	}
	return r.Stdout
}

// liveOpen reports whether the clone's live ref holds an open (not
// tombstoned) copy of row id. A move leaves a tombstone, which is how the
// old place learns the row left; only an open copy is a row left behind.
func (g *gridMachine) liveOpen(id string) bool {
	for _, f := range strings.Fields(g.liveTree()) {
		if !strings.Contains(f, id) {
			continue
		}
		if b := g.h.TryGit("show", "refs/lore/live:"+f); b.Code == 0 && !strings.Contains(strings.ReplaceAll(b.Stdout, " ", ""), `"_deleted":true`) {
			return true
		}
	}
	return false
}

// fileOpen reports whether the clone's work tree holds an open (not
// tombstoned) row file rel.
func (g *gridMachine) fileOpen(rel string) bool {
	b, ok := g.h.ReadFile(rel)
	return ok && !strings.Contains(strings.ReplaceAll(b, " ", ""), `"_deleted":true`)
}
