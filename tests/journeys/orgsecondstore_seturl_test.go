//go:build journeys && !windows

package journeys

// orgsecondstore_seturl_test.go — an admin of two orgs moves one org's
// second store (an admins-only store added with `lore org store add`)
// from the home folder (devco D-540). `lore store set-url --name
// lore-ops …` refused "no org entry is named lore-ops … takes an org's
// name, me or a sto_ id": outside a repo only the org entry's own name
// named a store. Every store an org lists is named the same way now: its
// short name, <org>/<store>, the org's name (its company store), a
// setup's name, me or a sto_ id; set-url, push, show and forget share the
// one resolver, and a name that is none lists the orgs' store names.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyOrgSecondStoreMovedFromHome(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ops := w.Remote("github.com/acme-co/lore-ops")
	moved := w.Remote("github.com/acme-new/lore-ops")
	side := w.Remote("github.com/sideco/lore-side")
	const sideOrg = "sideco"

	w.Step("day1/two-orgs-one-with-an-admins-only-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		scratch := lee.Clone(w.Remote("github.com/acme-co/scratch"), "scratch")
		lee.LoreIn(scratch, "store", "init", "--name", "ops", "--url", ops.SSHURL(), "--yes").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", ops.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t)
		// a second org on the same laptop, with its own store
		lee.LoreIn(scratch, "store", "init", "--name", "side", "--url", side.SSHURL(), "--yes").OK(t)
		lee.LoreIn(scratch, "org", "create", "--org", sideOrg, "--store", side.SSHURL(), "--own", "github.com/sideco/*",
			"--key", "human:lee@side.test", "--name", "Lee", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day2/move-the-second-store-from-home-then-push-it", func() {
		w.AdvanceDays(1)
		if err := os.RemoveAll(moved.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(ops.Dir, moved.Dir); err != nil {
			t.Fatal(err)
		}
		dest := lee.LorePath("stores", "acme-new", "lore-ops")
		// D-540: the second store's own name was refused outside a repo
		r := lee.Lore(lee.Home, "store", "set-url", "--name", "lore-ops", "--url", moved.SSHURL(), "--yes").OK(t)
		// D-514/515 still hold: the clone moved, with the org identity
		r.Says(t, "is now at "+moved.SSHURL(), "moved to", "your identity in org "+tlOrg).Never(t, "lore store forget")
		if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
			t.Fatalf("no clone at the new address's folder %s: %v", dest, err)
		}
		if got := strings.TrimSpace(w.TryGit(dest, "config", "--local", "user.email").Stdout); got != tlLee.Email {
			t.Errorf("the moved clone's user.email = %q, want the org identity %s", got, tlLee.Email)
		}
		lee.Lore(lee.Home, "store", "show", "--store", "lore-ops").OK(t).Says(t, "lore-ops:", "org "+tlOrg)
		// a commit waits in the moved clone; push by the store's name sends it
		before := len(moved.Log("main"))
		lee.Lore(lee.Home, "store", "folder", "add", moved.SSHURL(), "infra").OK(t)
		lee.Lore(lee.Home, "store", "push", "--store", "lore-ops").OK(t).Says(t, "sent 1 commit")
		if got := len(moved.Log("main")); got <= before {
			t.Fatalf("the push sent nothing to the moved remote: %d commits, %d before", got, before)
		}
		// <org>/<store> names the same store
		lee.Lore(lee.Home, "store", "push", "--store", tlOrg+"/lore-ops").OK(t).Says(t, "nothing to send")
		// the org's name still names its company store, not the second one
		lee.Lore(lee.Home, "store", "show", "--store", tlOrg).OK(t).Says(t, tlOrg+":").Never(t, "lore-ops")
		// nothing matches: the refusal lists every org's store names
		lee.Lore(lee.Home, "store", "set-url", "--name", "nosuch", "--url", moved.SSHURL(), "--yes").
			Refused(t, "nosuch", tlOrg+"/lore-ops", sideOrg+"/lore-side").Never(t, "in this repo")
		lee.Lore(lee.Home, "store", "push", "--store", "nosuch").
			Refused(t, "nosuch", tlOrg+"/lore-ops", sideOrg+"/lore-side").Never(t, "in this repo")
	})
}

// TestJourneyEmptyOrgStoreMovedInRepo (devco D-541): in an org repo, an
// admins-only store `org store add --push` added and nobody wrote to (its
// remote is still an empty repository) is moved with `lore store set-url
// --name lore-ops`. It refused "… is an empty repository, and acmeteam is
// not a folder store …" with a hint to set-url the TEAM store to the ops
// url: following it would have repointed the team store. set-url acts on
// the store named, and no line of it names another store.
func TestJourneyEmptyOrgStoreMovedInRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ops := w.Remote("github.com/acme-co/lore-ops")
	moved := w.Remote("github.com/acme-new/lore-ops")

	w.Step("day1/an-admins-only-store-nobody-wrote-to", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", ops.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
	})

	w.Step("day2/move-it-from-the-org-repo", func() {
		w.AdvanceDays(1)
		teamURL := tt.store.SSHURL()
		if n := len(ops.Log("main")); n != 0 {
			t.Fatalf("the ops store must be empty before the move, has %d commits", n)
		}
		if err := os.RemoveAll(moved.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(ops.Dir, moved.Dir); err != nil {
			t.Fatal(err)
		}
		r := tt.lore(tlLee, "store", "set-url", "--name", "lore-ops", "--url", moved.SSHURL(), "--yes")
		// D-541: never a line naming the team store or another store's set-url
		r.Never(t, "--name "+tlOrg+" ", "--name "+tlOrg+"\n", teamURL)
		r.OK(t).Says(t, "is now at "+moved.SSHURL(), "only its address moved")
		// the store named moved; the team store kept its own address
		tt.lore(tlLee, "store", "list").OK(t).Says(t, moved.SSHURL(), teamURL).Never(t, ops.SSHURL())
		cfg, err := os.ReadFile(lee.LorePath("config.yaml"))
		if err != nil || !strings.Contains(string(cfg), moved.SSHURL()) || !strings.Contains(string(cfg), teamURL) {
			t.Fatalf("the org entry should name the moved ops url and the team url (%v):\n%s", err, cfg)
		}
	})
}
