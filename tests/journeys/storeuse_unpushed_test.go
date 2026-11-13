//go:build journeys && !windows

package journeys

// storeuse_unpushed_test.go — a lead keeps notes in a git store and edits
// a row file by hand in lore's clone; a read picks the edit up as a commit
// that waits there, unpushed. Then they switch the store to a clone of
// their own: `lore store use` first runs the store sync every command
// runs, which pushes that commit, and then switches. It was refused
// ("holds work its remote does not have") while the remote was reachable,
// once the open of a store command stopped running the sync.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyStoreUseAfterUnpushedWork(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	notes := w.Remote("github.com/acme-co/notes")
	lorClone := lee.LorePath("stores", "acme-co", "notes")
	unpushed := func() string {
		return strings.TrimSpace(w.TryGit(lorClone, "rev-list", "--count", "--branches", "--not", "--remotes=origin").Stdout)
	}

	w.Step("day1/team-and-a-notes-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		tt.lore(tlLee, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		tt.lore(tlLee, "task", "add", "--title", "Renew the certificate", "--to", "notes", "--no-live").OK(t)
		tt.lore(tlLee, "memory", "add", "--to", "notes", "--body", "Staging resets every Monday.").OK(t)
	})

	w.Step("day1/a-hand-edit-lore-picks-up", func() {
		files, _ := filepath.Glob(filepath.Join(lorClone, "_store", "memories", "*.json"))
		if len(files) != 1 {
			t.Fatalf("want one memory file in lore's clone, found %v", files)
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, files[0], strings.Replace(string(b), "every Monday", "every Sunday night", 1))
		// a read picks the edit up as a commit in lore's clone
		tt.lore(tlLee, "memory", "list", "--store", "notes").OK(t).Says(t, "every Sunday night")
		if n := unpushed(); n == "0" {
			t.Fatalf("the picked-up edit should wait in lore's clone, unpushed (got %s)", n)
		}
	})

	w.Step("day1/store-use-pushes-then-switches", func() {
		own := lee.Clone(notes, "my-notes")
		tt.lore(tlLee, "store", "use", "--path", own.Dir, "--yes").OK(t).
			Says(t, "now uses your clone").
			Never(t, "holds work its remote does not have")
		if n := unpushed(); n != "0" {
			t.Errorf("lore's clone still holds %s unpushed commit(s) after store use", n)
		}
	})
}
