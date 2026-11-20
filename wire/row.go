package wire

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"

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
	w, err := a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActor(ctx, st, to); aerr != nil {
			return engine.Written{}, aerr
		}
		if perr := a.moveFolderProjectFirst(ctx, st, to); perr != nil {
			return engine.Written{}, perr
		}
		w, err := st.eng.MoveWith(ctx, ref, to, opt)
		a.wroteTo(to, err)
		return w, err
	})
	if err != nil {
		return w, err
	}
	// devco D-731: a linked repo keeps nothing of a row that left it
	moved := append([]row.Row{w.Row}, alsoRows(w.Also)...)
	d, derr := a.dropMovedRepoCopies(ctx, to, moved)
	w.Warnings = append(w.Warnings, d.warnings()...)
	return w, derr
}

// alsoRows are the rows of also (a move's sub-tasks).
func alsoRows(also []engine.Changed) []row.Row {
	out := make([]row.Row, 0, len(also))
	for _, c := range also {
		if !c.Row.Deleted {
			out = append(out, c.Row)
		}
	}
	return out
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
		if perr := a.moveFolderProjectFirst(ctx, st, to); perr != nil {
			return engine.Written{}, perr
		}
		var merr error
		out, merr = st.eng.MoveMany(ctx, refs, to, opt)
		if opt.Plan {
			if merr == nil {
				merr = errMovePlanned // drops the batch: a preview writes nothing
			}
			return engine.Written{}, merr
		}
		a.wroteTo(to, merr)
		return engine.Written{}, merr
	})
	if opt.Plan {
		if errors.Is(err, errMovePlanned) {
			return out, nil
		}
		return engine.MovedMany{}, err
	}
	if err != nil || len(out.Rows) == 0 {
		return out, err
	}
	// devco D-731: a linked repo keeps nothing of the rows that left it
	moved := make([]row.Row, 0, len(out.Rows))
	for _, w := range out.Rows {
		moved = append(moved, w.Row)
	}
	d, derr := a.dropMovedRepoCopies(ctx, to, moved)
	out.Rows[0].Warnings = append(out.Rows[0].Warnings, d.warnings()...)
	return out, derr
}

// PlanMove implements opsrow.ManyMover: the bulk move's dry run (devco
// D-717, D-761). It runs MoveMany itself with opt.Plan (every row through
// the one-row move: secret scan, project and container rules, reference
// check, role guard), then drops the batch, so a preview refuses whatever
// the real run refuses and writes nothing. Before, the dry run ran only
// the secret scan and promised a move whose reference check then failed.
func (a *App) PlanMove(ctx context.Context, refs []engine.Ref, to store.Source, opt engine.MoveOptions) error {
	opt.Plan = true
	_, err := a.MoveMany(ctx, refs, to, opt)
	return err
}

// errMovePlanned ends a planned bulk move's batch (PlanMove) so
// withActorRows discards it instead of committing it.
var errMovePlanned = errors.New("bulk move planned; nothing written")

// moveFolderProjectFirst stages, in a move's batch, the project row of
// the store folder to when it has none yet (devco D-761), as
// folderProjectFirst does for a create: the engine then points every
// moved row at the folder's own project (placeFolderProject). Before,
// the row was made only after the move (ensureFolderProject), so rows
// moved into a fresh folder kept the old folder's project_id. A target
// with no folder id (a store root, the repo) or whose folder name is
// unknown (a linked folder attached under the bare org name, which has
// its row already) is left as it is.
func (a *App) moveFolderProjectFirst(ctx context.Context, st *state, to store.Source) error {
	folder := ""
	if nf, ok := ctx.Value(newFolderKey{}).(newFolder); ok && nf.target == to {
		folder = nf.folder
	} else if strings.Contains(string(to), targetSep) {
		folder = rosterFolderName(to)
	}
	if folder == "" {
		return nil
	}
	pid, err := folderProjectIDOf(ctx, st, to)
	if err != nil || pid.IsZero() {
		return nil // no folder id: the engine keeps the row's project (rehomeProject)
	}
	rows, err := st.eng.List(ctx, to, schema.TableProjects)
	if err != nil {
		return fmt.Errorf("folder project of %s: %w", to, err)
	}
	for _, r := range rows {
		if r.ID == pid.Any() && !r.Deleted {
			return nil
		}
	}
	// staged in the move's batch, the dry run's too (its batch is dropped):
	// not rosterProject, whose read guard refuses a --dry-run's access
	n, err := value.TextOf(folder)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "move: the folder name is not valid text", err)
	}
	if _, err = a.createFolderProject(ctx, st, to, pid, map[string]value.Value{rosterFieldName: n}); err != nil {
		return fmt.Errorf("folder project of %s: %w", to, err)
	}
	return nil
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
