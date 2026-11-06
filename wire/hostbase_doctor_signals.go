package wire

// hostbase_doctor_signals.go — the signals rows of `lore doctor` on cmd/lore
// (F7 Debug; port of saas/cmd/cli/coord_doctor.go doctorSignals):
//
//	open the coordination world (no agent or person, no git repo: no rows)
//	  ─▶ a client repo keeps no signals of its own (D8): no rows
//	  ─▶ the last fetched copy of this repo's signals ref (no network)
//	       more than signalsRefLargeFiles files ─▶ warning, degraded
//	       expired signal files still on it    ─▶ warning (the next write drops them)
//	  ─▶ remote with autopush: a no-op atomic push of the ref onto itself;
//	     the remote refusing --atomic ─▶ warning, degraded
//
// Read-only: the probe pushes the commit the remote already has under a
// lease on that same commit, which the remote reports as up to date.

import (
	"context"
	"errors"
	"fmt"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// signalsRefLargeFiles is the size past which the signals ref is reported:
// tens of agents per store branch is the design scale (§7.8), so hundreds of
// live signal files mean writers are not dropping expired ones or the hub
// (P5) is due.
const signalsRefLargeFiles = 200

// doctorSignals adds the signals rows; warn degrades, expired only reports
// (the next signals write drops them, so it does not degrade, as v0.1.17).
func (a *App) doctorSignals(ctx context.Context, warn, report func(string)) {
	w, err := standaloneCoord{a: a}.openWorld(ctx)
	if err != nil || w.sit.Role == orgresolve.RoleClient {
		return
	}
	st := w.signalsStore()
	v, err := st.Local(ctx)
	if err != nil {
		return
	}
	if n := len(v.Board); n > signalsRefLargeFiles {
		warn(fmt.Sprintf("%s holds %d signal files (more than %d): agents may not be dropping expired claims", st.Ref, n, signalsRefLargeFiles))
	}
	if exp := v.Expired(); len(exp) > 0 {
		report(fmt.Sprintf("%s: %d expired signal file(s) still on the ref (the next signals write drops them)", st.Ref, len(exp)))
	}
	if st.Remote == nil || !st.Autopush {
		return
	}
	if err := probeAtomicPush(ctx, st); errors.Is(err, gitremote.ErrAtomicUnsupported) {
		warn("the remote does not support atomic push: claims fall back to signals first, then live (a narrow window, TEAM-036)")
	}
}

// probeAtomicPush pushes the signals ref onto itself atomically (a no-op the
// remote reports as up to date) to learn whether atomic push works. Nothing
// fetched yet: nothing to probe with, no finding.
func probeAtomicPush(ctx context.Context, st *signals.Store) error {
	tracking, ok := gitremote.TrackingRef(st.RemoteName, st.Ref)
	if !ok {
		return nil
	}
	oid, ok, err := st.Repo.ResolveRef(ctx, tracking)
	if err != nil || !ok {
		return nil
	}
	_, err = st.Remote.Push(ctx, gitremote.PushRequest{Atomic: true, Updates: []gitremote.RefUpdate{
		{Src: oid, Dst: st.Ref, Mode: gitremote.ModeLease, Expect: oid},
	}})
	return err
}
