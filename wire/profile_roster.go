package wire

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/identmatch"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The team profile store (TEAM_PROFILES_DESIGN 2.0, 11 item 9): App
// implements profilestore.Store on the roster machinery the roster verbs
// use (roster_ops.go), so every profile row lands where `lore actor
// register` writes actors.
//
// Flow:
//
//	App.Run ──▶ profilestore.IsDirect ──▶ profileContext (yes, caller)
//	        ──▶ runDirect ──▶ op ──▶ App.Roster(ctx, write)
//	                                  repoRoster ──▶ no org + write: E_REFUSED (TS-1)
//	                                  rosterApp  ──▶ store-only App outside a repo (BUG-T3)
//	                                  rosterState ──▶ client store: E_LEAK_BLOCKED
//	                                  write: profileWriter (identity; agent needs
//	                                         agents.may_manage_people, TS-4)
//	                              ──▶ profileRoster: List/Get/Create/Update/Delete
//	                                  on the roster App's engine, Push = rosterPush

var _ profilestore.Store = (*App)(nil)

// profileCallerKey carries the op.Caller of a direct profile op: runDirect
// runs without the engine, so engine.CallerOf is empty there.
type profileCallerKey struct{}

// profileContext is ctx for a direct profile op: the caller's --yes for
// op.Confirmed (offboard, rejoin, contact add --merge: TP-20) and the
// caller itself, so a REST client's X-Lore-Agent reaches the TS-4 check.
func profileContext(ctx context.Context, c op.Caller) context.Context {
	return context.WithValue(op.WithConfirmed(ctx, c.Yes), profileCallerKey{}, c)
}

// Roster implements profilestore.Store. done is never nil; on an error it
// is a no-op (anything opened is already closed).
func (a *App) Roster(ctx context.Context, write bool) (profilestore.Roster, func(), error) {
	noop := func() {}
	r, err := a.repoRoster(ctx)
	if err != nil {
		return nil, noop, err
	}
	if write && r.src == store.SourceRepo {
		// TS-1: a no-org roster is the repo's committed .lore/data; contacts
		// (TP-6: plain) would be published with the code.
		return nil, noop, errs.New(errs.Refused, "team profiles live in an org store; this repo has no org").
			WithHint("lore org create <name>")
	}
	x, done, err := a.rosterApp(ctx, r)
	if err != nil {
		return nil, noop, err
	}
	st, err := x.rosterState(ctx, r.src, write)
	if err == nil && write {
		err = x.profileWriter(ctx, st, r.src)
	}
	if err != nil {
		done()
		return nil, noop, err
	}
	return &profileRoster{app: x, st: st, at: r, person: x.rosterPerson(ctx, st, r)}, done, nil
}

// profileWriter refuses a profile write by no identity, and by an agent
// unless agents.may_manage_people is true for target (TS-4, TP-20; the
// roster verbs make an agent's change as its person and check this same
// flag, rosterCaller).
func (a *App) profileWriter(ctx context.Context, st *state, target store.Source) error {
	who, err := a.Who(ctx)
	if err != nil {
		return err
	}
	if who.StableKey == "" {
		return errs.New(errs.InvalidInput, "no identity: lore cannot tell who you are").WithHint("run `lore whoami` to see the identity chain")
	}
	agent := who.Agent
	if c, ok := ctx.Value(profileCallerKey{}).(op.Caller); ok && (c.AgentFixed || c.Agent != "") {
		agent = c.Agent // the surface decided (REST X-Lore-Agent, or an explicit agent)
	}
	if agent == "" && who.Kind != schema.ActorKindAgent {
		return nil
	}
	if agent == "" {
		agent = who.StableKey
	}
	set, err := st.eng.Policy(target)
	if err != nil {
		return err // fail closed: a policy that cannot be read allows nothing
	}
	if v, ok := set.Get(policy.KeyAgentsMayManagePeople); ok && v.Value != nil {
		if b, isBool := v.Value.(value.Bool); isBool && b.Bool() {
			return nil
		}
	}
	e := errs.New(errs.PolicyDenied, string(policy.KeyAgentsMayManagePeople)+" is false: an agent may not change team profiles; this looks like "+agent).
		WithHint("a person runs it in their own terminal, or sets `lore policy set " + string(policy.KeyAgentsMayManagePeople) + " true`")
	e.NeedsPerson = true
	return e
}

