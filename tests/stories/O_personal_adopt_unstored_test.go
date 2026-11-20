//go:build stories && !windows

package stories

// O_personal_adopt_unstored_test.go — story O, devco D-762 (owner P-4, bug
// hunt H-21): moving a code repo's notes into a personal store,
// `lore store adopt --target me/<folder> --secret-unstored` redacted the
// key it found but added no store-then-rotate task, though its help says it
// does; the run only listed "a store-then-rotate task failed" under "left in
// the repo". The task step ran in the adopt's engine of the folder alone,
// which asked for "this repo's project" (there is no repo there) and then
// named a folder project row nobody had made. The same scan's refusal in a
// bulk `lore move --from repo --to me/<folder>` printed reruns with
// --secret-unstored, --secret-ref and --not-a-secret, and `lore move`
// refused each as an unknown flag. Here one owner-shaped laptop keeps a
// personal store on a local bare repo; both answers write the redacted rows
// and their tasks in the target folder, read from home.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestStoryOPersonalAdoptUnstoredTask: adopt and bulk move into a personal
// store folder with --secret-unstored add the store-then-rotate task there
// (devco D-762).
func TestStoryOPersonalAdoptUnstoredTask(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	code := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/keys-app"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	// built at run time: no key-shaped literal in the repo
	keyA := "AKIA" + strings.Repeat("Q", 4) + "7XKD2M4N5P6R"
	keyB := "AKIA" + strings.Repeat("Z", 4) + "3WJH8B9C2V4T"
	wc := ada.CloneTo(code, ada.Code("keys-app"))
	const rotated = "then rotate it" // the store-then-rotate task's title

	// plant writes a repo memory holding word, then swaps word for key in
	// its row file by hand (no lore write: an editor or an old lore did it).
	plant := func(word, key string) {
		t.Helper()
		ada.LoreIn(wc, "memory", "add", "--body", "staging token "+word, "--to", "repo").OK(t)
		paths, _ := filepath.Glob(filepath.Join(wc.Dir, ".lore", "data", "memories", "*.json"))
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), word) {
				if err := os.WriteFile(p, []byte(strings.Replace(string(b), word, key, 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("no repo memory holds %q", word)
	}
	tasks := func(folder string) teamharness.Result {
		return ada.Lore(ada.Home, "task", "list", "--store", "me/"+folder)
	}

	s.Step("D-762/personal-store-and-a-repo-holding-a-key", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		for _, f := range []string{"notes", "moved"} {
			ada.Lore(ada.Home, "store", "folder", "add", "me", f).OK(t)
		}
		ada.LoreIn(wc, "init", "--non-interactive", "--name=keys-app").OK(t)
		plant("zerkelmotha", keyA)
	})

	s.Step("D-762/adopt-secret-unstored-adds-the-rotate-task", func() {
		ada.LoreIn(wc, "store", "adopt", "--target", "me/notes", "--yes").
			Refused(t, "E_NEEDS_CONFIRM", "lore store adopt --target me/notes --yes --secret-unstored").Never(t, keyA)
		ada.LoreIn(wc, "store", "adopt", "--target", "me/notes", "--yes", "--secret-unstored").OK(t).
			Says(t, "memories 1").Never(t, keyA, "store-then-rotate task failed")
		tasks("notes").OK(t).Says(t, rotated, "[me/notes]")
		// the stored note holds the placeholder, never the key
		ada.Lore(ada.Home, "memory", "list", "--store", "me/notes").OK(t).Says(t, "${secret:unstored}").Never(t, keyA)
	})

	s.Step("D-762/bulk-move-runs-its-printed-secret-unstored-rerun", func() {
		plant("zerkelmothb", keyB)
		rerun := "lore move --from repo --to me/moved --where 'table=memories' --yes --secret-unstored"
		ada.LoreIn(wc, "move", "--from", "repo", "--to", "me/moved", "--where", "table=memories", "--yes").
			Refused(t, "E_NEEDS_CONFIRM", rerun, "--not-a-secret --reason").Never(t, keyA, keyB)
		ada.LoreIn(wc, "move", "--from", "repo", "--to", "me/moved", "--where", "table=memories", "--yes", "--secret-unstored").OK(t).Never(t, keyA, keyB)
		tasks("moved").OK(t).Says(t, rotated, "[me/moved]")
		ada.Lore(ada.Home, "memory", "list", "--store", "me/moved").OK(t).Says(t, "${secret:unstored}").Never(t, keyA, keyB)
	})
}
