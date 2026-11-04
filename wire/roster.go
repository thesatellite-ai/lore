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
	mb, ok := st.byName[target].(markerBackend)
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

// rosterProject is the ref to target's project row (see RosterTeamPut).
func (a *App) rosterProject(ctx context.Context, st *state, target store.Source, folder string) (value.Ref, error) {
	rows, err := st.eng.List(ctx, target, schema.TableProjects)
	if err != nil {
		return value.Ref{}, err
	}
	var live []row.Row
	for _, r := range rows {
		if !r.Deleted {
			live = append(live, r)
		}
	}
	if len(live) > 0 {
		slices.SortFunc(live, func(x, y row.Row) int { return strings.Compare(x.ID.String(), y.ID.String()) })
		return value.RefOf(live[0].ID)
	}
	n, err := value.TextOf(folder)
	if err != nil {
		return value.Ref{}, errs.Wrap(errs.InvalidInput, "roster: the folder name is not valid text", err)
	}
	w, err := st.eng.Create(ctx, target, schema.TableProjects, map[string]value.Value{rosterFieldName: n})
	a.wrote(err)
	if err != nil {
		return value.Ref{}, err
	}
	return value.RefOf(w.Row.ID)
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
	if l, ok := r.Fields[schema.FieldTeamMembers].(value.List); ok {
		for _, it := range l.Items() {
			t.Members = append(t.Members, it.String())
		}
	}
	return t
}