// profileRoster is the opened org roster for one profile op.
type profileRoster struct {
	app *App
	st  *state
	at  rosterAt
	// person is the caller's person (Caller): a coding agent acts for its
	// person (owner rule 2026-11-11, rosterPerson).
	person ids.ID[ids.Actor]
	// warns are the engine's warnings of every write so far (Warnings).
	warns []string
}

// keep records w's warnings (Warnings) and passes its row through.
func (p *profileRoster) keep(w engine.Written) row.Row {
	for _, x := range w.Warnings {
		p.warns = append(p.warns, profilestore.WarningLine(string(x.Code), x.Field, x.Message))
	}
	return w.Row
}

// Warnings implements profilestore.Roster.
func (p *profileRoster) Warnings() []string { return slices.Clone(p.warns) }

var _ profilestore.Roster = (*profileRoster)(nil)

// profileRoster lists its trash, so a rejoin preview names the memberships
// an offboard trashed before they began (opsperiod.TrashLister).
var _ opsperiod.TrashLister = (*profileRoster)(nil)

// Source implements profilestore.Roster.
func (p *profileRoster) Source() store.Source { return p.at.src }

// Where implements profilestore.Roster (hidden in quiet output, §6.5).
func (p *profileRoster) Where() string { return p.at.shown() }

// Caller implements profilestore.Roster.
func (p *profileRoster) Caller() ids.ID[ids.Actor] { return p.person }

// Today implements profilestore.Roster: the wall clock's day.
func (p *profileRoster) Today() time.Time { return teamprofile.Day(p.app.opts.Wall()) }

// ref is the engine ref of id in the roster store.
func (p *profileRoster) ref(table row.Table, id ids.AnyID) engine.Ref {
	return engine.Ref{Store: p.at.src, Table: table, ID: id}
}

