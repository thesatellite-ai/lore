package wire

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The App is the operations' Env: each method opens the App on first use
// and delegates to the engine (writes pass the DOCS O6 format check first).

// Create implements opsmemory.Env.
func (a *App) Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	if target == "" || target == store.SourceRepo {
		if gerr := a.guard(ctx, table, fields, target == store.SourceRepo); gerr != nil {
			return engine.Written{}, gerr
		}
	}
	w, err := st.eng.Create(ctx, target, table, fields)
	a.wrote(err)
	return w, err
}

// Update implements opsmemory.Env.
func (a *App) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	w, err := st.eng.Update(ctx, ref, patch)
	a.wrote(err)
	return w, err
}

// Delete implements opsmemory.Env.
func (a *App) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	w, err := st.eng.Delete(ctx, ref)
	a.wrote(err)
	return w, err
}

// Get implements opsmemory.Env.
func (a *App) Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", row.Row{}, err
	}
	return eng.Get(ctx, ref)
}

// List implements opsmemory.Env.
func (a *App) List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return eng.List(ctx, source, table)
}

// Stores implements opsmemory.Env: the attached stores, or none before
// the App opened (an operation only runs after Run opened it).
func (a *App) Stores() []store.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	return a.st.eng.Stores()
}

// Sync implements opsstores.Syncer: the engine's Sync (backend sync, then
// a cache refresh). The repo store's sync is a no-op (the person pushes
// their code).
func (a *App) Sync(ctx context.Context, source store.Source) (store.SyncReport, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return store.SyncReport{}, err
	}
	return eng.Sync(ctx, source)
}

// Search implements opsmemory.Env over every store's cache.
func (a *App) Search(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return st.caches.search(ctx, table, match, limit)
}

// Project implements opsmemory.Env: the repo store's one live project
// (written by `lore init`). None is E_NOT_FOUND; several are
// E_AMBIGUOUS_PROJECT, as v0.1.17 refused them.
func (a *App) Project(ctx context.Context) (ids.ID[ids.Project], error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	switch len(rows) {
	case 0:
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, "wire: this repository has no lore project").
			WithHint("run `lore init` in the repository first (or `lore sync`, which writes the project's row file to .lore/data)")
	case 1:
		return ids.As[ids.Project](rows[0].ID)
	}
	return ids.ID[ids.Project]{}, errs.New(errs.AmbiguousProject, "wire: the repository holds several projects").
		WithHint("run `lore project list` and archive the ones you do not use")
}

// guard asks Options.GuardRepoWrite about a create in the repo store,
// passing every text value it writes (sorted by field, newline-joined).
func (a *App) guard(ctx context.Context, table row.Table, fields map[string]value.Value, explicit bool) error {
	if a.opts.GuardRepoWrite == nil {
		return nil
	}
	var texts []string
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if t, ok := fields[k].(value.Text); ok {
			texts = append(texts, t.String())
		}
	}
	return a.opts.GuardRepoWrite(ctx, table, strings.Join(texts, "\n"), explicit)
}

// repoMountField is the repos field a --repo value names.
const repoMountField = "mount_name"

// RepoID implements opsmemory.Env: a rep_ id of a live repo row, or the
// mount name of one, in the repo store (repos belong to the repo's
// project). Anything else is E_NOT_FOUND, as v0.1.17 refused it.
func (a *App) RepoID(ctx context.Context, mountOrID string) (ids.ID[ids.Repo], error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return ids.ID[ids.Repo]{}, err
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableRepos)
	if err != nil {
		return ids.ID[ids.Repo]{}, err
	}
	for _, r := range rows {
		if r.ID.String() == mountOrID || textField(r, repoMountField) == mountOrID {
			return ids.As[ids.Repo](r.ID)
		}
	}
	return ids.ID[ids.Repo]{}, errs.New(errs.NotFound, "wire: no repo "+strconv.Quote(mountOrID)+" in this project").
		WithHint("run `lore repo list` to see the mount names")
}

// writeState opens the App and applies the O6 format check.
func (a *App) writeState(ctx context.Context) (*state, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := st.format.check(); err != nil {
		return nil, err
	}
	return st, nil
}

// wrote records that a 0.1.18 write landed: the O6 question never comes
// back once a `_v: 2` row exists.
func (a *App) wrote(err error) {
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st != nil {
		a.st.format.written = true
	}
}
