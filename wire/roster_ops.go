package wire

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The roster verbs (ops/infra roster.go) and the audit reads (ops/audit)
// as wire runs them.
//
// Flow:
//
//	App.Run ──▶ hostRunOp(name)? ──▶ runDirect: decode + invoke, no repo
//	                                   engine open (the op picks its own
//	                                   App / sink)
//	actor.register|deactivate ──▶ Options.Roster set? ──▶ the host's roster
//	                              (the old binary: org situation, v0.1.17
//	                               org stores, store-only App, autopush)
//	                           └▶ else the repo's own roster on this App
//	                              (no org, coordinator answer Q4)
//
// Why direct: the roster may live in an org store opened store-only
// outside any repo (FA-AGENT-9, BUG-T3), where the repo engine cannot
// open; the audit reads must not open anything that records (ops/audit
// package comment); actor.delete always refuses (BATCH2 #8).

var _ opsinfra.Roster = (*App)(nil)

// repoRosterWhere names the repo's own roster in messages (v0.1.17
// roster_cmd.go wording).
const repoRosterWhere = "this repo"

// rosterHandleMark prefixes a handle.
const rosterHandleMark = "@"

// hostRunOp reports whether wire runs the operation name directly.
func hostRunOp(name string) bool {
	return opsaudit.IsAuditOp(name) || opsinfra.IsRosterOp(name) || opsagent.IsDirectOp(name) || profilestore.IsDirect(name)
}

// runDirect decodes and invokes e with no engine around it. A list's
// --created-by / --updated-by are bound here as Engine.Run binds them
// (engine.BindListAuthors), because the roster lists read through an
// engine of their own that never sees the input.
func runDirect(ctx context.Context, e op.Entry, d op.Decoder) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return op.Result{}, err
	}
	if ctx, err = engine.BindListAuthors(ctx, reg, in); err != nil {
		return op.Result{}, err
	}
	// B2-4 / R-1: a store commit made by a direct operation (actor
	// register, …) is titled after it too, as engine.Run does.
	return e.Invoke(store.WithCommitOp(ctx, e.Spec().Name), in)
}

// ActorRegister implements opsinfra.Roster.
func (a *App) ActorRegister(ctx context.Context, in opsinfra.ActorRegisterIn) (opsinfra.ActorRegisterOut, error) {
	if a.opts.Roster != nil {
		return a.opts.Roster.ActorRegister(ctx, in)
	}
	r, err := a.repoRoster(ctx)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	x, done, err := a.rosterApp(ctx, r)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	defer done()
	who, err := x.rosterCaller(ctx, r)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: who.StableKey, Name: who.DisplayName, Handle: defaultRosterHandle(who.Handle, who.StableKey)}
	if in.Kind == opsinfra.RegisterKindAgent {
		owner, oerr := x.rosterOwner(ctx, r, in.Owner, who.StableKey)
		if oerr != nil {
			return opsinfra.ActorRegisterOut{}, oerr
		}
		sp = RosterActor{Kind: schema.ActorKindAgent, StableKey: agentStableKeyPrefix + in.Label, Name: in.Label, Label: in.Label, OwnerID: owner.ID}
	}
	return x.registerSpec(ctx, r, sp, in.Kind)
}

