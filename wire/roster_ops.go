package wire

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
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

// ActorRegister implements opsinfra.Roster.
func (a *App) ActorRegister(ctx context.Context, in opsinfra.ActorRegisterIn) (opsinfra.ActorRegisterOut, error) {
	who, err := a.repoRoster(ctx)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: who.StableKey, Name: who.DisplayName, Handle: defaultRosterHandle(who.Handle, who.StableKey)}
	if in.Kind == opsinfra.RegisterKindAgent {
		owner, oerr := a.rosterOwner(ctx, in.Owner, who.StableKey)
		if oerr != nil {
			return opsinfra.ActorRegisterOut{}, oerr
		}
		sp = RosterActor{Kind: schema.ActorKindAgent, StableKey: agentStableKeyPrefix + in.Label, Name: in.Label, Label: in.Label, OwnerID: owner.ID}
	}
	if sp.Handle != "" {
		if herr := a.rosterHandleFree(ctx, sp.Handle, sp.StableKey); herr != nil {
			return opsinfra.ActorRegisterOut{}, herr
		}
	}
	id, created, err := a.RosterRegister(ctx, store.SourceRepo, sp)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	return opsinfra.ActorRegisterOut{ID: id, Kind: in.Kind, Name: sp.Name, Handle: sp.Handle, Label: sp.Label, Created: created, Where: repoRosterWhere}, nil
}

// ActorDeactivate implements opsinfra.Roster.
func (a *App) ActorDeactivate(ctx context.Context, handle string) (opsinfra.ActorDeactivateOut, error) {
	if _, err := a.repoRoster(ctx); err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	act, err := a.rosterByHandle(ctx, handle)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	if err := a.RosterDeactivate(ctx, store.SourceRepo, act.ID); err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	return opsinfra.ActorDeactivateOut{ID: act.ID, Name: act.Name}, nil
}

// repoRoster checks that this App's roster is the repo's own (no org, Q4)
// and returns the caller's identity. An agent never registers (F18: its
// owner runs `lore agent start`). An org's roster lives in the org store's
// org folder, which only a host with org resolution reaches
// (Options.Roster): refused here rather than written to the wrong place.
func (a *App) repoRoster(ctx context.Context) (whoIs, error) {
	if a.opts.Org != nil {
		org, err := a.opts.Org(ctx)
		if err != nil {
			return whoIs{}, err
		}
		if org != nil {
			return whoIs{}, errs.New(errs.NotImplemented, "org "+org.Name+"'s roster is kept in its org store, which this lore build does not reach for the roster").
				WithHint("run the command with the lore release binary")
		}
	}
	who, err := a.Who(ctx)
	if err != nil {
		return whoIs{}, err
	}
	if who.Kind == schema.ActorKindAgent {
		return whoIs{}, errs.New(errs.InvalidInput, "an agent cannot register actors; its owner runs `lore agent start --label <l> --owner @<owner>`")
	}
	if who.StableKey == "" {
		return whoIs{}, errs.New(errs.InvalidInput, "no identity: lore cannot tell who you are").WithHint("run `lore whoami` to see the identity chain")
	}
	return whoIs{StableKey: who.StableKey, DisplayName: who.DisplayName, Handle: who.Handle}, nil
}

// whoIs is the part of the identity a registration uses.
type whoIs struct{ StableKey, DisplayName, Handle string }

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
func (a *App) rosterHandleFree(ctx context.Context, handle, stableKey string) error {
	actors, err := a.RosterActors(ctx, store.SourceRepo)
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
		teams, terr := a.RosterTeams(ctx, store.SourceRepo)
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
	return errs.New(errs.InvalidInput, fmt.Sprintf("handle @%s is taken by %s on %s's roster", handle, holder, repoRosterWhere)).
		WithHint("choose another handle (`lore identity set --org <org> --handle <h>`); handles are unique per roster")
}

// rosterByHandle finds the active actor @handle on the repo's roster.
func (a *App) rosterByHandle(ctx context.Context, handle string) (RosterActor, error) {
	h := strings.TrimPrefix(handle, rosterHandleMark)
	actors, err := a.RosterActors(ctx, store.SourceRepo)
	if err != nil {
		return RosterActor{}, err
	}
	for _, x := range actors {
		if x.Active && x.Handle == h {
			return x, nil
		}
	}
	return RosterActor{}, errs.New(errs.InvalidInput, "no one is @"+h+" on "+repoRosterWhere+"'s roster").
		WithHint("register first: `lore actor register` (as that person)")
}

// rosterOwner is an agent's owner: @handle, or the caller's own actor.
func (a *App) rosterOwner(ctx context.Context, handle, callerKey string) (RosterActor, error) {
	var owner RosterActor
	if handle != "" {
		o, err := a.rosterByHandle(ctx, handle)
		if err != nil {
			return RosterActor{}, err
		}
		owner = o
	} else {
		actors, err := a.RosterActors(ctx, store.SourceRepo)
		if err != nil {
			return RosterActor{}, err
		}
		found := false
		for _, x := range actors {
			if x.Active && x.StableKey == callerKey {
				owner, found = x, true
			}
		}
		if !found {
			return RosterActor{}, errs.New(errs.InvalidInput, "you are not on "+repoRosterWhere+"'s roster yet").WithHint("run `lore actor register` first")
		}
	}
	if owner.Kind != schema.ActorKindHuman {
		return RosterActor{}, errs.New(errs.InvalidInput, "an agent's owner must be a human on the roster")
	}
	return owner, nil
}
