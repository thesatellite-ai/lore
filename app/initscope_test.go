package app

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/wire"
)

// TestInitScopeKeepsStoresForAClientRepo (H-1): init opens no org store,
// except in a repo linked as a client, whose repo writes get the F16 leak
// check that reads the linked folder (with it dropped, init in a client
// repo always failed E_LEAK_BLOCKED "cannot check").
func TestInitScopeKeepsStoresForAClientRepo(t *testing.T) {
	ctx := context.Background()
	want := []attach.Store{{Name: "acme", Folder: "web"}}
	for _, c := range []struct {
		name   string
		client bool
		n      int
	}{{"client repo", true, 1}, {"own repo", false, 0}} {
		o := wire.Options{
			OrgStores:    func(context.Context) ([]attach.Store, error) { return want, nil },
			RepoIsClient: func(context.Context) (bool, error) { return c.client, nil },
		}
		initScope(&o)
		if o.DefaultTarget != nil || o.OrgEntryStore != nil {
			t.Fatalf("%s: init keeps the default target or entry stores", c.name)
		}
		got := 0
		if o.OrgStores != nil {
			s, err := o.OrgStores(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got = len(s)
		}
		if got != c.n {
			t.Errorf("%s: init attaches %d org stores, want %d", c.name, got, c.n)
		}
	}
}
