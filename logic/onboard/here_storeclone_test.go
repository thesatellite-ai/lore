package onboard

// here_storeclone_test.go — devco D-682: setup list in a store clone said
// "a git repo lore is not set up in (lore init sets it up)", and init
// there wrote into the store. A store clone is named as the store it is.

import (
	"strings"
	"testing"
)

func TestHereLineStoreClone(t *testing.T) {
	for _, c := range []struct {
		h    Here
		want string
	}{
		{Here{State: HereStoreClone, StoreName: "acme", StoreFolder: "web"}, "This folder: the clone of store acme (folder web), a lore store, not a code repo"},
		{Here{State: HereStoreClone, StoreName: "acme"}, "This folder: the clone of store acme (its top), a lore store, not a code repo"},
	} {
		got := HereLine(c.h)
		if !strings.HasPrefix(got, c.want) || strings.Contains(got, "lore init sets it up") {
			t.Errorf("HereLine(%+v) = %q, want it to start %q and never offer lore init", c.h, got, c.want)
		}
	}
}
