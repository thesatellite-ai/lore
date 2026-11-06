package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The org roster on the core (LORE_TEAM_SPEC F18, CFG1): actors and teams
// are rows of the org store's org/ folder, written through the engine like
// every row (scanned, stamped, audited). The caller names the store folder
// (the old binary passes "<org>/<org folder>", which Options.OrgStores
// attached); no client repo is ever a roster target, because the org
// folder is internal (refused below when the engine marks it client).
//
// Ids: an actor's id is the deterministic id of its stable key
// (identity.ActorID), so the same person registering on two machines
// writes one file; a team's id is the engine's natural id of its key
// (project_id, handle) in that store.

// Roster field names (schema.Builtin actors and teams).
const (
	rosterFieldLabel   = "label"
	rosterFieldOwner   = "owner_actor_id"
	rosterFieldActive  = "active"
	rosterFieldLeftAt  = "left_at"
	rosterFieldProject = "project_id"
	rosterFieldName    = "name"
)

// RosterActor is one actor on a roster, as read or to be registered.
type RosterActor struct {
	ID        string
	Kind      string // schema.ActorKinds: human, agent, …
	StableKey string
	Name      string
	Handle    string
	Label     string
	OwnerID   string
	Active    bool
}

// RosterTeam is one team on a roster.
type RosterTeam struct {
	ID      string
	Handle  string
	Name    string
	Members []string // actor ids
	Active  bool
}

// rosterState opens the App for a roster write or read in target and
// refuses a store the engine treats as a client repo (a roster names
// people; F18 keeps emails out of customer repositories).
func (a *App) rosterState(ctx context.Context, target store.Source, write bool) (*state, error) {
	var st *state
	var err error
	if write {
		st, err = a.writeState(ctx)
	} else {
		st, err = a.state(ctx)
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains(st.eng.Stores(), target) {
		return nil, errs.New(errs.NotFound, "roster: no store named "+string(target)+" is attached").
			WithHint("the org's store is attached from ~/.lore/config.yaml in a repo of that org")
	}
	if st.eng.IsClient(target) {
		return nil, errs.New(errs.LeakBlocked, "roster: "+string(target)+" is a client repo; the roster lives in the org's store")
	}
	return st, nil
}

// StoreFormat is the lore-store.yaml version of an attached store (1 =
// v0.1.17, rowfs.MarkerVersion = 0.1.18); 0 when the backend has no marker
// (the repo store). The old binary keeps a v0.1.17 org store on its own
// roster code until `lore migrate run`, so teammates on v0.1.17 never see
// `_v: 2` rows there.
func (a *App) StoreFormat(ctx context.Context, target store.Source) (int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return 0, err
	}
	mb, ok := concrete(st.byName[target]).(markerBackend)
	if !ok {
		return 0, nil
	}
	return mb.Marker().Version, nil
}

// RosterActors lists the actors of target's roster (tombstones skipped).
func (a *App) RosterActors(ctx context.Context, target store.Source) ([]RosterActor, error) {
	st, err := a.rosterState(ctx, target, false)
	if err != nil {
		return nil, err
	}
	rows, err := st.eng.List(ctx, target, schema.TableActors)
	if err != nil {
		return nil, err
	}
	out := make([]RosterActor, 0, len(rows))
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, actorOfRow(r))
		}
	}
	return out, nil
}

// RosterTeams lists the teams of target's roster (tombstones skipped),
// sorted by handle.
func (a *App) RosterTeams(ctx context.Context, target store.Source) ([]RosterTeam, error) {
	st, err := a.rosterState(ctx, target, false)
	if err != nil {
		return nil, err
	}
	rows, err := st.eng.List(ctx, target, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	out := make([]RosterTeam, 0, len(rows))
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, teamOfRow(r))
		}
	}
	slices.SortFunc(out, func(x, y RosterTeam) int { return strings.Compare(x.Handle, y.Handle) })
	return out, nil
}

