package wire

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
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

// createFrame is the part of a create that comes before the write: the
// store a row with no --to lands in (DefaultTarget), the write gate of
// that store, the repo guard and the target check. Create and
// CreateWithID share it, so a keyed add (`--key`, devco D-439) is routed
// and guarded exactly like a plain one.
func (a *App) createFrame(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (*state, store.Source, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, "", err
	}
	target = st.eng.StoreName(target) // devco T-5: an org store's own name answers for it
	if uerr := a.unreachableGuard(ctx, st, target); uerr != nil {
		return nil, "", uerr
	}
	q, err := a.writeQuery(ctx, st, table, fields)
	if err != nil {
		return nil, "", err
	}
	if target == "" && (a.opts.DefaultTarget != nil || a.convertedModeBRepo(ctx)) {
		to, derr := a.routedDefault(ctx, q)
		if derr != nil {
			return nil, "", derr
		}
		if to != store.SourceRepo {
			target = to
		}
	}
	if st, err = a.writeStateFor(ctx, target); err != nil {
		return nil, "", err
	}
	if target == "" || target == store.SourceRepo {
		if gerr := a.guard(ctx, q, target == store.SourceRepo); gerr != nil {
			return nil, "", gerr
		}
	}
	if ferr := a.checkTarget(target); ferr != nil {
		return nil, "", ferr
	}
	return st, target, nil
}

// Create implements opsmemory.Env.
//
// devco:T-4: where a row with no --to goes is decided before the write
// gate (writeStateFor), so the gate asks about the store the row really
// lands in: in a linked client repo still on the 0.1.17 format a memory
// routed to the org's client folder is not refused for the repo's format.
func (a *App) Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	st, target, err := a.createFrame(ctx, target, table, fields)
	if err != nil {
		return engine.Written{}, err
	}
	// A machine-local row (this machine's session row) writes no actor row
	// (ensureActor skips the machine store, P3-LOCAL-1), so it needs no
	// batch, and it must not take actorMu: the audit of an actor-row write
	// creates the session row while ensureActor holds actorMu (B-FLOW4-2).
	if target == store.SourceMachine {
		w, err := st.eng.Create(ctx, target, table, fields)
		a.wroteTo(target, err)
		return w, err
	}
	// G-ENGINE-2: the actor rows and the row land as one batch, so a refused
	// create writes nothing (not even the caller's actor row)
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if perr := a.createRowsFirst(ctx, st, target, table, fields); perr != nil {
			return engine.Written{}, perr
		}
		w, err := st.eng.Create(ctx, target, table, fields)
		a.wroteTo(target, err)
		return w, err
	})
}

// createRowsFirst stages, in the open batch of a create (Create and
// CreateWithID alike), the rows the new row needs before it: the caller's
// actor row, the actors the row names, and the folder project row of a
// `--to <store>/<folder>` write (folderProjectFirst). Both create paths
// run this one step so neither can miss a row the other stages
// (devco:D-746: task add's default list is made with CreateWithID, which
// skipped folderProjectFirst, so once D-740 stopped targetFolderProject
// from committing the folder's project row on the side, the list's
// project_id named a row no store held and `task add --to <org>/<folder>`
// failed E_NOT_FOUND).
func (a *App) createRowsFirst(ctx context.Context, st *state, target store.Source, table row.Table, fields map[string]value.Value) error {
	if err := a.ensureActor(ctx, st, target); err != nil {
		return err
	}
	if err := a.ensureNamedActors(ctx, st, target, table, fields); err != nil {
		return err
	}
	return a.folderProjectFirst(ctx, st, target, fields)
}

// Update implements opsmemory.Env.
func (a *App) Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	st, src, err := a.writeStateForRef(ctx, ref)
	if err != nil {
		return engine.Written{}, err
	}
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(src)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	// G-ENGINE-2: the actor rows and the edit land as one batch, so a
	// refused edit (a landed-not-pulled row under --no-live, a unique
	// name) writes nothing, not even the caller's first actor row.
	at, was, undoable := priorOf(ctx, st.eng, ref) // R14: an edit batch keeps each row's before-image
	// A machine-local row (this machine's session row) writes no actor row,
	// so it takes no batch and never actorMu, as in Create (B-FLOW4-2): the
	// audit of a write made while ensureActor holds actorMu (a move's or a
	// trash restore's first write into a store) updates the session row, and
	// taking actorMu here deadlocked that write (`lore move --yes` under an
	// agent hung for good, 2026-11-08).
	if src == store.SourceMachine {
		w, err := st.eng.Update(ctx, ref, patch)
		a.wroteTo(src, err)
		if err == nil && undoable {
			w = a.keepPrior(ctx, at, was, w)
		}
		return w, err
	}
	w, err := a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
			return engine.Written{}, aerr
		}
		if aerr := a.ensureNamedActors(ctx, st, src, ref.Table, patch); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.Update(ctx, ref, patch)
		a.wroteTo(src, err)
		return w, err
	})
	if err == nil && undoable {
		w = a.keepPrior(ctx, at, was, w)
	}
	return w, err
}

