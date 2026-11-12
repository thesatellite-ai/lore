package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestNoCloneMakesNothing (B4-14): under WithNoClone (`lore fix --dry-run`)
// a store with no clone here is E_STORE_UNREACHABLE and nothing is made:
// no clone folder, no lock, no git init.
func TestNoCloneMakesNothing(t *testing.T) {
	src := newOldRemote(t, "", []string{"team"}, nil)
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	ctx := WithNoClone(context.Background())
	if _, err := OpenClone(ctx, src.remote, dir, "team", store.OpenOptions{}); !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("open under no-clone: %v, want E_STORE_UNREACHABLE", err)
	}
	if err := PrepareClone(ctx, dir, src.remote, false); !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("prepare under no-clone: %v, want E_STORE_UNREACHABLE", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("%s was made under no-clone", dir)
	}
	b, err := OpenClone(context.Background(), src.remote, dir, "team", store.OpenOptions{})
	if err != nil {
		t.Fatalf("a normal open: %v", err)
	}
	_ = b.Close()
	if b, err = OpenClone(ctx, src.remote, dir, "team", store.OpenOptions{}); err != nil {
		t.Fatalf("an existing clone under no-clone: %v", err)
	}
	_ = b.Close()
}
