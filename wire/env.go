package wire

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
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

// AuditSecret implements settings.SecretAuditor: one secret.* audit event
// (SECRETS_AND_REFERENCES 16), never a value.
func (a *App) AuditSecret(ctx context.Context, action engine.Action, s engine.SecretAudit) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	return st.eng.AuditSecret(ctx, action, s)
}

// Create implements opsmemory.Env.
func (a *App) Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	if uerr := a.unreachableGuard(ctx, st, target); uerr != nil {
		return engine.Written{}, uerr
	}
	q, err := a.writeQuery(st, table, fields)
	if err != nil {
		return engine.Written{}, err
	}
	if target == "" && a.opts.DefaultTarget != nil {
		to, derr := a.opts.DefaultTarget(ctx, q)
		if derr != nil {
			return engine.Written{}, derr
		}
		if to != store.SourceRepo {
			target = to
		}
	}
	if target == "" || target == store.SourceRepo {
		if gerr := a.guard(ctx, q, target == store.SourceRepo); gerr != nil {
			return engine.Written{}, gerr
		}
	}
	if ferr := a.checkTarget(target); ferr != nil {
		return engine.Written{}, ferr
	}
	// G-ENGINE-2: the actor rows and the row land as one batch, so a refused
	// create writes nothing (not even the caller's actor row)
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActor(ctx, st, target); aerr != nil {
			return engine.Written{}, aerr
		}
		if aerr := a.ensureNamedActors(ctx, st, target, table, fields); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.Create(ctx, target, table, fields)
		a.wroteTo(target, err)
		return w, err
	})
}

// Update implements opsmemory.Env.
func (a *App) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	src := refStore(ctx, st, ref)
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(src)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
		return engine.Written{}, aerr
	}
	if aerr := a.ensureNamedActors(ctx, st, src, ref.Table, patch); aerr != nil {
		return engine.Written{}, aerr
	}
	at, was, undoable := priorOf(ctx, st.eng, ref) // R14: an edit batch keeps each row's before-image
	w, err := st.eng.Update(ctx, ref, patch)
	a.wroteTo(src, err)
	if err == nil && undoable {
		w = a.keepPrior(ctx, at, was, w)
	}
	return w, err
}

// Delete implements opsmemory.Env.
func (a *App) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	src := refStore(ctx, st, ref)
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
		return engine.Written{}, aerr
	}
	w, err := st.eng.Delete(ctx, ref)
	a.wroteTo(src, err)
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

// SearchColumns implements opsmemory.ColumnSearcher: Search with the
// match restricted to the named full-text fields (the sqlite cache's FTS5
// column filter).
func (a *App) SearchColumns(ctx context.Context, table row.Table, match string, fields []string, limit int) ([]opsmemory.Hit, error) {
	return a.Search(ctx, table, sqlite.ColumnMatch(fields, match), limit)
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
	// FA-AGENT-10: a `repo fork --new-project` clone holds its upstream's
	// project row too; _meta.json's project_id names the checkout's own.
	if top, terr := a.knownRepoTop(ctx); terr == nil {
		if pid, ok := repoMetaProjectID(top); ok {
			for _, r := range rows {
				if r.ID.String() == pid {
					return ids.As[ids.Project](r.ID)
				}
			}
		}
	}
	if a.opts.ProjectHint != nil {
		if hint, ok := a.opts.ProjectHint(ctx); ok {
			for _, r := range rows {
				if r.ID.String() == hint {
					return ids.As[ids.Project](r.ID)
				}
			}
		}
	}
	return ids.ID[ids.Project]{}, errs.New(errs.AmbiguousProject, "wire: the repository holds several projects").
		WithHint("run `lore project list` and archive the ones you do not use")
}

// guard asks Options.GuardRepoWrite about a create in the repo store.
func (a *App) guard(ctx context.Context, q WriteQuery, explicit bool) error {
	if a.opts.GuardRepoWrite == nil {
		return nil
	}
	return a.opts.GuardRepoWrite(ctx, q, explicit)
}

