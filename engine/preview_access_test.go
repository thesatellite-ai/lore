package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// paIn / paOut are the input and output of the preview probe below.
type (
	paIn struct {
		DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"write nothing"`
	}
	paOut struct {
		Access string `json:"access"`
	}
)

// TestRunPreviewIsARead (devco:D-682): a write operation run with
// --dry-run runs with read access, so what only a write may do on first
// use (a store folder's project row, T-11; a push) never happens for a
// preview. Without --dry-run it stays a write.
func TestRunPreviewIsARead(t *testing.T) {
	t.Parallel()
	f := setup(t)
	probe := op.Define(op.Spec{Name: "probe.edit", Summary: "probe", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, _ paIn) (paOut, error) {
			acc, _ := AccessOf(ctx)
			return paOut{Access: string(acc)}, nil
		})
	for _, tc := range []struct {
		body string
		want op.Access
	}{{`{"dry_run":true}`, op.AccessRead}, {`{}`, op.AccessWrite}} {
		res, err := f.eng.Run(context.Background(), probe, op.JSONDecoder{Body: []byte(tc.body)}, op.Caller{})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := res.Value().Interface().(paOut)
		if !ok {
			t.Fatalf("result %v", res.Value())
		}
		if got.Access != string(tc.want) {
			t.Errorf("input %s ran with access %q, want %q", tc.body, got.Access, tc.want)
		}
	}
}