// DeleteMany implements wheredel.ManyDeleter: every ref to the trash as
// ONE delete (engine.TrashMany: one plan, one batch, one store write per
// store; owner report 2026-11-12), the caller's actor row in the same
// write, as Delete.
func (a *App) DeleteMany(ctx context.Context, refs []engine.Ref) error {
	if len(refs) == 0 {
		return nil
	}
	st, err := a.writeStateForRefs(ctx, refs)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ferr := a.checkTarget(refStore(ctx, st, ref)); ferr != nil {
			return ferr
		}
	}
	_, err = a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActorFor(ctx, st, refs[0]); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.TrashMany(ctx, refs)
		for _, ref := range refs {
			a.wroteTo(refStore(ctx, st, ref), err)
		}
		return w, err
	})
	return err
}

// Delete implements opsmemory.Env.
func (a *App) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	st, src, err := a.writeStateForRef(ctx, ref)
	if err != nil {
		return engine.Written{}, err
	}
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	// A machine-local row needs no actor row and must not take actorMu
	// (see Update).
	if src == store.SourceMachine {
		w, err := st.eng.Delete(ctx, ref)
		a.wroteTo(src, err)
		return w, err
	}
	// G-ENGINE-2: one batch with the actor row, as Create and Update.
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.Delete(ctx, ref)
		a.wroteTo(src, err)
		return w, err
	})
}

// Get implements opsmemory.Env.
func (a *App) Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", row.Row{}, err
	}
	src, r, err := eng.Get(ctx, ref)
	if err == nil && ref.Store == "" && ref.ID.Prefix() == ids.PrefixActor && len(eng.Stores()) > 1 {
		// one actor in several stores (actor_source.go): the engine answers
		// the newest copy; the source's row and name are what readers see
		if s2, r2, aerr := a.actorSourceGet(ctx, ref); aerr == nil && s2 != "" {
			return s2, r2, nil
		}
	}
	return src, r, err
}

// List implements opsmemory.Env.
func (a *App) List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	if source == store.SourceRepo && a.noRepoStoreHere(ctx, eng) {
		if access, known := engine.AccessOf(ctx); known && access == op.AccessRead {
			// devco D-631: a read under a setup from a plain folder has no
			// repo store, so it has no repo rows; the setup's stores are
			// read beside it (repo list, context's render sources). A
			// write still gets the engine's refusal.
			return nil, nil
		}
	}
	return eng.List(ctx, source, table)
}

// noRepoStoreHere reports a store-only open (runOnDefaultStore: a read
// under a setup from a plain folder) where no attached store answers for
// the repo store: there is no "this repo" to read rows or a project of.
func (a *App) noRepoStoreHere(ctx context.Context, eng *engine.Engine) bool {
	st, err := a.state(ctx)
	return err == nil && st.storeOnly && st.here == "" && !eng.AnswersRepo()
}

// SetupReadStores is every attached store when the open is a read under
// a setup from a plain folder (noRepoStoreHere), ok false anywhere else.
// devco D-632: `lore context --setup <setup>` reads the setup's stores as
// its scope, as search and the lists do there; its render sources (the
// repo store plus the shareable ones) found none of them, so --topics
// said "(no rows yet)".
func (a *App) SetupReadStores(ctx context.Context) ([]store.Source, bool) {
	eng, err := a.Engine(ctx)
	if err != nil || !a.noRepoStoreHere(ctx, eng) {
		return nil, false
	}
	return engine.ReadStoresOf(eng), true
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

// shadowedStores are the attached names a read across stores skips: in
// Stores() but not in ReadStores() (a later name of a place attached
// twice). nil when eng is not open yet.
func shadowedStores(eng *engine.Engine) map[store.Source]bool {
	if eng == nil {
		return nil
	}
	keep := map[store.Source]bool{}
	for _, s := range eng.ReadStores() {
		keep[s] = true
	}
	out := map[store.Source]bool{}
	for _, s := range eng.Stores() {
		if !keep[s] {
			out[s] = true
		}
	}
	return out
}

// ReadStores implements engine.ReadStorer: the stores a read across
// every store walks (Engine.ReadStores: a place attached under two names
// is read once, through the first), or none before the App opened.
func (a *App) ReadStores() []store.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	return a.st.eng.ReadStores()
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
	return st.caches.search(ctx, table, match, limit, shadowedStores(st.eng))
}

