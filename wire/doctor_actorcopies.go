package wire

// doctor_actorcopies.go — the doctor's stale actor copies (SOURCE_OF_TRUTH
// actors): one warning per copy whose copied values differ from its
// source's, naming the actor, the store, the copy's value and the
// source's, fixed by `lore actor refresh` (which rewrites them now; the
// next write to each store does too). A copy made from an older version
// of the source whose values are the same (a title edit on the roster
// moves the row's HLC) is left to actor show: nothing a reader sees is
// wrong. Copies of an actor whose source cannot be read are not judged.
// The setup check identity.actor_copies (setupprobe.go) runs the same
// finder and offers the fix as a Run it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckActorCopies is the check's JSON name.
const DoctorCheckActorCopies DoctorCheck = "actor_copies"

// actorRefreshFix is the command that rewrites stale copies now.
const actorRefreshFix = "lore actor refresh"

// staleActorCopy is one copy whose values differ from its source's.
type staleActorCopy struct {
	actor string
	store store.Source
	field string
	has   string
	want  string
	where string
}

// staleActorCopies lists every copy of every actor here whose copied
// values (name, kind; key and handle where the copy holds them) differ
// from its readable source, in actor then store order.
func (a *App) staleActorCopies(ctx context.Context) ([]staleActorCopy, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	all, err := a.actorIDs(ctx, st)
	if err != nil {
		return nil, err
	}
	var out []staleActorCopy
	for _, id := range all {
		rep := a.actorReport(ctx, st, id)
		for _, c := range rep.Stale() {
			if f, has, want, ok := firstDiff(rep.Source, c); ok {
				out = append(out, staleActorCopy{actor: actorShownName(rep, id), store: store.Source(c.Store), field: f, has: has, want: want, where: rep.Source.Where})
			}
		}
	}
	return out, nil
}

// firstDiff is the first copied field whose value differs between c and
// its source (ok false when only the stamp differs).
func firstDiff(src actorsource.Source, c actorsource.Copy) (field, has, want string, ok bool) {
	p := actorsource.Patch(src, c)
	for _, f := range []struct{ name, has string }{
		{actorsource.FieldDisplayName, c.Values.Name}, {actorsource.FieldKind, c.Values.Kind},
		{actorsource.FieldStableKey, c.Values.StableKey}, {actorsource.FieldHandle, c.Values.Handle},
	} {
		if w, changed := p[f.name]; changed {
			return f.name, f.has, w, true
		}
	}
	return "", "", "", false
}

// actorShownName names an actor in a finding: "Pat Two (act_…)".
func actorShownName(rep actorsource.Report, id ids.AnyID) string {
	if n := rep.Shown().Values.Name; n != "" {
		return n + " (" + id.String() + ")"
	}
	return id.String()
}

// DoctorActorCopies is the doctor check (registered in doctor_op.go).
func (a *App) DoctorActorCopies(ctx context.Context) ([]DoctorFinding, error) {
	stale, err := a.staleActorCopies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(stale))
	for _, s := range stale {
		out = append(out, DoctorFinding{Check: DoctorCheckActorCopies, Level: DoctorWarning, Store: s.store, Fix: actorRefreshFix,
			Message: fmt.Sprintf("actor %s: the copy in %s has %s %q, its source (%s) says %q", s.actor, s.store, s.field, s.has, s.where, s.want)})
	}
	return out, nil
}

// actorCopiesDetail is the setup check's detail: every stale copy, "; "
// separated ("" when none).
func actorCopiesDetail(stale []staleActorCopy) string {
	parts := make([]string, 0, len(stale))
	for _, s := range stale {
		parts = append(parts, fmt.Sprintf("%s in %s has %q, %s says %q", s.actor, s.store, s.has, s.where, s.want))
	}
	return strings.Join(parts, "; ")
}

// actorCopies is the identity.actor_copies probe: fail with every stale
// copy in the detail (its fix, `lore actor refresh`, is a Run it); skip
// where no lore repo is open (nothing attached to compare).
func (sp setupProber) actorCopies(ctx context.Context) opssetup.ProbeResult {
	stale, err := sp.a.staleActorCopies(ctx)
	switch {
	case errs.Has(err, errs.NotAStore):
		return skip("not inside a lore repo here, so no store copies to compare")
	case err != nil:
		return result(onboard.StatusError, err.Error(), nil)
	case len(stale) == 0:
		return result(onboard.StatusOK, "every copy of an actor here matches its source", nil)
	}
	return result(onboard.StatusFail, actorCopiesDetail(stale), nil)
}
