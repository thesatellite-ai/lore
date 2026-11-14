//go:build journeys && !windows

package journeys

// ci_attached_store_test.go — a CI job in a repo whose .lore/stores.yaml
// attaches a team store, and whose LORE.full.md was never committed (a
// repo migrated from lore 0.1.17 commits .lore/data and LORE.md only).
//
// devco O-7: before, render --check and doctor on the runner cloned the
// team store into the runner's HOME (~/.lore/stores/…) and fetched it: an
// internal store's rows written onto a CI machine, and a job with no key
// for the store's private repo could not pass. Now a CI read clones and
// fetches no store; one with no clone on the runner is left out.
// devco O-18: on a read-only HOME the same clone failed E_READ_ONLY_FS
// (render --check exit 23, doctor DEGRADED). Now both pass.
// devco O-8: render --check failed E_REFUSED on the missing LORE.full.md
// the repo never carried, and doctor read DEGRADED. Now it is a note that
// names `lore render && git add .lore/LORE.full.md`; a stale LORE.md
// still fails.
// devco O-9: doctor in a repo with no lore data created
// ~/.lore/state/machine.db on the runner. Now it creates no ~/.lore.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
)

func TestJourneyCIAttachedStoreUntrackedFull(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ana := w.Persona("Ana Brook", "ana@gearworks.example")
	lap := ana.Laptop()
	app := w.Remote("github.com/gearworks/pumpctl")
	plainApp := w.Remote("github.com/gearworks/plain")
	bare := filepath.Join(w.Root, "srv", "git", "gearworks-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)

	w.Step("day1/team-store-attached-full-file-left-out", func() {
		repo := lap.Clone(app, "pumpctl")
		lap.LoreIn(repo, "init", "--non-interactive", "--name=pumpctl").OK(t)
		lap.LoreIn(repo, "store", "init", "gearworks", bare, "--yes").OK(t).Says(t, "created store gearworks")
		lap.LoreIn(repo, "rule", "add", "--body", "never log a pump serial").OK(t)
		lap.LoreIn(repo, "render").OK(t)
		repo.Git("add", "-A")
		repo.Git("rm", "-q", "--cached", ".lore/LORE.full.md") // the file a migrated repo never committed
		repo.Git("commit", "-q", "-m", "lore: notes")
		repo.Git("push", "-q", "origin", "HEAD")
		if got := repo.Git("ls-files", ".lore/LORE.full.md"); got != "" {
			t.Fatalf("setup: LORE.full.md is tracked: %q", got)
		}
		if got := repo.Git("ls-files", ".lore/stores.yaml"); got == "" {
			t.Fatal("setup: .lore/stores.yaml is not committed")
		}
		plain := lap.Clone(plainApp, "plain")
		plain.WriteFile("README.md", "# no lore here\n")
		plain.CommitAll("docs: readme")
		plain.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/ci-reads-clone-no-store", func() {
		ci := ana.NewLaptop("runner")
		ci.SetEnv("CI", "true")
		ci.SetEnv("GITHUB_ACTIONS", "true")
		clone := ci.Clone(app, "pumpctl")
		storeClones := filepath.Join(ci.Home, ".lore", "stores")

		// O-7, O-8: writable HOME
		ci.LoreIn(clone, "render", "--check").OK(t).
			Says(t, "rendered files match their rows", ".lore/LORE.full.md", "git add .lore/LORE.full.md").
			Never(t, "E_REFUSED", "store clone", "git user set")
		ci.LoreIn(clone, "doctor").OK(t).Says(t, "Status: HEALTHY", "git add .lore/LORE.full.md").Never(t, "DEGRADED", "git user set")
		if _, err := os.Stat(storeClones); err == nil {
			t.Fatalf("a CI read cloned the attached store into the runner's HOME (%s)", storeClones)
		}

		// O-18: a HOME the job cannot write
		home := mustTree(t, ci.Home)
		readOnlyTree(t, ci.Home, true)
		ci.LoreIn(clone, "render", "--check").OK(t).Says(t, "rendered files match their rows").Never(t, "E_READ_ONLY_FS", "E_REFUSED")
		ci.LoreIn(clone, "doctor").OK(t).Says(t, "Status: HEALTHY").Never(t, "DEGRADED", "E_READ_ONLY_FS", "could not be opened")
		readOnlyTree(t, ci.Home, false)
		if diff := untouchedwalk.Diff(home, mustTree(t, ci.Home)); diff != "" {
			t.Fatalf("the CI reads wrote into the read-only home:\n%s", diff)
		}
	})

	w.Step("day2/ci-doctor-no-lore-data-writes-no-home", func() {
		ci := ana.NewLaptop("runner2")
		ci.SetEnv("CI", "true")
		plain := ci.Clone(plainApp, "plain")
		loreHome := filepath.Join(ci.Home, ".lore")
		if _, err := os.Stat(loreHome); err == nil {
			t.Fatalf("setup: the fresh runner already has %s", loreHome)
		}
		ci.LoreIn(plain, "check", "leaks", "--ci").OK(t)
		ci.LoreIn(plain, "render", "--check").OK(t).Says(t, "has no .lore folder")
		ci.LoreIn(plain, "doctor").OK(t).Says(t, "Status: HEALTHY")
		if _, err := os.Stat(loreHome); err == nil {
			t.Fatalf("a CI run in a repo with no lore data created %s", loreHome)
		}
	})

	w.Step("day3/stale-lore-md-still-fails", func() {
		repo := lap.Clones()[0]
		lap.LoreIn(repo, "rule", "add", "--body", "pumps restart at most twice an hour").OK(t)
		repo.Git("checkout", "-q", "--", ".lore/LORE.md") // the committed render, now behind the rows
		lap.LoreIn(repo.With("CI=true"), "render", "--check").Fails(t, 23, "E_REFUSED", ".lore/LORE.md is stale").Never(t, "LORE.full.md is missing")
	})
}
