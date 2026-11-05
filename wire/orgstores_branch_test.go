package wire

// orgstores_branch_test.go — later 123: an org entry whose store names a
// branch (stores: [{url, branch: staging}]) attaches on that branch
// (attachBranch feeds every attach.Store OrgStores builds: the linked
// folder, the committed project, the root and "<org>/<org folder>"); the
// default branch, written or not, attaches with no branch, and the same
// url on two branches is two store roots. The OrgStores path end to end
// needs a reachable store for its links (E_LINK_UNKNOWN otherwise), which
// the git-store e2e of later 222 covers for stores.yaml entries.

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

func TestOrgEntryBranchAttach(t *testing.T) {
	const url = "git@github.com:acme/notes.git"
	cases := []struct {
		name, branch, want string
	}{
		{"staging branch", "staging", "staging"},
		{"release/2026 branch", "release/2026", "release/2026"},
		{"default branch written out", orgconf.DefaultStoreBranch, ""},
		{"no branch", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := &orgconf.Store{URL: url, Branch: c.branch, OrgFolder: "org"}
			if got := attachBranch(sc); got != c.want {
				t.Fatalf("attachBranch = %q, want %q", got, c.want)
			}
			a := attach.Store{URL: url, Branch: attachBranch(sc)}
			if !sameStoreRoot(a, attach.Store{URL: url, Folder: ".", Branch: c.want}) {
				t.Fatal("the root and \".\" on one branch are one store root")
			}
			if sameStoreRoot(a, attach.Store{URL: url, Branch: c.want + "x"}) {
				t.Fatal("the same url on two branches is one store root")
			}
			if sameStoreRoot(a, attach.Store{URL: url, Folder: "acme", Branch: c.want}) {
				t.Fatal("a folder is not the store root")
			}
		})
	}
}
