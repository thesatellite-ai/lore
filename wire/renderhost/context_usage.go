// context_usage.go — usage recording (CONTEXT_BUDGET 6.8, 6.12): each row
// `lore context` prints (and each row a context hook sends) gets its last
// use time and a count in this machine's local state (row_usage in
// <lore home>/state/local.db, never synced), so `lore lint --context` can
// say "not used in 60 days on this machine". It replaces the v0.1.17
// decay buffer / last_accessed_at idea with the machine-local store
// (FA-OLD-LOCAL).
//
//	runContext / hook ─► fit → printed ids ─► recordUsage ─► localstate.Usage[<row id>]
//
// DECISION FA-CTX-3: usage is best effort and silent: a local-state
// failure never fails or slows the command (hooks must not block, 6.9);
// key = the row's permanent id, doc = rowUsage.

package renderhost

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/store/localstate"
)

// rowUsage is one row's machine-local use record.
type rowUsage struct {
	LastUsed time.Time `json:"last_used"`
	Count    int       `json:"count"`
}

// recordUsage stamps every id as used now; errors are dropped (FA-CTX-3).
func (h *Host) recordUsage(ctx context.Context, ids []string) {
	if h.app == nil || len(ids) == 0 {
		return
	}
	s, err := h.app.LocalState(ctx)
	if err != nil {
		return
	}
	defer func() { _ = s.Close() }() // best effort: usage never fails a command
	now := h.now().UTC()
	for _, id := range ids {
		u, _, gerr := localstate.GetJSON[rowUsage](ctx, s, localstate.Usage, id)
		if gerr != nil {
			return
		}
		u.LastUsed, u.Count = now, u.Count+1
		if localstate.PutJSON(ctx, s, localstate.Usage, id, u) != nil {
			return
		}
	}
}