// SearchBare is Search with no snippets: a topic word's own search in
// `lore search` only counts which rows hold the word (devco:D-808).
func (a *App) SearchBare(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return st.caches.searchWith(ctx, table, match, limit, shadowedStores(st.eng), true)
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
	if st, serr := a.state(ctx); serr == nil && st.here != "" {
		return a.folderProject(ctx, st, st.here) // inside a store folder: its project (storehere.go)
	}
	if pid, ok, ferr := a.targetFolderProject(ctx); ferr != nil || ok {
		return pid, ferr // D-718: --to <store>/<folder> names the project too
	}
	if a.noRepoStoreHere(ctx, eng) {
		// devco D-631: a read under a setup from a plain folder opens the
		// setup's stores alone (runOnDefaultStore), none of them the repo
		// store, so "this repo's project" is nobody's: the place has no
		// project, and a read that filters by it reads every project's rows
		// (engine.EveryProject), as rule and memory list do. Before it every
		// content list (playbook, pattern, handoff, …), link, tag and repo
		// list failed "no store named repo is attached".
		return ids.ID[ids.Project]{}, errs.Wrap(errs.NotFound, "this place is no repo and no store folder, so it has no project row yet, and a read never makes one", engine.ErrNoPlaceProject).
			WithHint("name a folder with --store <org>/<folder> to read one project's rows")
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	switch len(rows) {
	case 0:
		if st, serr := a.state(ctx); serr == nil && st.linkedByRemote != "" {
			return a.linkedFolderProject(ctx, st)
		}
		if pid, ok, lerr := a.linkedRepoProject(ctx, eng); ok {
			return pid, lerr // D-736: a linked repo's project is its folder's (linkidentity.go)
		}
		if pid, ok := a.projectInAStore(ctx, eng); ok {
			return pid, nil // owner item 36: a converted Mode B repo's project is in its "shared" store
		}
		if top, terr := a.knownRepoTop(ctx); terr == nil && hasV017Rows(top) {
			// owner item 22: the project row is there, in the format lore
			// 0.1.17 wrote, which is not read until it is migrated
			return ids.ID[ids.Project]{}, errs.New(errs.NotFound, "wire: this repository's lore project is in rows lore 0.1.17 wrote, which are not migrated yet").
				WithHint("run lore fix (it migrates them; `lore migrate status` shows what is pending)")
		}
		if sl, ok := a.findStaleLink(ctx); ok {
			// L-7: say why the link is gone instead of only offering init
			return ids.ID[ids.Project]{}, staleLinkErr(sl, a.staleLinkOrg(ctx))
		}
		if e := a.checkoutWithoutLoreErr(ctx); e != nil {
			return ids.ID[ids.Project]{}, e // D-8/D-10: never "run lore init" there
		}
		return ids.ID[ids.Project]{}, noLoreProjectErr()
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

// noLoreProject is the words of Project's E_NOT_FOUND for a repository
// with no project at all (lore fix's render step skips on it, owner item 36).
const noLoreProject = "this repository has no lore project"

// projectInAStore is the project of a repo whose repo store holds no
// project row but which names one that an attached store holds: the
// project_id of a converted Mode B pointer (its rows became the "shared"
// folder store, migrate_modeb.go), else _meta.json's project_id. A
// tombstone or an id no attached store holds is not a project.
func (a *App) projectInAStore(ctx context.Context, eng *engine.Engine) (ids.ID[ids.Project], bool) {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return ids.ID[ids.Project]{}, false
	}
	var want []string
	if pid, ok := convertedModeBProjectID(top); ok {
		want = append(want, pid)
	}
	if pid, ok := repoMetaProjectID(top); ok {
		want = append(want, pid)
	}
	for _, s := range want {
		pid, perr := ids.Parse[ids.Project](s)
		if perr != nil {
			continue
		}
		if _, r, gerr := eng.Get(ctx, engine.Ref{Table: schema.TableProjects, ID: pid.Any()}); gerr == nil && !r.Deleted {
			return pid, true
		}
	}
	// devco D-855: a converted repo whose rows a person moved into a folder
	// of the converted store and attached: that folder's project
	if len(want) > 0 {
		if src, ok := convertedModeBFolder(top); ok {
			if st, serr := a.state(ctx); serr == nil && st.byName[src] != nil {
				if pid, perr := a.folderProject(ctx, st, src); perr == nil && !pid.IsZero() {
					return pid, true
				}
			}
		}
	}
	return ids.ID[ids.Project]{}, false
}

// convertedModeBRepo reports whether the repo this command runs in was
// converted from the retired shared-database mode (it keeps the pointer
// as .lore/lore.yaml.migrated); a cheap file test, before any store opens.
func (a *App) convertedModeBRepo(ctx context.Context) bool {
	if inStore(a.opts.RepoDir) {
		return false
	}
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return false
	}
	_, ok := convertedModeBProjectID(top)
	return ok
}

// convertedModeBHome is the attached store a converted shared-database
// repo's project lives in (devco D-855): the store holding the project
// row its kept pointer names, when the repo store holds no project of its
// own (a repo that ran lore init since keeps writing to itself). ok is
// false for every other repo. A write with no --to lands there
// (routedDefault), as Project() finds the project there
// (projectInAStore).
func (a *App) convertedModeBHome(ctx context.Context) (store.Source, bool) {
	if !a.convertedModeBRepo(ctx) {
		return "", false
	}
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return "", false
	}
	text, _ := convertedModeBProjectID(top)
	pid, err := ids.Parse[ids.Project](text)
	if err != nil {
		return "", false
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return "", false
	}
	own, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return "", false
	}
	for _, r := range own {
		if !r.Deleted {
			return "", false
		}
	}
	src, r, err := eng.Get(ctx, engine.Ref{Table: schema.TableProjects, ID: pid.Any()})
	if err == nil && !r.Deleted && src != store.SourceRepo && src != "" {
		return src, true
	}
	return convertedModeBFolder(top)
}

