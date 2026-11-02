// coord_doctor.go — `lore doctor` rows for signals (LORE_TEAM_SPEC F7
// Debug, §9.4): the signals ref's size, expired files nobody dropped yet,
// and whether the remote accepts atomic pushes (without it, fencing uses
// the documented two-step fallback, TEAM-036). Called with one line from
// doctor.go (doctorSignals); every check is best-effort and never fails.
package main

import (
	"context"
	"errors"
	"fmt"

	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/signals"
)

// signalsRefLargeFiles is the size past which the signals ref is reported:
// tens of agents per store branch is the design scale (§7.8), so hundreds
// of live signal files mean writers are not dropping expired ones or the
// hub (P5) is due.
const signalsRefLargeFiles = 200

// doctorSignals adds the signals rows to the report.
func doctorSignals(ctx context.Context, r *doctorReport) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return // client repos keep no signals of their own (D8)
	}
	warn := func(msg string) {
		r.Warnings = append(r.Warnings, msg)
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
		}
	}
	v, err := st.Local(ctx)
	if err != nil {
		return
	}
	if n := len(v.Board); n > signalsRefLargeFiles {
		warn(fmt.Sprintf("%s holds %d signal files (more than %d): agents may not be dropping expired claims", st.Ref, n, signalsRefLargeFiles))
	}
	if exp := v.Expired(); len(exp) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %d expired signal file(s) still on the ref (the next signals write drops them)", st.Ref, len(exp)))
	}
	if st.Remote == nil || !st.Autopush {
		return
	}
	if err := probeAtomic(ctx, st); errors.Is(err, gitremote.ErrAtomicUnsupported) {
		warn("the remote does not support atomic push: claims fall back to signals first, then live (a narrow window, TEAM-036)")
	}
}

// probeAtomic pushes the signals ref onto itself atomically (a no-op the
// remote reports as up to date) to learn whether atomic push works.
func probeAtomic(ctx context.Context, st *signals.Store) error {
	oid, ok, err := st.Repo.ResolveRef(ctx, trackingOf(st))
	if err != nil || !ok {
		return nil // nothing to probe with yet
	}
	_, err = st.Remote.Push(ctx, gitremote.PushRequest{Atomic: true, Updates: []gitremote.RefUpdate{
		{Src: oid, Dst: st.Ref, Mode: gitremote.ModeLease, Expect: oid},
	}})
	return err
}

func trackingOf(st *signals.Store) string {
	t, _ := gitremote.TrackingRef(st.RemoteName, st.Ref)
	return t
}
