//go:build stories && !windows

package stories

// O_remote_replaced_test.go — devco:D-913/D-914 (public issue #136) on the
// owner-shaped machine (story O): the code remote of an own repo is
// replaced by a new empty repository at the same url (the old repo renamed
// away to a private archive, a clean history published under its name).
// Cells: lore sync reports the live rows not sent and exits 0, a write that
// is not live goes on, a live write stops naming --fresh first; a plain
// `lore live restore` refuses and pushes nothing (D-914: the old live rows
// reach the new remote only with --publish); `lore live restore --fresh`
// pushes nothing, and the next live write starts a new live ref on the new
// remote that holds only the new row, never the old one in any commit.

import (
	"os"
	"strings"
	"testing"
)

func TestStoryORemoteReplacedKeepsOldLiveRowsOff(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing-app"))
	const oldTitle, newTitle = "rotate the private billing token", "publish the changelog"

	s.Step("D-914/live-row-on-the-old-remote", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		bc.CommitAll("lore init")
		ada.Lore(bc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(bc.Dir, "task", "add", "--title", oldTitle, "--commitment", "accepted", "--to", "repo").OK(t)
		ada.Lore(bc.Dir, "sync").OK(t)
		oCheck(t, "D-914/setup-live-ref-pushed", billing.Has("refs/lore/live"), "the live row never reached the old remote")
		// the old repo is renamed away and a new empty one made at its url
		if err := os.Rename(billing.Dir, billing.Dir+".archive"); err != nil {
			t.Fatal(err)
		}
		bc.Git("init", "-q", "--bare", "-b", "main", billing.Dir)
		bc.Git("push", "-q", "origin", "main")
	})

	s.Step("D-914/new-remote-plain-restore-refuses", func() {
		sync := ada.Lore(bc.Dir, "sync").OK(t)
		oCheck(t, "D-913/sync-reports", strings.Contains(sync.Out(), "live rows not sent") && strings.Contains(sync.Out(), "`lore live restore --fresh`"),
			"lore sync on the new remote does not report the live rows not sent:\n%s", sync.Out())
		ada.Lore(bc.Dir, "memory", "add", "--body", "the repo moved to a clean history", "--to", "me").OK(t) // a write that is not live (in this own repo a repo memory is a live row)
		w := ada.Lore(bc.Dir, "task", "add", "--title", newTitle, "--commitment", "accepted", "--to", "repo").Refused(t, "E_READ_ONLY")
		fresh, publish := strings.Index(w.Out(), "`lore live restore --fresh`"), strings.Index(w.Out(), "`lore live restore --publish`")
		oCheck(t, "D-914/live-write-hint", fresh >= 0 && publish > fresh, "the live write's hint does not name --fresh before --publish:\n%s", w.Out())
		r := ada.Lore(bc.Dir, "live", "restore", "--yes")
		oCheck(t, "D-914/plain-restore-refused", r.Code != 0 && strings.Contains(r.Out(), "E_REFUSED") && strings.Contains(r.Out(), "--publish"),
			"plain `lore live restore` on the new remote did not refuse naming --publish (exit %d):\n%s", r.Code, r.Out())
		oCheck(t, "D-914/nothing-pushed", !billing.Has("refs/lore/live"), "the old live rows reached the new remote: %v", billing.Refs("refs/lore/"))
	})

	s.Step("D-914/fresh-starts-a-new-live-ref", func() {
		ada.Lore(bc.Dir, "live", "restore", "--fresh", "--yes").OK(t).Says(t, "nothing was pushed")
		oCheck(t, "D-914/fresh-pushed-nothing", !billing.Has("refs/lore/live"), "--fresh pushed: %v", billing.Refs("refs/lore/"))
		ada.Lore(bc.Dir, "task", "add", "--title", newTitle, "--commitment", "accepted", "--to", "repo").OK(t)
		ada.Lore(bc.Dir, "sync").OK(t)
		oCheck(t, "D-914/new-live-ref", billing.Has("refs/lore/live"), "the next live write did not start a live ref on the new remote")
		bc.Git("fetch", "-q", "origin", "refs/lore/live")
		hist := bc.Git("log", "-p", "FETCH_HEAD")
		oCheck(t, "D-914/only-new-rows", strings.Contains(hist, newTitle) && !strings.Contains(hist, oldTitle),
			"the new remote's live ref history (want the new task only):\n%s", hist)
	})
}
