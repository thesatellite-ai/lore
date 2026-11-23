package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// Check-only writes (devco:D-928, H-186). Every write operation's
// --dry-run used to be its own preview code (63 operations), so no one
// place ran a write's checks in a dry run: a dry run could pass where the
// write fails, and an operation whose preview forgot to stop (meta set
// --id … --dry-run) wrote. Run now marks the context of every write
// operation's preview check-only, and the engine's write sinks run every
// check of the real write and then stop:
//
//	Create / Update / Delete / Move / Purge … (validate.Fields, references,
//	metadata keys and values, secrets, policy, roles, purged and stale ids)
//	  → put / writeRemove / move …
//	    → writeChanges / writePurge: role and --store checks, then STOP
//	    → finishW: no cache refresh, no audit record
//
// So an operation that runs its real write path under --dry-run fails
// exactly as the write would and writes nothing, and one that forgets its
// dry run cannot write. Rows written earlier in the same check-only run
// are not visible to its later reads (nothing reached the store), as in a
// stage (FB-RES-1).

// checkOnlyKey is the context key of a check-only run.
type checkOnlyKey struct{}

// WithCheckOnly is ctx marked check-only: every engine write made with it
// runs all of its checks and writes nothing (no store change, no commit,
// no cache refresh, no audit record). Run sets it for the preview of
// every write operation (op.Spec.Previews); an operation that previews
// by running its real write path needs nothing else.
func WithCheckOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, checkOnlyKey{}, true)
}

// CheckOnly reports whether ctx is a check-only run (WithCheckOnly): an
// operation uses it to word its result as a preview ("would …; nothing
// written") after running its real write path.
func CheckOnly(ctx context.Context) bool {
	on, _ := ctx.Value(checkOnlyKey{}).(bool)
	return on
}

// checkChanges is the one check of a backend Write that a check-only
// write still makes: every change's Expect (store.ErrStale for a create
// over a stored row, an edit of a row another write changed), so a dry
// run of a create whose name is taken answers E_NAME_TAKEN as the write
// does (the natural-id re-create reads the ErrStale), not success.
func checkChanges(ctx context.Context, st Attached, changes []store.Change) error {
	for _, c := range changes {
		if err := staleNow(ctx, st, c.Row, c.Expect); err != nil {
			return err
		}
	}
	return nil
}
