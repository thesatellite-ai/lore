//go:build journeys && !windows

package journeys

// storemove_org_test.go — the org's store repo moves to another GitHub
// org (devco D-514, D-515). Lee's own git identity is personal
// (lee@home.test); his identity in the org is lee@example.test. From his
// home folder he points lore at the new address with `lore store set-url
// --name <org> --url <new> --yes`, then sends with `lore store push`.
//
//	D-514: the store clone's git user must be the org identity (set-url
//	       gave it the personal email and said "your lore identity", while
//	       its own commit there was authored with the org email)
//	D-515: a fully pushed old clone moves to the new address's folder like
//	       one holding work (it was left at stores/<old org>/… with a
//	       `lore store forget` hint)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyStoreMovedToAnotherOrg(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	moved := w.Remote("github.com/acme-new/lore-team")
	const personal = "lee@home.test"

	w.Step("day1/team-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "sync").OK(t)
		lee.setGitIdentity(t, "Lee Home", personal) // his own git identity is personal
	})

	w.Step("day2/the-store-moves-to-another-org", func() {
		w.AdvanceDays(1)
		if err := os.RemoveAll(moved.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tt.store.Dir, moved.Dir); err != nil {
			t.Fatal(err)
		}
		old := lee.LorePath("stores", "acme-co", "lore-team")
		dest := lee.LorePath("stores", "acme-new", "lore-team")
		r := lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		// D-515: moved, never left behind with a forget hint
		r.Says(t, "is now at "+moved.SSHURL(), "moved to").
			Never(t, "lore store forget", "no longer used")
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("set-url left the old clone %s: %v", old, err)
		}
		if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
			t.Fatalf("no clone at the new address's folder %s: %v", dest, err)
		}
		// D-514: the clone commits as the org identity, and says so
		r.Says(t, "your identity in org "+tlOrg).Never(t, personal, "(your lore identity)")
		if got := strings.TrimSpace(w.TryGit(dest, "config", "--local", "user.email").Stdout); got != tlLee.Email {
			t.Errorf("the moved clone's user.email = %q, want the org identity %s", got, tlLee.Email)
		}
		lee.Lore(lee.Home, "store", "push", "--store", moved.SSHURL()).OK(t)
		if got := strings.TrimSpace(w.TryGit(moved.Dir, "log", "-1", "--format=%ae").Stdout); got != tlLee.Email {
			t.Errorf("the pushed head is authored by %q, want the org identity %s", got, tlLee.Email)
		}
		lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t)
	})
}
