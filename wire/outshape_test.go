package wire

import (
	"fmt"
	"testing"
)

// TestRegistryShapes reads every registered operation's input and output
// fields. op.Define reads both on first use, not at startup (fastgate:G1a
// for the output, devco:D-826 for the input: a command line mounts and
// decodes one operation), so a bad tag would otherwise panic only when a
// user first runs that command; this test makes it fail the build.
func TestRegistryShapes(t *testing.T) {
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
					t.Errorf("%s: input/output fields: %s", e.Spec().Name, fmt.Sprint(r))
				}
			}()
			_ = e.InputFields()
			_ = e.OutputFields()
		}()
	}
}

// TestRegistryFields keeps a Confirm.When without its bool input, a
// non-actor --by and a REST route clash failing the build: those checks
// moved out of op.NewRegistry (which every lore process runs) into
// Registry.CheckFields, run by the REST surface (devco:D-826).
func TestRegistryFields(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	if err := a.Registry().CheckFields(); err != nil {
		t.Fatal(err)
	}
}