// registerSpec is ActorRegister's write once the actor spec is known (x
// is the roster App): the identity-contact match (people only), the
// handle check, the row, the push. setup join reuses it (registerCaller).
func (x *App) registerSpec(ctx context.Context, r rosterAt, sp RosterActor, kind string) (opsinfra.ActorRegisterOut, error) {
	if kind != opsinfra.RegisterKindAgent {
		// TEAM_PROFILES 6.3 (TP-4): a key that is an identity contact of an
		// existing actor is that person already; nothing is written, so a
		// second email never becomes a second actor.
		if out, ok, cerr := x.rosterContactMatch(ctx, r, sp.StableKey); cerr != nil || ok {
			return out, cerr
		}
	}
	if sp.Handle != "" {
		if herr := x.rosterHandleFree(ctx, r, sp.Handle, sp.StableKey); herr != nil {
			return opsinfra.ActorRegisterOut{}, herr
		}
	}
	if r.src == store.SourceRepo {
		// Q12, BUG-T1 (v0.1.17 liveCmdState): a no-org register puts its
		// actors row on the live ref, so the next command of every other
		// clone resolves the handle (`task add --assign @h`, Recipe02).
		ctx = store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: schema.TableActors})
	}
	id, created, err := x.RosterRegister(ctx, r.src, sp)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	x.rosterPush(ctx, r)
	return opsinfra.ActorRegisterOut{ID: id, Kind: kind, Name: sp.Name, Handle: sp.Handle, Label: sp.Label, Created: created, Where: r.shown()}, nil
}

// registerCaller registers the caller (a person, or the person an agent
// acts for: rosterCaller) on r's roster, as
// `lore actor register` would, with x the roster App.
func (x *App) registerCaller(ctx context.Context, r rosterAt) (opsinfra.ActorRegisterOut, error) {
	who, err := x.rosterCaller(ctx, r)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: who.StableKey, Name: who.DisplayName, Handle: defaultRosterHandle(who.Handle, who.StableKey)}
	return x.registerSpec(ctx, r, sp, opsinfra.RegisterKindHuman)
}

// ActorDeactivate implements opsinfra.Roster.
func (a *App) ActorDeactivate(ctx context.Context, handle string) (opsinfra.ActorDeactivateOut, error) {
	if a.opts.Roster != nil {
		return a.opts.Roster.ActorDeactivate(ctx, handle)
	}
	r, err := a.repoRoster(ctx)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	x, done, err := a.rosterApp(ctx, r)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	defer done()
	if _, err = x.rosterCaller(ctx, r); err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	act, err := x.rosterByHandle(ctx, r, handle)
	if err != nil {
		if gone := x.rosterInactive(ctx, r, handle); gone != nil && errs.Has(err, errs.InvalidInput) {
			return opsinfra.ActorDeactivateOut{}, gone
		}
		return opsinfra.ActorDeactivateOut{}, err
	}
	if r.src == store.SourceRepo {
		// Same as register (Q12): the no-org roster change rides the live
		// ref so other clones see the actor gone on their next command.
		ctx = store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: schema.TableActors})
	}
	if err := x.RosterDeactivate(ctx, r.src, act.ID); err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	x.rosterPush(ctx, r)
	return opsinfra.ActorDeactivateOut{ID: act.ID, Name: act.Name}, nil
}

// rosterInactive is the refusal of deactivating @handle when the roster
// holds them but not as active (offboarded: left or archived, or a
// v0.1.17 deactivate): E_STATUS_CHANGE naming their status and the way
// back, the same answer as a second `lore actor offboard`. nil when no
// inactive actor has the handle (the caller's "no one is @h" stands).
// A read error is nil too: the lookup's own error is the one to show.
func (a *App) rosterInactive(ctx context.Context, r rosterAt, handle string) error {
	h := strings.TrimPrefix(handle, rosterHandleMark)
	actors, err := a.RosterActors(ctx, r.src)
	if err != nil {
		return nil
	}
	for _, x := range actors {
		if x.Active || x.Handle != h {
			continue
		}
		if x.Status == "" {
			// v0.1.17 deactivate (active=false only): they come back by
			// registering again (rosterNotOwner).
			return errs.New(errs.StatusChange, rosterShown(x)+" is already deactivated; nothing to deactivate").
				WithHint("they run `lore actor register` in a repo of the org to come back")
		}
		e := errs.New(errs.StatusChange, rosterShown(x)+" is already "+x.Status+"; nothing to deactivate")
		if x.Kind == schema.ActorKindHuman {
			return e.WithHint("to bring them back: lore actor rejoin --actor " + rosterRef(x) + " --type employee|contractor|intern|client_contact")
		}
		return e
	}
	return nil
}

