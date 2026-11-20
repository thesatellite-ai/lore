//go:build stories && !windows

package stories

// O_storeset_hint_test.go — devco:D-760 (bug hunt H-15) on the
// owner-shaped machine (story O): a bad --settings word to `lore store
// set` printed the hint `e.g. lore store set me push=batch …`, the store
// and every setting by position, although the command's usage is
// `--store <store> --settings <settings>`. The hint now gives the named
// form with the store the person gave, and that form runs where it was
// printed.

import (
	"strings"
	"testing"
)

func TestStoryOStoreSetHintNamed(t *testing.T) {
	s := New(t, "O")
	mine := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))

	s.Step("D-760/store-set-bad-word-hint-runs-as-printed", func() {
		// This machine starts with no personal store; give it a git one
		// (push modes apply to git stores), so the example's --store me exists.
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", mine.SSHURL(), "--yes").OK(t)
		const named = "lore store set --store me --settings push=batch --settings push_every=10m --settings compact=on --settings compact_after=30d"
		for _, dir := range []string{pc.Dir, ada.Home} {
			r := ada.Lore(dir, "store", "set", "--store", "me", "--settings", "oops")
			if r.Code == 0 {
				t.Fatalf("store set --settings oops passed:\n%s", r.Out())
			}
			r.Says(t, named).Never(t, "store set me ")
			_, example, ok := strings.Cut(r.Out(), "e.g. lore ")
			if !ok {
				t.Fatalf("no example in the hint:\n%s", r.Out())
			}
			example, _, _ = strings.Cut(example, "\n")
			ada.Lore(dir, strings.Fields(example)...).OK(t).Says(t, "push batch every 10m")
		}
	})
}
