package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
)

// lraIn / lraOut are the input and output of the probe operations below.
type (
	lraIn  struct{}
	lraOut struct {
		Access string `json:"access"`
	}
)

// TestLocalReadRunsAsARead (devco:D-503): a Local operation that only
// reads runs with the engine's read access on its ctx, as engine.Run marks
// every other read, so the engine it opens never creates a row (T-11) or
// pushes. Before, lore why-context show in a store clone committed the
// folder's project row. A Local operation that may write stays unmarked.
func TestLocalReadRunsAsARead(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	probe := func(perm op.Permission) op.Entry {
		return op.Define(op.Spec{Name: "probe.run", Summary: "probe", Local: true, Permission: perm, Access: op.AccessRead},
			func(ctx context.Context, _ lraIn) (lraOut, error) {
				acc, _ := engine.AccessOf(ctx)
				return lraOut{Access: string(acc)}, nil
			})
	}
	for _, tc := range []struct {
		perm op.Permission
		want op.Access
	}{{op.PermRead, op.AccessRead}, {op.PermWrite, ""}} {
		res, err := a.runLocal(context.Background(), probe(tc.perm), op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := res.Value().Interface().(lraOut)
		if !ok {
			t.Fatalf("result %v", res.Value())
		}
		if got.Access != string(tc.want) {
			t.Errorf("Local op with permission %s ran with access %q, want %q", tc.perm, got.Access, tc.want)
		}
	}
}