// RosterRegister creates or refreshes sp in target (idempotent: the id is
// identity.ActorID(sp.StableKey)); created reports a new row. The caller
// checks handle uniqueness first (RosterActors, RosterTeams).
func (a *App) RosterRegister(ctx context.Context, target store.Source, sp RosterActor) (id string, created bool, err error) {
	st, err := a.rosterState(ctx, target, true)
	if err != nil {
		return "", false, err
	}
	aid, err := identity.ActorID(sp.StableKey)
	if err != nil {
		return "", false, err
	}
	fields, err := textFields(map[string]string{actorFieldKind: sp.Kind, actorFieldDisplay: sp.Name, actorFieldKey: sp.StableKey,
		actorFieldHandle: sp.Handle, rosterFieldLabel: sp.Label})
	if err != nil {
		return "", false, err
	}
	if sp.OwnerID != "" {
		owner, perr := ids.ParseAny(sp.OwnerID)
		if perr != nil {
			return "", false, errs.Wrap(errs.InvalidInput, "roster: the owner id is not valid", perr)
		}
		ref, rerr := value.RefOf(owner)
		if rerr != nil {
			return "", false, rerr
		}
		fields[rosterFieldOwner] = ref
	}
	fields[rosterFieldActive] = value.BoolOf(true)
	if target != store.SourceRepo {
		// The org folder's first roster write also gives the folder its
		// project row (v0.1.17 ensureProjectRow): the row every clone
		// agrees on, named by _meta.json (see rosterProject).
		if _, perr := a.rosterProject(ctx, st, target, rosterFolderName(target)); perr != nil {
			return "", false, perr
		}
	}
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: aid.Any()}
	_, cur, gerr := st.eng.Get(ctx, ref)
	switch {
	case gerr == nil && !cur.Deleted:
		fields[rosterFieldLeftAt] = value.Null{}
		_, err = st.eng.Update(ctx, ref, fields)
		a.wrote(err)
		return aid.String(), false, err
	case gerr != nil && !errs.Has(gerr, errs.NotFound):
		return "", false, gerr
	}
	_, err = st.eng.CreateWithID(ctx, target, schema.TableActors, aid.Any(), fields)
	a.wrote(err)
	if err != nil {
		return "", false, err
	}
	st.actorMu.Lock()
	if aid == st.actor {
		st.actorDone[target] = true // the caller's own row: ensureActor must not write it again
	}
	st.actorMu.Unlock()
	return aid.String(), true, nil
}

// RosterDeactivate marks actor id in target gone (active false, left_at
// now); actors are never deleted, so old rows still resolve.
func (a *App) RosterDeactivate(ctx context.Context, target store.Source, id string) error {
	st, err := a.rosterState(ctx, target, true)
	if err != nil {
		return err
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "roster: the actor id is not valid", err)
	}
	_, err = st.eng.Update(ctx, engine.Ref{Store: target, Table: schema.TableActors, ID: aid},
		map[string]value.Value{rosterFieldActive: value.BoolOf(false), rosterFieldLeftAt: value.DateTimeFrom(a.opts.Wall())})
	a.wrote(err)
	return err
}

// RosterTeamPut creates or updates the team handle in target with members
// (actor ids) and, when name is not empty, its display name; created
// reports a new row. A team belongs to the folder's project row (teams
// project_id is required): the folder's first live projects row, created
// (named after folder) when it has none.
func (a *App) RosterTeamPut(ctx context.Context, target store.Source, folder, handle, name string, members []string) (id string, created bool, err error) {
	st, err := a.rosterState(ctx, target, true)
	if err != nil {
		return "", false, err
	}
	list := make([]value.Value, 0, len(members))
	for _, m := range members {
		mid, perr := ids.ParseAny(m)
		if perr != nil {
			return "", false, errs.Wrap(errs.InvalidInput, "roster: a member id is not valid", perr)
		}
		ref, rerr := value.RefOf(mid)
		if rerr != nil {
			return "", false, rerr
		}
		list = append(list, ref)
	}
	memberList, err := value.ListOf(value.KindRef, list...)
	if err != nil {
		return "", false, err
	}
	fields := map[string]value.Value{schema.FieldTeamMembers: memberList, rosterFieldActive: value.BoolOf(true)}
	if name != "" {
		t, terr := value.TextOf(name)
		if terr != nil {
			return "", false, errs.Wrap(errs.InvalidInput, "roster: the team name is not valid text", terr)
		}
		fields[actorFieldDisplay] = t
	}
	rows, err := st.eng.List(ctx, target, schema.TableTeams)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if !r.Deleted && fieldText(r, actorFieldHandle) == handle {
			fields[rosterFieldLeftAt] = value.Null{}
			_, err = st.eng.Update(ctx, engine.Ref{Store: target, Table: schema.TableTeams, ID: r.ID}, fields)
			a.wrote(err)
			return r.ID.String(), false, err
		}
	}
	project, err := a.rosterProject(ctx, st, target, folder)
	if err != nil {
		return "", false, err
	}
	h, err := value.TextOf(handle)
	if err != nil {
		return "", false, errs.Wrap(errs.InvalidInput, "roster: the team handle is not valid text", err)
	}
	fields[actorFieldHandle] = h
	fields[rosterFieldProject] = project
	w, err := st.eng.Create(ctx, target, schema.TableTeams, fields)
	a.wrote(err)
	if err != nil {
		return "", false, err
	}
	return w.Row.ID.String(), true, nil
}

