package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// TestRunDirectNamesTheCommit pins R-1 (B2-4): an operation wire runs
// directly (actor register and the other roster ops) carries its name to
// the store commit as engine.Run does, so its actor row is committed as
// `lore: actor register act_…`, never `lore: write act_…`.
func TestRunDirectNamesTheCommit(t *testing.T) {
	t.Parallel()
	var got string
	e := op.Define(op.Spec{Name: "actor.register", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, _ syncCaptureIn) (syncCaptureOut, error) {
			got = store.CommitOp(ctx)
			return syncCaptureOut{}, nil
		})
	if _, err := runDirect(context.Background(), e, op.JSONDecoder{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if got != "actor.register" {
		t.Fatalf("commit op = %q, want actor.register", got)
	}
}
