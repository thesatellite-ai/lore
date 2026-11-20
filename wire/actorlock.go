package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
)

// devco D-738: state.actorMu is a plain sync.Mutex, and Go mutexes are not
// re-entrant. ensureActor holds it while it writes the caller's actor row,
// and that write's audit looks up this process's session row
// (chainAudit.Session -> CurrentSession -> Project). In a fresh client repo
// linked to an org folder with no project row yet, an agent's first write
// reached rosterProject again from inside that lookup, which wrote the
// project row through withAgentRow -> withActorRows -> actorMu.Lock: the
// process waited on itself forever (lore memory add right after lore org
// link hung at 0% CPU, holding its own store lock).
//
// rosterProject no longer writes inside the session lookup (the cause);
// lockActors is the guard for the whole class: a call chain that already
// holds actorMu carries that on its context, and asking for the lock again
// on that chain is refused with a clear E_INTERNAL instead of a hang.

// actorsHeldKey marks a context whose call chain holds a state's actorMu.
type actorsHeldKey struct{}

// actorsHeldBy reports whether ctx's call chain holds st's actorMu.
func actorsHeldBy(ctx context.Context, st *state) bool {
	held, _ := ctx.Value(actorsHeldKey{}).(*state)
	return held != nil && held == st
}

// errActorLockReentry is the refusal of a second actorMu lock on a chain
// that holds it (a lore bug; waiting would never return).
var errActorLockReentry = errs.New(errs.Internal, "wire: this write asked for its actor-row lock again while it already holds it, so it stopped instead of waiting on itself").
	WithHint("run the command again; if it repeats, report it with lore version and the command")

// lockActors takes st.actorMu for the caller and returns ctx marked as
// holding it (pass that ctx to every write made under the lock, so a nested
// request is recognised) and the unlock func. A chain that already holds
// the lock gets errActorLockReentry and nothing to unlock.
func lockActors(ctx context.Context, st *state) (context.Context, func(), error) {
	if actorsHeldBy(ctx, st) {
		return ctx, func() {}, errActorLockReentry
	}
	st.actorMu.Lock()
	return context.WithValue(ctx, actorsHeldKey{}, st), st.actorMu.Unlock, nil
}