// rosterAt is the roster a verb works on: the repo's own (no org, Q4) or
// the org folder of the resolved org's store ("<org>/<folder>", CFG1,
// P3-CMDLORE-5).
type rosterAt struct {
	src store.Source
	// where names it in messages: "this repo", "northwind org/". An org
	// folder is no secret: it is printed as it is, in a pipe and in CI too.
	where string
	// org is the org's name for an org roster ("" for the repo's own): a
	// person who left is named as having left the org (rosterLeftErr).
	org string
}

// newRosterAt is the roster at src named where.
func newRosterAt(src store.Source, where string) rosterAt {
	return rosterAt{src: src, where: where}
}

// shown is where for output, success lines and refusals alike.
func (r rosterAt) shown() string { return r.where }

// handleTakenErr refuses a handle another actor or team holds on r.
func (r rosterAt) handleTakenErr(handle, holder string) error {
	return errs.New(errs.InvalidInput, fmt.Sprintf("handle @%s is taken by %s on the roster in %s", handle, holder, r.shown())).
		WithHint("choose another handle (`lore identity set --org <org> --handle <h>`); handles are unique per roster")
}

// handleMissErr refuses a handle no active actor of r has.
func (r rosterAt) handleMissErr(handle string) error {
	return errs.New(errs.InvalidInput, "no one is @"+handle+" on the roster in "+r.shown()).WithHint(r.missHint())
}

// goneErr refuses agents to a caller who left r (status left|archived).
func (r rosterAt) goneErr(status, ref string) error {
	return errs.New(errs.StatusChange, "you are "+status+" on the roster in "+r.shown()+", so you cannot start agents there").
		WithHint("ask an admin to run `lore actor rejoin --actor " + ref + " --type …`")
}

// unregisteredErr refuses agents to a caller with no row on r.
func (r rosterAt) unregisteredErr() error {
	return errs.New(errs.InvalidInput, "you are not on the roster in "+r.shown()+" yet").WithHint("run `lore actor register` first")
}

// deletedRowErr is a roster row of table found only as a tombstone.
func (r rosterAt) deletedRowErr(table row.Table, id ids.AnyID) error {
	return errs.New(errs.NotFound, "no "+string(table)+" row "+id.String()+" on the roster in "+r.shown()+" (deleted)")
}

// actorMissErr is an actor reference no row of r matches.
func (r rosterAt) actorMissErr(ref string) error {
	return errs.New(errs.NotFound, "no actor "+ref+" on the roster in "+r.shown()).WithHint(r.missHint())
}

// missHint is the hint for a handle missing from the roster.
func (r rosterAt) missHint() string {
	if r.src == store.SourceRepo {
		return "register first: `lore actor register` (as that person)"
	}
	return "they run `lore actor register` in a repo of the org, then `lore sync` here"
}

// rosterLookups bounds a handle lookup: the store copy, then once more
// after syncing the roster's store.
const rosterLookups = 2

// rosterPush syncs an org roster's store at once (v0.1.17 autopush); a
// failure leaves the change committed in the local copy for `lore sync`.
func (a *App) rosterPush(ctx context.Context, r rosterAt) {
	if r.src == store.SourceRepo {
		return
	}
	_, _ = a.SyncStores(ctx, string(r.src)) // best effort: committed locally, `lore sync` pushes
}

