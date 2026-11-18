package wire

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestCachesShareOneHandlePerFileD667: two sources opened at one cache
// file (the org store and a repo linked with `--project org`, which names
// the org folder again) share one handle, so a kind table one source's
// rows create or drop is in the list every source writes through
// (devco:D-667); Close closes it once.
func TestCachesShareOneHandlePerFileD667(t *testing.T) {
	ctx := context.Background()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	c := newCaches()
	path := filepath.Join(t.TempDir(), "org.db")
	for _, s := range []store.Source{"acme", "acme/org"} {
		if err := c.open(ctx, s, path, reg, refs.Expander{}); err != nil {
			t.Fatal(err)
		}
	}
	if c.by["acme"] != c.by["acme/org"] {
		t.Fatal("two sources at one cache file got two handles: a kind table one creates or drops is missing from the other's list")
	}
	if got := len(c.order); got != 2 {
		t.Fatalf("order holds %d sources, want 2", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