// List implements profilestore.Roster: live rows only.
func (p *profileRoster) List(ctx context.Context, table row.Table) ([]row.Row, error) {
	rows, err := p.st.eng.List(ctx, p.at.src, table)
	if err != nil {
		return nil, err
	}
	out := rows[:0:0]
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListTrash implements period.TrashLister: the rows of table in the
// roster store's trash (engine.DeletedOnly), which List leaves out.
func (p *profileRoster) ListTrash(ctx context.Context, table row.Table) ([]row.Row, error) {
	return p.st.eng.List(engine.WithDeleted(ctx, engine.DeletedOnly, table), p.at.src, table)
}

// Get implements profilestore.Roster; a tombstone is E_NOT_FOUND.
func (p *profileRoster) Get(ctx context.Context, table row.Table, id ids.AnyID) (row.Row, error) {
	r, err := p.getOnce(ctx, table, id)
	if table != schema.TableInvites || !errs.Has(err, errs.NotFound) || p.at.src == store.SourceRepo {
		return r, err
	}
	// ONB 4.5: an invite is written and pushed on the inviter's machine
	// and read on the joiner's, whose clone of the org store may predate
	// it (a join retried within live.fetch_ttl of an earlier import). A
	// miss reads once more after syncing the store, as ResolveActor does
	// for a person (BUG-T6); a sync that fails leaves the first miss.
	if _, serr := p.app.Sync(ctx, p.at.src); serr != nil {
		return r, err
	}
	return p.getOnce(ctx, table, id)
}

// InTrash implements profilestore.Roster: the engine's own answer for the
// roster store (E_IN_TRASH naming who deleted it, or nil).
func (p *profileRoster) InTrash(ctx context.Context, table row.Table, id ids.AnyID) error {
	return p.st.eng.InTrash(ctx, p.ref(table, id))
}

// getOnce is Get on the roster as it is now (no sync).
func (p *profileRoster) getOnce(ctx context.Context, table row.Table, id ids.AnyID) (row.Row, error) {
	_, r, err := p.st.eng.Get(ctx, p.ref(table, id))
	if err != nil {
		return row.Row{}, err
	}
	if r.Deleted {
		return row.Row{}, p.at.deletedRowErr(table, id)
	}
	return r, nil
}

// Create implements profilestore.Roster.
func (p *profileRoster) Create(ctx context.Context, table row.Table, fields map[string]value.Value) (row.Row, error) {
	w, err := p.st.eng.Create(ctx, p.at.src, table, fields)
	p.app.wroteTo(p.at.src, err)
	return p.keep(w), err
}

// CreateWithID implements profilestore.Roster.
func (p *profileRoster) CreateWithID(ctx context.Context, table row.Table, id ids.AnyID, fields map[string]value.Value) (row.Row, error) {
	w, err := p.st.eng.CreateWithID(ctx, p.at.src, table, id, fields)
	p.app.wroteTo(p.at.src, err)
	return p.keep(w), err
}

// Update implements profilestore.Roster.
func (p *profileRoster) Update(ctx context.Context, table row.Table, id ids.AnyID, patch map[string]value.Value) (row.Row, error) {
	w, err := p.st.eng.Update(ctx, p.ref(table, id), patch)
	p.app.wroteTo(p.at.src, err)
	if _, named := patch[actorFieldDisplay]; err == nil && named && table == schema.TableActors && id == p.st.actor.Any() {
		// SOURCE_OF_TRUTH actors: the caller renamed themself on the
		// roster; orgs[].identity.name (whoami) follows now, the other
		// copies on the next write to each store (actor_source.go).
		p.app.followRosterName(ctx, p.st, actorsource.Source{Values: actorsource.Values{Name: fieldText(w.Row, actorFieldDisplay)}})
	}
	return p.keep(w), err
}

// Delete implements profilestore.Roster: into the trash with reason.
func (p *profileRoster) Delete(ctx context.Context, table row.Table, id ids.AnyID, reason string) error {
	w, err := p.st.eng.Delete(engine.WithDeleteReason(ctx, reason), p.ref(table, id))
	p.app.wroteTo(p.at.src, err)
	p.keep(w)
	return err
}

var _ profilestore.FolderProjecter = (*profileRoster)(nil)

// FolderProject implements profilestore.FolderProjecter: the org folder's
// project row (rosterProject, as RosterTeamPut uses), made when the folder
// has none yet.
func (p *profileRoster) FolderProject(ctx context.Context) (ids.ID[ids.Project], error) {
	ref, err := p.app.rosterProject(ctx, p.st, p.at.src, rosterFolderName(p.at.src))
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	return ids.Parse[ids.Project](ref.ID().String())
}

// Push implements profilestore.Roster.
func (p *profileRoster) Push(ctx context.Context) { p.app.rosterPush(ctx, p.at) }

// ResolveActor implements profilestore.Roster for the forms commit 0 knows
// (TEAM_PROFILES 3): an act_ id, @handle (or a bare handle), and a stable
// key (kind:rest, human:mira@acme.com). Left and archived people resolve
// (offboard and rejoin need them); between two people holding one handle
// (a left person's handle reused), the active one wins. The contact forms
// (email:…, github:…, <type>:<value>, a bare email) resolve through the
// identity contacts last (TEAM_PROFILES 6.3, actorByContact).
func (p *profileRoster) ResolveActor(ctx context.Context, ref string) (row.Row, error) {
	x, err := p.resolveOnce(ctx, ref)
	if !(errs.Has(err, errs.NotFound) || errs.Has(err, errs.InTrash)) || p.at.src == store.SourceRepo {
		return x, err
	}
	// A miss on the org roster reads it once more after syncing its store,
	// as rosterFind does for the roster verbs (BUG-T6): the person may
	// have registered on their own machine a moment ago. A sync that fails
	// leaves the first miss standing. A reference whose only holder is a
	// removed contact (E_IN_TRASH) syncs too: it may have been added back.
	if _, serr := p.app.Sync(ctx, p.at.src); serr != nil {
		return x, err
	}
	return p.resolveOnce(ctx, ref)
}

// resolveOnce is ResolveActor on the roster as it is now (no sync).
func (p *profileRoster) resolveOnce(ctx context.Context, ref string) (row.Row, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, string(ids.PrefixActor)+actorIDSep) {
		id, err := ids.ParseAny(ref)
		if err != nil {
			return row.Row{}, errs.Wrap(errs.InvalidID, "actor reference "+ref+" is not an actor id", err)
		}
		return p.Get(ctx, schema.TableActors, id)
	}
	actors, err := p.List(ctx, schema.TableActors)
	if err != nil {
		return row.Row{}, err
	}
	var hits []row.Row
	if key := ref; strings.Contains(key, stableKeySep) {
		for _, x := range actors {
			if fieldText(x, actorFieldKey) == key {
				hits = append(hits, x)
			}
		}
	} else if h := strings.TrimPrefix(ref, rosterHandleMark); h != "" && !strings.Contains(h, rosterHandleMark) {
		for _, x := range actors {
			if fieldText(x, actorFieldHandle) == h {
				hits = append(hits, x)
			}
		}
		if len(hits) > 1 {
			var active []row.Row
			for _, x := range hits {
				if fieldActive(x) {
					active = append(active, x)
				}
			}
			if len(active) == 1 {
				hits = active
			}
		}
	}
	if len(hits) == 0 {
		// TEAM_PROFILES 6.3: the contact forms (email:…, github:…,
		// <type>:<value>, a bare email) resolve through the identity
		// contacts of the roster store; a bare email that is someone's own
		// stable key (no contact row yet) resolves to them too.
		x, ok, err := p.st.actorByContact(ctx, []store.Source{p.at.src}, ref)
		if err != nil {
			return row.Row{}, err
		}
		if ok {
			return x, nil
		}
		if !strings.Contains(ref, stableKeySep) && strings.Contains(ref, rosterHandleMark) && !strings.HasPrefix(ref, rosterHandleMark) {
			key := humanStableKeyPrefix + strings.ToLower(ref)
			for _, x := range actors {
				if strings.ToLower(fieldText(x, actorFieldKey)) == key {
					hits = append(hits, x)
				}
			}
		}
	}
	switch len(hits) {
	case 0:
		if err := p.st.trashedContact(ctx, []store.Source{p.at.src}, ref); err != nil {
			return row.Row{}, err
		}
		return row.Row{}, p.at.actorMissErr(ref)
	case 1:
		return hits[0], nil
	}
	names := make([]string, 0, len(hits))
	for _, x := range hits {
		names = append(names, x.ID.String()+" ("+fieldText(x, actorFieldDisplay)+")")
	}
	return row.Row{}, errs.New(errs.AmbiguousID, "actor "+ref+" matches "+strings.Join(names, ", ")).WithHint("name one by its act_ id")
}