// rosterApp is the App the roster verbs write r with: a itself inside a
// git work tree or for the repo's own roster, else a store-only App on r's
// org folder store (BUG-T3, FA-AGENT-9: `lore actor register` outside any
// repo on a binary without a roster host, cmd/lore), closed by done. The
// old binary reaches the same App through its roster host (rosterCoreApp).
func (a *App) rosterApp(ctx context.Context, r rosterAt) (*App, func(), error) {
	noop := func() {}
	if r.src == store.SourceRepo {
		return a, noop, nil
	}
	alone, ok, err := StoreOnlyRoster(ctx, a.opts, r.src)
	if err != nil {
		return nil, noop, err
	}
	if !ok {
		return a, noop, nil
	}
	return alone, func() { _ = alone.Close() }, nil
}

// repoRoster picks the roster (the caller is rosterCaller's). With an org,
// the roster is its org folder store when OrgStores attaches one; an org
// without one is refused rather than written to the wrong place.
func (a *App) repoRoster(ctx context.Context) (rosterAt, error) {
	r := newRosterAt(store.SourceRepo, repoRosterWhere)
	if a.opts.Org != nil {
		org, err := a.opts.Org(ctx)
		if err != nil {
			return r, err
		}
		if org != nil {
			src, folder, ok, serr := a.orgRosterSource(ctx, org.Name)
			if serr != nil {
				return r, serr
			}
			if !ok {
				return r, errs.New(errs.NotImplemented, "org "+org.Name+"'s roster is kept in its org store, which this lore build does not reach for the roster").
					WithHint("set org_folder on one of the org's stores in ~/.lore/config.yaml")
			}
			r = newRosterAt(src, org.Name+" "+folder+"/")
		}
	}
	return r, nil
}

// rosterCaller is the person a roster change is made as (x is the roster
// App, rosterApp; r the roster). Owner rule 2026-11-11: a coding agent
// acts for its person and may do anything the person tells it, so its
// changes are made AS that person, and the write itself still records the
// agent ("<person> via <tool>", like every other write of it):
//
//	a person                         ─▶ themselves
//	a detected coding agent          ─▶ the person it was detected for (Resolved.Owner)
//	an agent actor (LORE_ACTOR)      ─▶ the owner its roster row names
//	an agent with no person behind   ─▶ E_INVALID_INPUT naming the fix
//
// An agent's change then passes agents.may_manage_people (default true; a
// team that sets it false opts into the refusal, profileWriter). No
// identity is refused.
func (a *App) rosterCaller(ctx context.Context, r rosterAt) (whoIs, error) {
	who, err := a.Who(ctx)
	if err != nil {
		return whoIs{}, err
	}
	if who.StableKey == "" {
		return whoIs{}, errs.New(errs.InvalidInput, "no identity: lore cannot tell who you are").WithHint("run `lore whoami` to see the identity chain")
	}
	person := whoIs{StableKey: who.StableKey, DisplayName: who.DisplayName, Handle: who.Handle}
	if who.Kind == schema.ActorKindAgent {
		if person, err = a.personBehind(ctx, r, who, "change the roster"); err != nil {
			return whoIs{}, err
		}
	}
	if a.agentCaller(ctx, who) {
		st, serr := a.rosterState(ctx, r.src, false)
		if serr != nil {
			return whoIs{}, serr
		}
		if serr = a.profileWriter(ctx, st, r.src); serr != nil {
			return whoIs{}, serr
		}
	}
	return person, nil
}

// agentCaller reports a caller lore sees as an agent: an agent identity
// (detected or LORE_ACTOR=agent:…), or a surface that fixed an agent on the
// call (REST X-Lore-Agent, profileContext).
func (a *App) agentCaller(ctx context.Context, who identity.Resolved) bool {
	if who.Kind == schema.ActorKindAgent || who.Agent != "" {
		return true
	}
	c, ok := ctx.Value(profileCallerKey{}).(op.Caller)
	return ok && c.Agent != ""
}

