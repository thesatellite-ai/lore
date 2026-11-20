//go:build journeys && !windows

package journeys

// refusedonce_test.go — a store refused once, reachable again (devco:D-741).
// One lore run on Lee's laptop had its fetch of the team store refused (an
// editor's commit hook with no ssh agent, a run with other credentials):
// it left the no-access record in the clone. Before, every later read
// skipped the store although Lee reads and writes it every day: from the
// home folder `show --id … --setup lead` looked only in the other stores,
// and in his web repo (linked to a folder, no .lore) the link vanished, so
// `show --id` refused "lore isn't set up in …". Now a store this machine
// has a copy of is read from it whatever the record says, and the next
// fetch that works removes the record.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

const (
	roWebSlug = "github.com/acme-co/web"
	roBody    = "the web app keeps sessions for a day"
)

var roMemID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

func TestJourneyStoreRefusedOnceIsReadAgain(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	web := w.Remote(roWebSlug)
	parent := filepath.Join(lee.Home, "code")
	record := lee.LorePath("stores", "acme-co", "lore-team", ".git", "lore", "no-access")
	var wc string
	id := ""

	w.Step("day1/lee-links-his-web-repo-and-writes-there", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", tt.store.SSHURL(), "web").OK(t)
		c := lee.Clone(web, "web")
		lee.MoveClone(t, c, filepath.Join(parent, "web"))
		wc = c.Dir
		jWrite(t, filepath.Join(wc, "README.md"), "# web\n")
		c.CommitAll("first")
		lee.Lore(wc, "org", "link", "--org", tlOrg, "--project", "web", "--yes").OK(t)
		id = roMemID.FindString(lee.Lore(wc, "memory", "add", "--body", roBody, "--yes").OK(t).Out())
		if id == "" {
			t.Fatal("memory add printed no id")
		}
		lee.Lore(wc, "sync").OK(t)
		if _, err := os.Stat(filepath.Join(wc, ".lore")); err == nil {
			t.Fatal("the linked web repo got a .lore folder")
		}
	})

	w.Step("day1/one-run-is-refused-by-the-forge", func() {
		asDeny(t, lee, tlStoreSlug)
		lee.Lore(wc, "sync") // refused: the run records it; its own answer is not the point
		asAllow(t, lee, tlStoreSlug)
		if _, err := os.Stat(record); err != nil {
			t.Fatalf("the refused sync left no no-access record (%v): the journey tests nothing", err)
		}
	})

	w.Step("day1/the-store-is-read-from-its-copy", func() {
		// no fetch has worked since the refusal: the record is still there
		lee.Lore(wc, "show", "--id", id).OK(t).Says(t, roBody).Never(t, "E_NOT_A_STORE", "isn't set up")
		lee.Lore(lee.Home, "show", "--id", id, "--setup", tlLeadSetup).OK(t).Says(t, roBody, tlOrg).Never(t, "E_NOT_FOUND")
		lee.Lore(wc, "whoami").OK(t).Never(t, "no access")
	})

	w.Step("day1/the-next-sync-clears-the-record", func() {
		lee.Lore(wc, "sync").OK(t).Never(t, "no access")
		if _, err := os.Stat(record); err == nil {
			t.Error("a sync that reached the store kept the no-access record")
		}
		lee.Lore(lee.Home, "show", "--id", id, "--setup", tlLeadSetup).OK(t).Says(t, roBody)
	})
}