// actorIDSep joins an id's prefix and its hex tail (act_…).
const actorIDSep = "_"

// stableKeySep separates a stable key's kind from the rest (human:…).
const stableKeySep = ":"

// trashedContact is E_IN_TRASH (trash.InTrashErr: who removed it, `lore
// trash restore <id>`) when an actor reference in contact form (email:…,
// github:…, <type>:<value>, human:<email>, a bare email) names no live
// holder in srcs of st but an identity contact in the trash of one of them
// would have answered it (DELETION 3.2: a removed contact reads like every
// trashed row, as liveContact answers for its id). nil for any other ref:
// no contact form, a purged or never-written contact, or one only a client
// store holds (identity matching never reads those). Callers ask it after
// every live form missed, so a live holder always wins.
func (st *state) trashedContact(ctx context.Context, srcs []store.Source, ref string) error {
	trashedOnly := engine.WithDeleted(ctx, engine.DeletedOnly, schema.TableActorContacts)
	var contacts []identmatch.Contact
	from := map[ids.AnyID]store.Source{}
	for _, src := range srcs {
		if st.eng.IsClient(src) {
			continue
		}
		rows, err := st.eng.List(trashedOnly, src, schema.TableActorContacts)
		if err != nil {
			return errs.Prefix("wire: read the trashed "+string(schema.TableActorContacts)+" of store "+string(src), err)
		}
		for _, r := range rows {
			if c, ok := contactOfRow(r, false); ok {
				if _, seen := from[c.ID]; !seen {
					contacts, from[c.ID] = append(contacts, c), src
				}
			}
		}
	}
	if len(contacts) == 0 {
		return nil // nothing removed: no need to read the contact types
	}
	in, err := readIdentityInputs(ctx, st, srcs)
	if err != nil {
		return err
	}
	key, ok := identmatch.KeyOf(ref, in.types)
	if !ok {
		return nil
	}
	c, ok := identmatch.Held(key, in.types, contacts)
	if !ok {
		return nil
	}
	// The engine's own answer (and wording): nil for an id a purge left
	// behind, E_IN_TRASH naming who removed it otherwise.
	return st.eng.InTrash(ctx, engine.Ref{Store: from[c.ID], Table: schema.TableActorContacts, ID: c.ID})
}