// personBehind is the person agent who acts for on r: the person a coding
// agent was detected for, else the owner the agent actor's roster row
// names. An agent with neither has no person behind it and is refused for
// what (the change it tried), with the two ways to give it one.
func (a *App) personBehind(ctx context.Context, r rosterAt, who identity.Resolved, what string) (whoIs, error) {
	if o := who.Owner; o != nil && o.StableKey != "" {
		id, err := o.ActorID()
		if err != nil {
			return whoIs{}, err
		}
		return whoIs{StableKey: o.StableKey, DisplayName: o.DisplayName, Handle: o.Handle, ID: id}, nil
	}
	ag, ok, err := a.rosterFind(ctx, r, func(x RosterActor) bool { return x.StableKey == who.StableKey })
	if err != nil {
		return whoIs{}, err
	}
	if ok && ag.OwnerID != "" {
		own, found, ferr := a.rosterFind(ctx, r, func(x RosterActor) bool { return x.ID == ag.OwnerID && x.Kind == schema.ActorKindHuman })
		if ferr != nil {
			return whoIs{}, ferr
		}
		if found {
			id, perr := ids.Parse[ids.Actor](own.ID)
			if perr != nil {
				return whoIs{}, errs.Wrap(errs.Internal, "roster: owner id of "+who.StableKey, perr)
			}
			return whoIs{StableKey: own.StableKey, DisplayName: own.Name, Handle: own.Handle, ID: id}, nil
		}
	}
	e := errs.New(errs.InvalidInput, "the agent "+who.StableKey+" has no person behind it on "+r.shown()+", so it cannot "+what).
		WithHint("its person runs it in their own terminal, or registers the agent with an owner: `lore agent start --label <l> --owner @<person>`")
	e.NeedsPerson = true
	return whoIs{}, e
}

// rosterPerson is the actor id of the person st's caller is or acts for on
// r (profilestore.Roster.Caller): the caller's own actor for a person, the
// person behind a coding agent (personBehind). An agent with no person
// behind it keeps its own id; its writes are refused by rosterCaller.
func (a *App) rosterPerson(ctx context.Context, st *state, r rosterAt) ids.ID[ids.Actor] {
	if st.who.Kind != schema.ActorKindAgent {
		return st.actor
	}
	p, err := a.personBehind(ctx, r, st.who, "act on the roster")
	if err != nil || p.ID.IsZero() {
		return st.actor
	}
	return p.ID
}

// orgRosterSource is the "<org>/<org folder>" store OrgStores attaches for
// org (ok false when none).
func (a *App) orgRosterSource(ctx context.Context, org string) (store.Source, string, bool, error) {
	if a.opts.OrgStores == nil {
		return "", "", false, nil
	}
	list, err := a.opts.OrgStores(ctx)
	if err != nil {
		return "", "", false, err
	}
	prefix := org + orgconf.RefSep
	for _, st := range list {
		if folder, ok := strings.CutPrefix(string(st.Name), prefix); ok && folder != "" {
			return st.Name, folder, true, nil
		}
	}
	return "", "", false, nil
}

// whoIs is the part of the identity a registration uses. ID is the
// person's actor id when an agent acts for them (personBehind); zero for a
// person, whose id is the App's own actor.
type whoIs struct {
	StableKey, DisplayName, Handle string
	ID                             ids.ID[ids.Actor]
}

// agentStableKeyPrefix starts an agent actor's stable key (F18).
const agentStableKeyPrefix = "agent:"

// humanStableKeyPrefix starts a person's stable key (human:<email>).
const humanStableKeyPrefix = "human:"

// defaultRosterHandle is the handle when the identity names none: the local
// part of the email of a human stable key (v0.1.17 defaultHandle, cookbook 2).
func defaultRosterHandle(handle, stableKey string) string {
	if handle != "" {
		return handle
	}
	email, ok := strings.CutPrefix(stableKey, humanStableKeyPrefix)
	if !ok {
		return ""
	}
	if local, _, found := strings.Cut(email, "@"); found {
		return local
	}
	return ""
}