// convertedModeBFolder is the one stores.yaml entry of a converted repo
// that attaches a folder of the converted store (<db dir>/stores/<name>):
// the folder a person linked by hand after an older conversion left the
// rows in the store's root (store folder add, then move --to
// shared/<folder>), whose project is the folder's own, not the kept
// pointer's. ok is false when there is none or more than one.
func convertedModeBFolder(top string) (store.Source, bool) {
	cfg, err := readStoresFile(top)
	if err != nil {
		return "", false
	}
	var found []store.Source
	for _, s := range cfg.Stores {
		dir := fileuri.LocalPath(s.URL)
		if s.Folder != "" && dir != "" && filepath.Base(filepath.Dir(dir)) == modeBStoresDir {
			found = append(found, s.Name)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// guard asks Options.GuardRepoWrite about a create in the repo store.
func (a *App) guard(ctx context.Context, q WriteQuery, explicit bool) error {
	if a.opts.GuardRepoWrite == nil || inStore(a.opts.RepoDir) {
		// a store checkout's "repo store" is the store folder it runs in,
		// no code repo: the repo guards (F14 routing, client leak guards)
		// have nothing to guard there (devco D-693: once the clone resolved
		// its store's org they refused every plain write in it)
		return nil
	}
	return a.opts.GuardRepoWrite(ctx, q, explicit)
}

// writeQuery describes a create for the write hooks: every text value it
// writes (sorted by field, newline-joined) and, only when a hook needs it,
// the effective client-repo routes from the policies rows.
func (a *App) writeQuery(ctx context.Context, st *state, table row.Table, fields map[string]value.Value) (WriteQuery, error) {
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
	set, err := st.eng.Policy(ctx, st.defaultTarget)
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
	// devco D-736: a cleaned linked repo has no repos row; its own id
	// (and its mount name) still name it, the identity being the link.
	if id, ok := a.linkedOwnRepo(ctx, mountOrID); ok {
		return id, nil
	}
	// devco D-491: in a store folder `--repo org` names a folder (a
	// project of the store), which --project resolves; say so instead of
	// a bare "no repo".
	if prj, perr := eng.List(ctx, store.SourceRepo, schema.TableProjects); perr == nil {
		for _, r := range prj {
			if !r.Deleted && (r.ID.String() == mountOrID || textField(r, projectNameField) == mountOrID) {
				return ids.ID[ids.Repo]{}, errs.New(errs.NotFound, "wire: "+strconv.Quote(mountOrID)+" is a project (a store folder), not a repo; --repo names a code repo mounted in this project").
					WithHint("name the project with --project " + mountOrID + "; `lore repo list` shows the repos")
			}
		}
	}
	return ids.ID[ids.Repo]{}, errs.New(errs.NotFound, "wire: no repo "+strconv.Quote(mountOrID)+" in this project").
		WithHint("run `lore repo list` to see the mount names")
}

// projectNameField is the projects field a --project name matches.
const projectNameField = "name"

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

// writeState opens the App and applies the O6 format check and the setup
// write gate (a write whose target is not known here: gated).
func (a *App) writeState(ctx context.Context) (*state, error) {
	return a.writeStateFor(ctx, "")
}

// writeStateFor is writeState for a write into target. A write into the
// machine store (setup_progress, known_repos, sessions: this machine's own
// bookkeeping, never shared) skips the setup write gate: a brand-new setup
// must be able to save its progress on a machine whose folders are still
// ambiguous, which is the very thing the setup is fixing (ONB 3.3, 3.4).
func (a *App) writeStateFor(ctx context.Context, target store.Source) (*state, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// F-MIGRATE-1: `lore migrate run` is the answer to the O6 question,
	// so its own writes (the machine-local copy, the session row its first
	// audited write creates, …) pass; every other write still asks.
	// The question is about this repository's row files: a write whose
	// target is another store (an org store's roster, `--to <store>`, the
	// machine store) changes none of them, so the repo's format never
	// blocks it (an attached v0.1.17 store asks its own, checkTarget).
	repoTarget := target == "" || target == store.SourceRepo
	// D13: a project on the 0.1.17 format is never written by this lore,
	// so the old "has everyone upgraded?" question is not asked; the write
	// is refused (formatold.go); an unreadable _meta.json refuses with its
	// own reason (ProjectWriteRefusal)
	if repoTarget && !opsmigrate.InRun(ctx) {
		if err := ProjectWriteRefusal(st.repoTop); err != nil {
			return nil, err
		}
	}
	if err := st.format.check(); err != nil && repoTarget && !opsmigrate.InRun(ctx) {
		return nil, err
	}
	// ONBOARDING 3.3: an unresolvable setup or a setup/org disagreement
	// refuses writes (reads still work).
	if target != store.SourceMachine {
		if err := a.setupWriteGate(ctx); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// writeStateForRef is writeStateFor for an edit of ref's row, gated by
// the store that row is in: a ref without a store (a machine-local
// sessions row's, a row found by id) is looked up first, so an edit of a
// row outside the repo never meets the repo's format refusal (fmt1: in a
// project on the 0.1.17 format, the session row's update made by the
// first audited write of a process refused every setup step and render
// history write with "is stored but its audit record failed"). A ref
// that cannot be found gates as a repo write (fail closed); the write
// itself then reports it missing.
func (a *App) writeStateForRef(ctx context.Context, ref engine.Ref) (*state, store.Source, error) {
	if ref.Store != "" {
		st, err := a.writeStateFor(ctx, ref.Store)
		return st, ref.Store, err
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, "", err
	}
	src := refStore(ctx, st, ref)
	st, err = a.writeStateFor(ctx, src)
	return st, src, err
}

// writeStateForRefs is writeStateForRef for several refs written as one:
// gated as a write into their one store when they all resolve to the same
// store, else as a repo write (every gate applies).
func (a *App) writeStateForRefs(ctx context.Context, refs []engine.Ref) (*state, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	target := refStore(ctx, st, refs[0])
	for _, ref := range refs[1:] {
		if refStore(ctx, st, ref) != target {
			target = ""
			break
		}
	}
	return a.writeStateFor(ctx, target)
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
	// batch leftover-3 (actor row staged with the copy) and devco D-731
	// (a linked repo keeps nothing of a row that left it): MoveWith does both
	return a.MoveWith(ctx, ref, to, engine.MoveOptions{})
}

// CreateWithID is engine.CreateWithID behind the same write path as every
// other App write (O6 format check, actor row first): the caller pins the
// row's id. Settings ops use it to re-create a natural-key row (a policy
// key+scope, a var name, a kind name) after a soft delete, because the
// tombstone keeps the natural id forever (DQ) and a plain Create would
// collide with it; they pass a fresh random id instead.
//
// The write gate asks about target, the store the row lands in (devco:T-4:
// task add's default task list in a linked client repo goes to the org's
// client folder, and the repo's 0.1.17 format refused it).
func (a *App) CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error) {
	st, target, err := a.createFrame(ctx, target, table, fields)
	if err != nil {
		return engine.Written{}, err
	}
	return a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) { // G-ENGINE-2
		if perr := a.createRowsFirst(ctx, st, target, table, fields); perr != nil {
			return engine.Written{}, perr
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
//     "cannot check"; a write that carries metadata answers
//     E_META_STORE_UNREADABLE instead (METADATA 3.11 rule 2, section 6:
//     a key it registers could shadow a store key, G-META-12);
//   - the unreachable store itself: its own E_STORE_UNREACHABLE (a store
//     with no local copy, spec 5.4).
//
// Any other target (an internal repo's repo store, another store) writes.
// The hint's first step depends on why the stores could not be opened
// (unreachableHint).
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
			WithHint(unreachableHint(st.unreachable, "go online once (the store is then kept locally)", ", or name the target: --to repo or --to <store>"))
	case store.SourceRepo:
		if client, _ := repoIsClient(ctx, a.opts); client {
			if engine.MetaBound(ctx) {
				// METADATA 3.11 rule 2 / 6: a metadata write in a client
				// repo may register a key the unreadable store already
				// defines (an internal one): refused until it can be read
				return errs.New(errs.MetaStoreUnreadable, "cannot check this metadata write against the store's keys: org store "+which+" has no local copy and cannot be reached").
					WithHint(unreachableHint(st.unreachable, "make the store readable (go online once, or lore sync in the store)", ", then write again; nothing written"))
			}
			return errs.New(errs.LeakBlocked, "cannot check this write for internal content: org store "+which+" has no local copy and cannot be reached").
				WithHint(unreachableHint(st.unreachable, "go online once so the store is kept locally", ", then write again"))
		}
	case store.SourceMachine:
		// Machine-local rows never leave this machine: no link decides
		// where they go and no leak check compares them, so an
		// unreachable org store does not stop them.
	}
	return nil
}

// unreachableHint is a write guard's hint for the stores that could not be
// opened: offline (the first step for a remote that never answered) when
// none of them refused this machine, else git.HintAccessRefused, because a
// remote that refused the ssh key or GitHub account will not open by going
// online (setupe2e v0117: an old store url fetched with another account's
// key). rest is the guard's own tail (", then write again"), kept either way.
func unreachableHint(unreachable map[store.Source]error, offline, rest string) string {
	for _, err := range unreachable {
		if git.AccessRefused(err) {
			return git.HintAccessRefused + rest
		}
	}
	return offline + rest
}

// linkedFolderProject is the project of a repo that holds no lore data and
// is linked by its remote (state.linkedByRemote): the project of the store
// folder it is linked to, whose row lives in that folder, never in the
// repo. A write creates the row when the folder has none yet (rosterProject:
// its id is the folder's _meta.json project_id, so two machines agree); a
// read only names that id.
func (a *App) linkedFolderProject(ctx context.Context, st *state) (ids.ID[ids.Project], error) {
	src, ok := orgAliases(ctx, a.opts, st.repoTop)[attach.ReservedTeam]
	if !ok || st.byName[src] == nil {
		return ids.ID[ids.Project]{}, errs.New(errs.StoreUnreachable, "wire: this repo is linked to a store folder by its remote ("+st.linkedByRemote+"), and that folder is not attached").
			WithHint("run `lore whoami` to see the link, or `lore sync` to fetch the store")
	}
	return a.folderProject(ctx, st, src)
}

// folderProject is the project of the attached store folder src: on a
// read its marker's project id when it has one, else (and on a write) its
// roster project row, made when missing.
func (a *App) folderProject(ctx context.Context, st *state, src store.Source) (ids.ID[ids.Project], error) {
	if access, known := engine.AccessOf(ctx); known && access == op.AccessRead {
		pid, err := folderProjectIDOf(ctx, st, src)
		if err != nil || !pid.IsZero() {
			return pid, err
		}
	}
	ref, err := a.rosterProject(ctx, st, src, st.folders[src])
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	return ids.Parse[ids.Project](ref.ID().String())
}
