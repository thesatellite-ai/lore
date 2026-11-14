//go:build journeys && !windows

package journeys

// clientrepo_guards_test.go — J-11: a client repo linked by its remote
// (no .lore/, never lore init) gets lore's leak guard hooks, so an
// internal rule sentence pasted into a file is refused on commit
// (E_LEAK_BLOCKED), and whoami never says "guards on" in a clone that
// has no guard hook. Lee links the client repo shop to a store folder;
// a teammate's fresh clone says the guards are off until lore sync
// (a set-up command) installs them there too.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	cgSlug   = "github.com/client-co/cart"
	cgFolder = "client_cart"
	cgRule   = "Never mention internal pricing tiers in cart commits or PRs."
)

func TestJourneyClientRepoGuards(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(cgSlug)
	cart := filepath.Join(parent, "cart")
	var shop *teamharness.Clone

	w.Step("day1/link-then-commit-an-internal-sentence", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", cgFolder).OK(t)
		shop = jPlainRepo(t, lee, remote, cart)
		c := shop
		lee.LoreIn(c, "org", "link", tlOrg, "--project", cgFolder, "--yes").OK(t)
		lee.LoreIn(c, "rule", "add", "--body", cgRule, "--severity", "must").OK(t).Says(t, cgFolder)
		lee.LoreIn(c, "whoami").OK(t).Says(t, "guards on")
		jWrite(t, filepath.Join(c.Dir, "src", "note.ts"), "// "+cgRule+"\n")
		c.Git("add", "-A")
		r := c.TryGit("commit", "-q", "-m", "Add cart note")
		if r.Code == 0 {
			t.Fatalf("an internal rule sentence was committed in the linked client repo: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED")
		if st := c.Git("status", "--porcelain", "--ignored", "--", ".lore", ".gitattributes"); st != "" {
			t.Fatalf("the linked client repo holds lore files:\n%s", st)
		}
	})

	w.Step("day1/a-clone-without-the-hook-says-guards-off", func() {
		c := shop
		if err := os.Remove(filepath.Join(c.Dir, ".git", "hooks", "commit-msg")); err != nil {
			t.Fatal(err)
		}
		lee.LoreIn(c, "whoami").OK(t).Says(t, "guards OFF in this clone", "lore org link --org "+tlOrg).Never(t, "(guards on)")
		lee.LoreIn(c, "org", "link", tlOrg, "--project", cgFolder, "--yes").OK(t).Says(t, "leak guard installed")
		lee.LoreIn(c, "whoami").OK(t).Says(t, "guards on")
		if r := c.TryGit("commit", "-q", "-m", "Add cart note"); r.Code == 0 {
			t.Fatalf("committed after the link was run again: %s", r)
		}
	})
}
