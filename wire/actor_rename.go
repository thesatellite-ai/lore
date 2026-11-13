package wire

// actor_rename.go — who may change an actor's source, and the solo
// rename (SOURCE_OF_TRUTH actors, owner 2026-11-09).
//
//	team  actor edit ──▶ Roster(write) ──▶ MayEditPerson: the person, or an
//	                     org admin (the org's creator: the roster folder's
//	                     project row created_by; or an admin setup of the
//	                     org on this machine) ──▶ roster row update
//	solo  actor edit --name ──▶ RenameIdentity: the caller's own actor only
//	                     ──▶ the identity setter (SetIdentity: the active
//	                         setup's identity, else identity:) ──▶ copies
//	                         follow on the next write to each store

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var (
	_ profilestore.PersonEditGate  = (*profileRoster)(nil)
	_ profilestore.IdentityRenamer = (*App)(nil)
)

// MayEditPerson implements profilestore.PersonEditGate: the caller may
// change id's roster row when id is the caller (or the person a detected
// agent acts for), or the caller is an admin of the org (orgAdmin). A
// repo's own roster (no org) is not gated: profile writes are refused
// there anyway (TS-1).
func (p *profileRoster) MayEditPerson(ctx context.Context, id ids.AnyID) error {
	if p.at.src == store.SourceRepo || id == p.st.actor.Any() {
		return nil
	}
	owner := p.callerOwner(ctx)
	if !owner.IsZero() && owner.Any() == id {
		return nil
	}
	admin, err := p.app.orgAdmin(ctx, p.st, p.at.src, owner)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	who := id.String()
	if r, gerr := p.getOnce(ctx, schema.TableActors, id); gerr == nil {
		who = rosterShown(actorOfRow(r))
	}
	return errs.New(errs.PolicyDenied, "only "+who+" or an admin of the org may change "+who+"'s roster row, and you ("+p.st.actor.String()+") are neither").
		WithHint("ask " + who + " to run it on their machine, or an org admin (the person who created the org, or one with an admin setup of it)")
}

// orgAdmin reports whether the caller is an admin of the org whose roster
// is rs: the creator of the roster folder's project row (written by `lore
// org create`, so it names the org's creator in the store itself), or
// the holder of an admin setup of that org on this machine (the admin
// concept team invites already use, ops/team adminSetup). An agent acts
// with its person's rights (the people writes reach here only when
// agents.may_manage_people lets agents make them), so the creator check
// accepts the person the agent acts for too.
func (a *App) orgAdmin(ctx context.Context, st *state, rs store.Source, owner ids.ID[ids.Actor]) (bool, error) {
	pid, err := folderProjectIDOf(ctx, st, rs)
	if err != nil {
		return false, err
	}
	if !pid.IsZero() {
		if _, r, gerr := st.eng.Get(ctx, engine.Ref{Store: rs, Table: schema.TableProjects, ID: pid.Any()}); gerr == nil && !r.Deleted && (r.CreatedBy == st.actor || (!owner.IsZero() && r.CreatedBy == owner)) {
			return true, nil
		}
	}
	org := ""
	if a.opts.Org != nil {
		if o, oerr := a.opts.Org(ctx); oerr == nil && o != nil {
			org = o.Name
		}
	}
	if org == "" {
		org, _, _ = strings.Cut(string(rs), targetSep)
	}
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return false, nil // no readable setups: no admin setup (the creator check stands)
	}
	for _, e := range cfg.Setups {
		if e.Kind == onboard.KindAdmin && e.Org == org {
			return true, nil
		}
	}
	return false, nil
}

// callerOwner is the actor id of the person the caller acts for when the
// caller is an agent: the owner of a detected agent, else the
// owner_actor_id `lore agent start` recorded on the agent's roster row
// (an agent named by LORE_ACTOR=agent:<label>). Zero for a person, or an
// agent with no readable owner.
func (p *profileRoster) callerOwner(ctx context.Context) ids.ID[ids.Actor] {
	if o := p.st.who.Owner; o != nil {
		if oid, err := o.ActorID(); err == nil {
			return oid
		}
	}
	r, err := p.getOnce(ctx, schema.TableActors, p.st.actor.Any())
	if err != nil || fieldText(r, actorFieldKind) != schema.ActorKindAgent {
		return ids.ID[ids.Actor]{}
	}
	oid, err := ids.Parse[ids.Actor](fieldText(r, rosterFieldOwner))
	if err != nil {
		return ids.ID[ids.Actor]{}
	}
	return oid
}

// RenameIdentity implements profilestore.IdentityRenamer: in a repo with
// no org the source of the caller's actor is the identity block the chain
// answered from, so --name is written there (never into a row: the copies
// follow on the next write to each store).
func (a *App) RenameIdentity(ctx context.Context, ref, name string) (profilestore.Renamed, error) {
	st, err := a.state(ctx)
	if err != nil {
		return profilestore.Renamed{}, err
	}
	id, err := a.ownActorRef(ctx, st, strings.TrimSpace(ref))
	if err != nil {
		return profilestore.Renamed{}, err
	}
	out := profilestore.Renamed{ID: id.String(), From: st.who.DisplayName, To: name}
	who := st.who
	switch who.Step {
	case identity.StepSetup, identity.StepTop, identity.StepOrg:
	default:
		return out, errs.New(errs.Refused, "your identity comes from "+string(who.Step)+", which lore does not write, so lore cannot rename it").
			WithHint(`persist your identity with the new name: lore identity set "` + name + `" --key ` + who.StableKey)
	}
	// The identity setter of ops/identity (wire SetIdentity): the active
	// solo setup's identity, a team setup's orgs[] entry, else identity:.
	res, err := a.SetIdentity(ctx, opsidentity.Entry{Key: who.StableKey, Name: name, Handle: who.Handle})
	if err != nil {
		return out, err
	}
	switch {
	case res.Setup != "":
		out.Where = "setup " + res.Setup
	case res.Org != "":
		out.Where = "the identity of org " + res.Org + " in " + res.Path
	default:
		out.Where = "identity: in " + res.Path
	}
	return out, nil
}

// ownActorRef resolves ref to the caller's own actor or refuses: in a repo
// with no org an actor's source is that person's identity on their own
// machine, which only they can change.
func (a *App) ownActorRef(ctx context.Context, st *state, ref string) (ids.AnyID, error) {
	me := st.actor.Any()
	switch {
	case ref == "" || ref == st.who.StableKey || ref == me.String():
		return me, nil
	case strings.HasPrefix(ref, rosterHandleMark):
		h := strings.TrimPrefix(ref, rosterHandleMark)
		if h != "" && (h == st.who.Handle || h == defaultRosterHandle(st.who.Handle, st.who.StableKey)) {
			return me, nil
		}
		if r, ok := a.actorByHandleHere(ctx, st, h); ok && r == me {
			return me, nil
		}
	}
	return ids.AnyID{}, errs.New(errs.Refused, "in a repo with no org an actor's name is that person's own identity, so only they can rename it; "+ref+" is not you ("+me.String()+")").
		WithHint("they run `lore actor edit --actor <them> --name …` on their machine; your own: lore actor edit --actor " + me.String() + " --name …")
}

// actorByHandleHere is the actor id of the first live actors row with
// handle h in the attached stores.
func (a *App) actorByHandleHere(ctx context.Context, st *state, h string) (ids.AnyID, bool) {
	for _, name := range st.eng.Stores() {
		rows, err := st.eng.List(ctx, name, schema.TableActors)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if !r.Deleted && fieldText(r, actorFieldHandle) == h {
				return r.ID, true
			}
		}
	}
	return ids.AnyID{}, false
}