// rosterProject is the ref to target's project row (see RosterTeamPut),
// creating it when the folder has none.
//
// The row's id is the folder's _meta.json project_id (rowfs
// Folder.ProjectID: the shared copy's, else the natural id of store and
// folder, recorded by the folder's first write). So two clones that create
// the same folder seconds apart, neither seeing the other's push, write the
// same project row file, which merges, instead of two rows under random ids
// (one per racing creator) that made every later @handle command
// E_AMBIGUOUS_PROJECT. A folder an older lore left with extra project rows
// resolves to the one _meta.json names. A backend with no folder identity
// (the store root, a test backend) keeps the first live row, else a new
// one under the engine's id.
func (a *App) rosterProject(ctx context.Context, st *state, target store.Source, folder string) (value.Ref, error) {
	pid, err := folderProjectIDOf(ctx, st, target)
	if err != nil {
		return value.Ref{}, err
	}
	rows, err := st.eng.List(ctx, target, schema.TableProjects)
	if err != nil {
		return value.Ref{}, err
	}
	var live []row.Row
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		if !pid.IsZero() && r.ID == pid.Any() {
			return value.RefOf(r.ID)
		}
		live = append(live, r)
	}
	if pid.IsZero() && len(live) > 0 {
		slices.SortFunc(live, func(x, y row.Row) int { return strings.Compare(x.ID.String(), y.ID.String()) })
		return value.RefOf(live[0].ID)
	}
	n, err := value.TextOf(folder)
	if err != nil {
		return value.Ref{}, errs.Wrap(errs.InvalidInput, "roster: the folder name is not valid text", err)
	}
	fields := map[string]value.Value{rosterFieldName: n}
	var w engine.Written
	if pid.IsZero() {
		w, err = st.eng.Create(ctx, target, schema.TableProjects, fields)
	} else {
		w, err = st.eng.CreateWithID(ctx, target, schema.TableProjects, pid.Any(), fields)
	}
	a.wrote(err)
	if err != nil {
		return value.Ref{}, err
	}
	return value.RefOf(w.Row.ID)
}

// folderProjectIDOf is target's folder project id (_meta.json project_id,
// or the id its first write records); zero when the backend has no folder
// identity.
func folderProjectIDOf(ctx context.Context, st *state, target store.Source) (ids.ID[ids.Project], error) {
	fb, ok := concrete(st.byName[target]).(folderIDOf)
	if !ok {
		return ids.ID[ids.Project]{}, nil
	}
	pid, err := fb.FolderProjectID(ctx)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	return pid, nil
}

// rosterFolderName is the folder part of a "<store>/<folder>" target, the
// name the folder's project row carries.
func rosterFolderName(target store.Source) string {
	if _, f, ok := strings.Cut(string(target), targetSep); ok {
		return f
	}
	return string(target)
}

// textFields turns the non-empty strings of vals into Text values.
func textFields(vals map[string]string) (map[string]value.Value, error) {
	out := map[string]value.Value{}
	for k, v := range vals {
		if v == "" {
			continue
		}
		t, err := value.TextOf(v)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "roster: "+k+" is not valid text", err)
		}
		out[k] = t
	}
	return out, nil
}

// fieldText is a field as text ("" when absent or null).
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// fieldActive reads active: absent counts as active (v0.1.17 rows that
// never set it), only an explicit false is gone.
func fieldActive(r row.Row) bool { return fieldText(r, rosterFieldActive) != "false" }

// actorOfRow reads one actors row.
func actorOfRow(r row.Row) RosterActor {
	return RosterActor{ID: r.ID.String(), Kind: fieldText(r, actorFieldKind), StableKey: fieldText(r, actorFieldKey),
		Name: fieldText(r, actorFieldDisplay), Handle: fieldText(r, actorFieldHandle), Label: fieldText(r, rosterFieldLabel),
		OwnerID: fieldText(r, rosterFieldOwner), Active: fieldActive(r)}
}

// teamOfRow reads one teams row.
func teamOfRow(r row.Row) RosterTeam {
	t := RosterTeam{ID: r.ID.String(), Handle: fieldText(r, actorFieldHandle), Name: fieldText(r, actorFieldDisplay), Active: fieldActive(r)}
	// any form: a List, or a JSON array as a row file may decode
	t.Members = value.ItemStrings(r.Fields[schema.FieldTeamMembers])
	return t
}

// StoreOnlyRoster returns an App for a roster command run outside any repo
// (BUG-T3, FA-AGENT-9): when opts.RepoDir is not inside a git work tree,
// the App opens only the org store folder target (openStoreOnly) and has
// no repo state, so every repo-only path (store operations, SeedRepoMeta,
// anything on configState) fails with E_NOT_A_STORE. Inside a work tree it
// returns nil, false: the caller keeps its normal App, whose full open
// attaches the org store beside the repo. Only the roster commands call
// this; every other command keeps requiring a repo (fail closed).
func StoreOnlyRoster(ctx context.Context, opts Options, target store.Source) (*App, bool, error) {
	if insideWorkTree(ctx, opts.RepoDir) {
		return nil, false, nil
	}
	a, err := New(opts)
	if err != nil {
		return nil, false, err
	}
	st, err := openStoreOnly(ctx, a.opts, target) // a.opts: New filled the defaults (Wall)
	if err != nil {
		return nil, false, err
	}
	st.eng.SetRedactHook(a.redactTask) // D21, as App.state does
	a.mu.Lock()
	a.opened, a.st = true, st
	a.cfgOpened = true
	a.cfgErr = errs.New(errs.NotAStore, "wire: this command needs a repo; only the roster commands run outside one").
		WithHint("run it inside a git repository")
	a.mu.Unlock()
	return a, true, nil
}

// insideWorkTree reports whether dir is inside a git work tree. A git that
// cannot run answers false: the store-only open then fails on its own git
// store, so nothing opens on a guess.
func insideWorkTree(ctx context.Context, dir string) bool {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--is-inside-work-tree"}}.output(ctx)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
