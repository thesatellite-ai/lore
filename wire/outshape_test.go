package wire

import (
	"fmt"
	"testing"
)

// TestRegistryOutputShapes reads every registered operation's output
// fields. op.Define reads them on first use, not at startup (fastgate:G1a:
// reading every output type was about half the registry build each lore
// process paid), so a bad output tag would otherwise panic only when a
// user first runs that command; this test makes it fail the build.
func TestRegistryOutputShapes(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	entries := a.Registry().Entries()
	if len(entries) < 100 {
		t.Fatalf("registry has %d operations, want the full set", len(entries))
	}
	for _, e := range entries {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: output fields: %s", e.Spec().Name, fmt.Sprint(r))
				}
			}()
			_ = e.OutputFields()
		}()
	}
}
