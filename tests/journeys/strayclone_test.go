//go:build journeys && !windows

package journeys

// strayclone_test.go — Sam renames the clone of their personal store
// under ~/.lore/stores by hand (A-16). The next lore write clones the
// store again at its path, and now says the renamed folder is another
// clone of the same store that lore no longer reads or writes.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyStrayStoreClone(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/app")
	w.Remote(soloStore)
	var repo *teamharness.Clone
	clone := filepath.Join(lap.Home, ".lore", "stores", "sam-dev", "lore-me")
	renamed := clone + "-old"

	w.Step("day1/setup-and-a-personal-note", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		repo = lap.Clone(app, "app")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "I like short commit subjects", "--to", "me").OK(t)
		if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
			t.Fatalf("no personal store clone at %s: %v", clone, err)
		}
	})

	w.Step("day2/rename-the-clone-folder", func() {
		if err := os.Rename(clone, renamed); err != nil {
			t.Fatal(err)
		}
		lap.LoreIn(repo, "memory", "add", "--body", "tabs, not spaces", "--to", "me").OK(t).
			Says(t, "lore-me-old is also a clone of this store", "lore reads and writes only", "then delete it")
	})
}
