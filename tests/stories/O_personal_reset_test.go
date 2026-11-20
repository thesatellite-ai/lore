//go:build stories && !windows

package stories

// O_personal_reset_test.go — story O, devco D-763 (owner P-5, bug hunt
// H-22): after the personal store's clone was reset by hand (git reset
// --hard to an older commit) the folder's cache still listed the rows the
// reset dropped: a clone-mode read lists the files changed since the
// cache's cursor commit, and a file gone from that diff is not a delete,
// so nothing ever took them out. `lore search rebuild`, the way out, could
// not run from the home folder either: it took no --store. Now the engine
// sees that HEAD no longer descends from the cache's cursor and rebuilds
// the folder's cache, and the rebuild takes --store from any folder.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryOPersonalResetRebuilds: a hand reset of the personal store's
// clone drops its rows from every read at once, and `lore search rebuild
// --store me/<folder>` runs from the home folder (devco D-763).
func TestStoryOPersonalResetRebuilds(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	to := "me/" + psFolder
	bodies := "SELECT body FROM memories_all ORDER BY body"

	var clone, kept string
	s.Step("personal-reset/rows", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "reset kept note", "--to", to, "--yes").OK(t)
		clone = oStoreCloneLocal(t, ada)
		kept = s.Git(clone, "rev-parse", "HEAD")
		ada.Lore(ada.Home, "memory", "add", "--body", "reset dropped note", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "sql", "--query", bodies, "--store", to).OK(t).Says(t, "reset kept note", "reset dropped note")
		ada.Lore(filepath.Join(clone, psFolder), "sql", "--query", bodies).OK(t).Says(t, "reset dropped note")
	})

	s.Step("personal-reset/hand-reset-drops-the-rows-everywhere", func() {
		s.Git(clone, "reset", "--quiet", "--hard", kept)
		for _, p := range []struct {
			dir  string
			args []string
		}{{ada.Home, []string{"--store", to}}, {filepath.Join(clone, psFolder), nil}} {
			r := ada.Lore(p.dir, append([]string{"sql", "--query", bodies}, p.args...)...).OK(t)
			if !strings.Contains(r.Out(), "reset kept note") || strings.Contains(r.Out(), "reset dropped note") {
				t.Errorf("[O D-763 %s] after a hand reset of the store clone, `lore sql %s` still lists the row the reset dropped (stale cache):\n%s",
					p.dir, strings.Join(p.args, " "), r)
			}
		}
	})

	s.Step("personal-reset/rebuild-from-home-with-store", func() {
		ada.Lore(ada.Home, "search", "rebuild", "--store", to).OK(t).Says(t, "rebuilt", to)
		ada.Lore(ada.Home, "search", "--query", "kept", "--store", to).OK(t).Says(t, "reset kept note")
		// with no store named, the home refusal names the flag that works
		ada.Lore(ada.Home, "search", "rebuild").Refused(t, "--store")
	})
}
