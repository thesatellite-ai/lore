package wire

import (
	"strings"
	"testing"

	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestSearchNamesKindsWithoutTheLens (devco:D-808): a search with a
// resource hit names the hit's kind ("server") by reading the caches'
// kinds tables; it no longer opens the SQL lens (which created every
// lens view on each search, about a third of a warm search's time).
func TestSearchNamesKindsWithoutTheLens(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := lensRepo(t, nil)
	kindWorld(t, a)
	before := a.lensOpens.Load()
	out, err := run(t, a, opssearch.OpRun, `{"query":"hub_prod"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hub_prod") || !strings.Contains(out, "server") {
		t.Errorf("search hub_prod: want the resource named with its kind server:\n%s", out)
	}
	if n := a.lensOpens.Load() - before; n != 0 {
		t.Errorf("search opened the SQL lens %d time(s), want 0", n)
	}
}