// writeQuery describes a create for the write hooks: every text value it
// writes (sorted by field, newline-joined) and, only when a hook needs it,
// the effective client-repo routes from the policies rows.
func (a *App) writeQuery(st *state, table row.Table, fields map[string]value.Value) (WriteQuery, error) {
	q := WriteQuery{Table: table}
	if a.opts.GuardRepoWrite == nil && a.opts.DefaultTarget == nil {
		return q, nil
	}
	var texts []string
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if t, ok := fields[k].(value.Text); ok {
			texts = append(texts, t.String())
		}
	}
	q.Text = strings.Join(texts, "\n")
	set, err := st.eng.Policy(st.defaultTarget)
	if err != nil {
		return WriteQuery{}, err
	}
	q.Routes = set.ClientRoutes()
	return q, nil
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

// GroupRepoID resolves `--repo <name>` for a write whose target is to.
//
// Why it exists: a task filed into a store folder (`--to northwind/globex
// --repo backend`) names a repo of THAT folder's project, which the repo
// store's repos table does not hold. v0.1.17 (ensureGroupRepo/repoIDFor)
// found or created a repos row {project_id: the folder's project,
// mount_name: name} in the store; this keeps that behaviour on the core.
//
// For the repo store (to "" or repo) it is RepoID: an existing row only,
// else E_NOT_FOUND. For any other target it returns the live repos row of
// to whose mount_name (or id) is name, else creates one there through the
// App's write path (the folder's project row is made first when the folder
// has none, as the roster does). The create uses the store's natural id,
// so a second call, or another clone, finds the same row; each store keeps
// its own rows. The row stays in to, so the task's ref never crosses a
// store (F15 is unaffected).
func (a *App) GroupRepoID(ctx context.Context, to store.Source, name string) (ids.AnyID, error) {
	if to == "" || to == store.SourceRepo {
		id, err := a.RepoID(ctx, name)
		if err != nil {
			return ids.AnyID{}, err
		}
		return id.Any(), nil
	}
	if strings.TrimSpace(name) == "" {
		return ids.AnyID{}, errs.New(errs.InvalidInput, "wire: --repo needs a mount name")
	}
	st, err := a.writeState(ctx)
	if err != nil {
		return ids.AnyID{}, err
	}
	rows, err := st.eng.List(ctx, to, schema.TableRepos)
	if err != nil {
		return ids.AnyID{}, err
	}
	for _, r := range rows {
		if !r.Deleted && (r.ID.String() == name || textField(r, repoMountField) == name) {
			return r.ID, nil
		}
	}
	folderName := string(to)
	if _, f, ok := strings.Cut(folderName, targetSep); ok {
		folderName = f
	}
	project, err := a.rosterProject(ctx, st, to, folderName)
	if err != nil {
		return ids.AnyID{}, err
	}
	mount, err := value.TextOf(name)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.InvalidInput, "wire: the repo name is not valid text", err)
	}
	w, err := a.Create(ctx, to, schema.TableRepos, map[string]value.Value{repoProjectField: project, repoMountField: mount})
	if err != nil {
		return ids.AnyID{}, err
	}
	return w.Row.ID, nil
}

// repoProjectField is the repos field naming the repo's project.
const repoProjectField = "project_id"

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

// checkTarget is the O6 question of an attached v0.1.17 org store before
// a write into it (target "" is the default target: the repo, checked by
// writeState).
func (a *App) checkTarget(target store.Source) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	return a.st.storeFormat[target].checkStore(target)
}

// wroteTo records a landed write into target, so its O6 question is not
// asked again in this process.
func (a *App) wroteTo(target store.Source, err error) {
	a.wrote(err)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return
	}
	if f, ok := a.st.storeFormat[target]; ok {
		f.written = true
		a.st.storeFormat[target] = f
	}
}

