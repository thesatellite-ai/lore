package rule_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestScopeGlobalRefusesLive (devco O-4, the same shape as memory add):
// rule add --scope global with --no-live or --live is refused naming the
// flag, never written to the personal store with the flag dropped.
func TestScopeGlobalRefusesLive(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	for _, in := range []string{`{"body":"r","scope":"global","no_live":true}`, `{"body":"r","scope":"global","live":true}`} {
		if msg := h.Fails("rule.add", in, errs.InvalidInput); !strings.Contains(msg, "--scope global") {
			t.Fatalf("%s: %q", in, msg)
		}
	}
	if got := opstest.Text(h.Must("rule.add", `{"body":"r","scope":"global"}`)); !strings.Contains(got, "scope: [global]") {
		t.Fatalf("global rule add %q", got)
	}
}
