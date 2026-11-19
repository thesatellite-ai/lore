//go:build journeys && !windows

package journeys

// fleet_links_origin_test.go — a fleet admin's health check over a setup
// folder (round 5 track O). Kit is kept elsewhere and only linked into the
// folder; a fleet run must count it, name it, and fix it (devco:O-1).
// Right after lore fix --all --migrate, doctor --all must count the
// migrated repos as checked (devco:O-16). A repo whose origin was moved
// away is DEGRADED in lore doctor and doctor --all with the git step
// (devco:O-19), and the "repo moved" follow-up lore repo set-url accepts a
// path remote (devco:O-13).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyFleetLinksAndDeadOrigins(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	admin := w.Persona("Fleet Admin", "fleet.admin@example.test")
	lap := admin.Laptop()
	var one, two, kit *teamharness.Clone
	var fleet, kitLink string

	w.Step("day1/two-repos-in-the-folder-one-linked-from-elsewhere", func() {
		fleet = filepath.Join(lap.Home, "fleet")
		one = lap.Clone(w.Remote("github.com/fleetco/one"), "one")
		two = lap.Clone(w.Remote("github.com/fleetco/two"), "two")
		kit = lap.Clone(w.Remote("github.com/fleetco/kit"), "kit")
		for _, c := range []*teamharness.Clone{one, two, kit} {
			fixOldRepo(t, c)
		}
		lap.MoveClone(t, one, filepath.Join(fleet, "one"))
		lap.MoveClone(t, two, filepath.Join(fleet, "two"))
		lap.MoveClone(t, kit, filepath.Join(lap.Home, "elsewhere", "kit"))
		kitLink = filepath.Join(fleet, "kit")
		if err := os.Symlink(kit.Dir, kitLink); err != nil {
			t.Fatal(err)
		}
		cfg := filepath.Join(lap.Home, ".lore", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(cfg), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte("setups:\n  - name: fleet\n    kind: solo\n    folders: [\""+fleet+"/**\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})

	w.Step("day1/fleet-runs-count-the-linked-repo", func() {
		lap.Lore(fleet, "doctor", "--all", "--verbose").OK(t).
			Says(t, "3 with lore data", kit.Dir+"  on the lore 0.1.17 format")
		lap.Lore(fleet, "fix", "--all", "--migrate").OK(t).
			Says(t, "== "+kit.Dir, "== "+one.Dir, "== "+two.Dir, "3 repo(s)")
	})

	w.Step("day1/doctor-all-right-after-fix-all-counts-them-checked", func() {
		lap.Lore(fleet, "doctor", "--all", "--verbose").OK(t).
			Says(t, one.Dir+"  checked", two.Dir+"  checked", "reached through the link "+kitLink, "lore setup edit --name fleet").
			Never(t, "lore has not run in it")
	})

	w.Step("day2/a-moved-origin-is-degraded-with-the-git-step", func() {
		w.AdvanceDays(1)
		gone := filepath.Join(lap.Home, "remotes", "one-gone.git")
		one.Git("remote", "set-url", "origin", gone)
		lap.LoreIn(one, "doctor").Fails(t, 1, "origin: no git repository at "+gone, "git remote set-url origin <new-url>")
		lap.Lore(fleet, "doctor", "--all").Fails(t, 1, one.Dir+"  origin: no git repository at "+gone)
		lap.LoreIn(one, "repo", "set-url", "--url", gone).OK(t).Says(t, "not an address lore records")
	})
}