// rosterHandleFree refuses a handle another active actor or team holds.
func (a *App) rosterHandleFree(ctx context.Context, r rosterAt, handle, stableKey string) error {
	actors, err := a.RosterActors(ctx, r.src)
	if err != nil {
		return err
	}
	holder := ""
	for _, x := range actors {
		if x.Active && x.Handle == handle && x.StableKey != stableKey {
			holder = "actor " + x.Name
		}
	}
	if holder == "" {
		teams, terr := a.RosterTeams(ctx, r.src)
		if terr != nil {
			return terr
		}
		for _, t := range teams {
			if t.Active && t.Handle == handle {
				holder = "team " + t.Handle
			}
		}
	}
	if holder == "" {
		return nil
	}
	return r.handleTakenErr(handle, holder)
}

// rosterByHandle finds the active actor @handle on the repo's roster.
func (a *App) rosterByHandle(ctx context.Context, r rosterAt, handle string) (RosterActor, error) {
	h := strings.TrimPrefix(handle, rosterHandleMark)
	x, ok, err := a.rosterFind(ctx, r, func(x RosterActor) bool { return x.Handle == h })
	if err != nil || ok {
		return x, err
	}
	return RosterActor{}, r.handleMissErr(h)
}

// rosterFind is the active roster actor match picks. A miss on an org
// roster re-reads it once after syncing its store (v0.1.17 refreshOnMiss
// and stores.RosterCheck: a teammate, or the caller on another machine,
// may have just registered, BUG-T6): a hit costs no fetch, a repo roster
// is never synced, and a sync that fails leaves the local copy's miss
// standing. Every roster lookup that refuses on a miss goes through it
// (rosterByHandle, rosterOwner, requireRosterAgent).
func (a *App) rosterFind(ctx context.Context, r rosterAt, match func(RosterActor) bool) (RosterActor, bool, error) {
	for attempt := 0; attempt < rosterLookups; attempt++ {
		if attempt > 0 {
			if r.src == store.SourceRepo {
				break
			}
			if _, serr := a.Sync(ctx, r.src); serr != nil {
				break
			}
		}
		actors, err := a.RosterActors(ctx, r.src)
		if err != nil {
			return RosterActor{}, false, err
		}
		for _, x := range actors {
			if x.Active && match(x) {
				return x, true, nil
			}
		}
	}
	return RosterActor{}, false, nil
}

// rosterNotOwner is the refusal when the caller has no active row on r:
// an offboarded person (explicit left or archived, INTEG-ORG-1) is told
// their status and that only `lore actor rejoin` brings them back; anyone
// else (never registered, or a v0.1.17 deactivate) is told to register.
func (a *App) rosterNotOwner(ctx context.Context, r rosterAt, callerKey string) error {
	actors, err := a.RosterActors(ctx, r.src)
	if err != nil {
		return err
	}
	for _, x := range actors {
		if x.StableKey == callerKey && x.Kind == schema.ActorKindHuman && rosterGone(x.Status) {
			return r.goneErr(x.Status, rosterRef(x))
		}
	}
	return r.unregisteredErr()
}

// rosterOwner is an agent's owner: @handle, or the caller's own actor.
func (a *App) rosterOwner(ctx context.Context, r rosterAt, handle, callerKey string) (RosterActor, error) {
	var owner RosterActor
	if handle != "" {
		o, err := a.rosterByHandle(ctx, r, handle)
		if err != nil {
			return RosterActor{}, err
		}
		owner = o
	} else {
		o, found, err := a.rosterFind(ctx, r, func(x RosterActor) bool { return x.StableKey == callerKey })
		if err != nil {
			return RosterActor{}, err
		}
		owner = o
		if !found {
			return RosterActor{}, a.rosterNotOwner(ctx, r, callerKey)
		}
	}
	if owner.Kind != schema.ActorKindHuman {
		return RosterActor{}, errs.New(errs.InvalidInput, "an agent's owner must be a human on the roster")
	}
	return owner, nil
}
