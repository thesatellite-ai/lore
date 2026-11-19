package row

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestMoveLineKeepsTheSetup (devco D-712): the bulk move's printed line
// (the dry run's next step, the admin's line of a refusal) keeps the
// global --setup the caller gave. From the home folder of a machine with
// two org setups `lore move --from <org>/<folder> --to … --setup <s>
// --dry-run` printed a next step without --setup, which is refused there
// (no setup to pick), so the printed command did not run where printed.
func TestMoveLineKeepsTheSetup(t *testing.T) {
	in := MoveIn{To: "acme/archive", BulkFlags: BulkFlags{From: "acme/legacy", Where: []string{"body~=old cdn"}}}
	for _, c := range []struct {
		ctx  context.Context
		want string
	}{
		{context.Background(), "lore move --from acme/legacy --to acme/archive --where 'body~=old cdn' --yes"},
		{op.WithSetup(context.Background(), "acme-admin"), "lore move --from acme/legacy --to acme/archive --where 'body~=old cdn' --yes --setup acme-admin"},
	} {
		if got := in.line(c.ctx, true); got != c.want {
			t.Errorf("line = %q, want %q", got, c.want)
		}
	}
}
