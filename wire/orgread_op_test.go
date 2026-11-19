package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestOrgReadOpLeavesStoreAdminToTheRepo: `lore store set|compact --store
// <name>` in a repo names the repo's attached store, never an org read
// (devco D-476: the org-read route sent them to the store-only path, which
// refused "this command needs a repo").
func TestOrgReadOpLeavesStoreAdminToTheRepo(t *testing.T) {
	read := func(name string) op.Spec { return op.Spec{Name: name, Access: op.AccessRead, Permission: op.PermRead} }
	for _, name := range []string{"store.set", "store.compact", "store.push", "store.list"} {
		if orgReadOp(read(name)) {
			t.Errorf("%s: routed as an org read", name)
		}
	}
	for _, name := range []string{"memory.list", "rule.list", "search.run", "sql.run", "store.folder.list"} {
		if !orgReadOp(read(name)) {
			t.Errorf("%s: not an org read", name)
		}
	}
	if orgReadOp(op.Spec{Name: "memory.add", Access: op.AccessWrite, Permission: op.PermWrite}) {
		t.Error("a write routed as an org read")
	}
}
