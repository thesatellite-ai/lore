package live

import "context"

// Confirm records that the remote now holds oid on this ref because a
// push this caller made elsewhere carried it (a coordination write's
// atomic push of refs/lore/signals plus refs/lore/live, P3-SIGCOORD-6):
// the tracking ref moves to oid, the seen marker follows and the pending
// list empties, exactly as after Push. The local ref is not touched.
func (r *Ref) Confirm(ctx context.Context, oid string) error {
	if oid == "" {
		return nil
	}
	if err := r.setTracking(ctx, oid); err != nil {
		return err
	}
	return r.confirmed(ctx, oid)
}
