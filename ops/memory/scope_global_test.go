package memory_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// TestScopeGlobalAdd (devco O-4): memory add --scope global prints the
// global scope (it printed [project]) and lands in the personal store;
// --live or --no-live next to it is refused with the cause and the next
// step instead of being dropped without a word.
func TestScopeGlobalAdd(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	for _, in := range []string{`{"body":"x","scope":"global"}`, `{"body":"y","scope":"global","to":"me"}`} {
		r := h.Must(memory.OpAdd, in)
		if got := opstest.IDPattern(opstest.Text(r)); !strings.Contains(got, "scope: [global]") || !strings.Contains(got, "[me]") {
			t.Fatalf("add %s: %q, want [me] and scope: [global]", in, got)
		}
	}
	for flag, in := range map[string]string{
		"--no-live": `{"body":"z","scope":"global","no_live":true}`,
		"--live":    `{"body":"z","scope":"global","live":true}`,
	} {
		msg := h.Fails(memory.OpAdd, in, errs.InvalidInput)
		if !strings.Contains(msg, flag) || !strings.Contains(msg, "personal store") {
			t.Fatalf("%s: %q, want the flag and the cause named", flag, msg)
		}
	}
	// A plain add still prints the project badge.
	if got := opstest.Text(h.Must(memory.OpAdd, `{"body":"p"}`)); !strings.Contains(got, "scope: [project]") {
		t.Fatalf("plain add %q", got)
	}
}
