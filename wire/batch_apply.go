package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
)

// RunLine implements opsbatch.LineRunner: one line of `lore batch apply`
// (devco D-925) runs through the same Run path as the command typed on
// its own (decode, store, permission, secret scan, policy, roles, client
// folders), as the caller of the apply, with the apply's --yes and its
// answer to a secret finding (--not-a-secret --reason, --secret-ref,
// --secret-unstored), so the hint a refused line prints ("re-run with
// --not-a-secret …") works on the apply command. A line never reads the
// terminal: a question it would ask is E_NEEDS_CONFIRM for the whole
// apply. Its writes join the apply's open stage (ctx), so they land in the
// apply's one commit per store instead of one commit each.
func (a *App) RunLine(ctx context.Context, e op.Entry, d op.Decoder) (op.Result, error) {
	c := op.Caller{Yes: engine.Confirmed(ctx)}
	if outer, ok := engine.CallerOf(ctx); ok {
		c = outer
	}
	c.Interactive = false
	return a.Run(ctx, e, d, c)
}