// refStore is the store holding ref's row (ref.Store, else found by id;
// "" when it cannot be found: the write itself reports that).
func refStore(ctx context.Context, st *state, ref engine.Ref) store.Source {
	if ref.Store != "" {
		return ref.Store
	}
	s, _, err := st.eng.Get(ctx, ref)
	if err != nil {
		return ""
	}
	return s
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

// ensureActorFor writes the actor row into the store holding ref's row.
func (a *App) ensureActorFor(ctx context.Context, st *state, ref engine.Ref) error {
	src := ref.Store
	if src == "" {
		s, _, err := st.eng.Get(ctx, ref)
		if err != nil {
			return nil // the write itself reports the missing row
		}
		src = s
	}
	return a.ensureActor(ctx, st, src)
}

// Move implements opsmemory.Mover: the engine's Move (the row keeps its id,
// the old copy is tombstoned), after the O6 format check, with the actor
// row written into the destination first.
func (a *App) Move(ctx context.Context, ref engine.Ref, to store.Source) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(to)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(to); ferr != nil {
		return engine.Written{}, ferr
	}
	if aerr := a.ensureActor(ctx, st, to); aerr != nil {
		return engine.Written{}, aerr
	}
	w, err := st.eng.Move(ctx, ref, to)
	a.wroteTo(to, err)
	return w, err
}

// CreateWithID is engine.CreateWithID behind the same write path as every
// other App write (O6 format check, actor row first): the caller pins the
// row's id. Settings ops use it to re-create a natural-key row (a policy
// key+scope, a var name, a kind name) after a soft delete, because the
// tombstone keeps the natural id forever (DQ) and a plain Create would
// collide with it; they pass a fresh random id instead.
func (a *App) CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	if uerr := a.unreachableGuard(ctx, st, target); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(target); ferr != nil {
		return engine.Written{}, ferr
	}
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) { // G-ENGINE-2
		if aerr := a.ensureActor(ctx, st, target); aerr != nil {
			return engine.Written{}, aerr
		}
		if aerr := a.ensureNamedActors(ctx, st, target, table, fields); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.CreateWithID(ctx, target, table, id, fields)
		a.wroteTo(target, err)
		return w, err
	})
}

// explicitTarget is the target a write into an existing row's store names:
// the repo store is named explicitly (src is never "" for a found row).
func explicitTarget(src store.Source) store.Source {
	if src == "" {
		return store.SourceRepo
	}
	return src
}

// unreachableGuard fails a write closed while an org store (the one that
// holds the repo's links and the internal rows the leak check compares
// against) could not be opened: it has no local copy and is offline
// (state.unreachable). Reads are never stopped (spec 5.4). Per situation,
// v0.1.17's codes:
//   - no explicit target (target ""): where the row should go depends on
//     the links, which cannot be read: E_LINK_UNKNOWN (exit 18);
//   - the repo store of a client repo (RepoIsClient true, or unknown: fail
//     closed): the write-time leak check cannot run: E_LEAK_BLOCKED
//     "cannot check";
//   - the unreachable store itself: its own E_STORE_UNREACHABLE (a store
//     with no local copy, spec 5.4).
//
// Any other target (an internal repo's repo store, another store) writes.
func (a *App) unreachableGuard(ctx context.Context, st *state, target store.Source) error {
	if len(st.unreachable) == 0 {
		return nil
	}
	if err, ok := st.unreachable[target]; ok {
		return err
	}
	names := make([]string, 0, len(st.unreachable))
	for n := range st.unreachable {
		names = append(names, string(n))
	}
	slices.Sort(names)
	which := strings.Join(names, ", ")
	switch target {
	case "":
		return errs.New(errs.LinkUnknown, "cannot tell where this write belongs: org store "+which+" has no local copy and cannot be reached").
			WithHint("go online once (the store is then kept locally), or name the target: --to repo or --to <store>")
	case store.SourceRepo:
		if client, _ := repoIsClient(ctx, a.opts); client {
			return errs.New(errs.LeakBlocked, "cannot check this write for internal content: org store "+which+" has no local copy and cannot be reached").
				WithHint("go online once so the store is kept locally, then write again")
		}
	case store.SourceMachine:
		// Machine-local rows never leave this machine: no link decides
		// where they go and no leak check compares them, so an
		// unreachable org store does not stop them.
	}
	return nil
}
