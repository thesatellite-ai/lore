package wire

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// MoveWith implements opsrow.Mover: App.Move with the move options
// (--drop-internal, --with-time; CLI_REFERENCE 2.7), behind the same
// guards (unreachable target, O6 format check, actor row first).
func (a *App) MoveWith(ctx context.Context, ref engine.Ref, to store.Source, opt engine.MoveOptions) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	to = st.eng.StoreName(to)
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(to)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(to); ferr != nil {
		return engine.Written{}, ferr
	}
	// batch leftover-3: staged with the copy, as App.Move
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActor(ctx, st, to); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.MoveWith(ctx, ref, to, opt)
		a.wroteTo(to, err)
		return w, err
	})
}

// MoveMany implements opsrow.ManyMover (devco D-712, `lore move --from`):
// engine.MoveMany behind MoveWith's guards (unreachable target, O6
// format check), in one staged batch with the caller's actor row in to,
// so the rows land in one write at to and one per source store.
func (a *App) MoveMany(ctx context.Context, refs []engine.Ref, to store.Source, opt engine.MoveOptions) (engine.MovedMany, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.MovedMany{}, err
	}
	to = st.eng.StoreName(to)
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(to)); uerr != nil {
		return engine.MovedMany{}, uerr
	}
	if ferr := a.checkTarget(to); ferr != nil {
		return engine.MovedMany{}, ferr
	}
	var out engine.MovedMany
	_, err = a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActor(ctx, st, to); aerr != nil {
			return engine.Written{}, aerr
		}
		var merr error
		out, merr = st.eng.MoveMany(ctx, refs, to, opt)
		a.wroteTo(to, merr)
		return engine.Written{}, merr
	})
	return out, err
}

// PlanMoveSecrets implements opsrow.ManyMover: the bulk move's secret
// pre-scan for its dry run (devco D-717), with the caller's flags;
// nothing is written.
func (a *App) PlanMoveSecrets(ctx context.Context, refs []engine.Ref, to store.Source, rerun string) error {
	eng, err := a.Engine(ctx)
	if err != nil {
		return err
	}
	_, err = eng.PlanMoveSecrets(ctx, refs, eng.StoreName(to), rerun)
	return err
}

// AttachedStore implements opsrow.ManyMover: the attached store a flag
// value names (an alias resolved, as --to is), or E_NOT_FOUND naming the
// attached stores, so `lore move --from <typo>` never reads as "no rows".
func (a *App) AttachedStore(ctx context.Context, name store.Source) (store.Source, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", err
	}
	got := eng.StoreName(name)
	if slices.Contains(eng.Stores(), got) {
		return got, nil
	}
	names := make([]string, 0, len(eng.Stores()))
	for _, s := range eng.Stores() {
		names = append(names, string(s))
	}
	return "", errs.New(errs.NotFound, "no attached store or folder "+strconv.Quote(string(name))+" (attached: "+strings.Join(names, ", ")+")").
		WithHint("`lore store list` shows the stores this repo uses; a folder is <store>/<folder>")
}
