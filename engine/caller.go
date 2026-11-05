package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/op"
)

// CallerOf returns the caller of the operation running in ctx (set by
// Run), and false outside Run (a direct invoke, a test). An operation
// that only decides how much to show, never whether to write, reads it:
// `lore session list` shows raw tool session ids only to an interactive
// caller (AGENT_IDENTITY 9, OV11-f), so a `lore api` client never gets
// them even when the server itself runs in a terminal.
func CallerOf(ctx context.Context) (op.Caller, bool) {
	c, ok := ctx.Value(callKey{}).(call)
	if !ok {
		return op.Caller{}, false
	}
	return c.caller, true
}
