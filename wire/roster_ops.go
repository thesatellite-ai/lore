package wire

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
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

// hostRunOp reports whether wire runs the operation name directly.
func hostRunOp(name string) bool {
	return opsaudit.IsAuditOp(name) || opsinfra.IsRosterOp(name) || opsagent.IsDirectOp(name)
}

// runDirect decodes and invokes e with no engine around it.
func runDirect(ctx context.Context, e op.Entry, d op.Decoder) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	return e.Invoke(ctx, in)
}

// ActorRegister implements opsinfra.Roster.
func (a *App) ActorRegister(ctx context.Context, in opsinfra.ActorRegisterIn) (opsinfra.ActorRegisterOut, error) {
	if a.opts.Roster != nil {
		return a.opts.Roster.ActorRegister(ctx, in)
	}
	r, who, err := a.repoRoster(ctx)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: who.StableKey, Name: who.DisplayName, Handle: defaultRosterHandle(who.Handle, who.StableKey)}
	if in.Kind == opsinfra.RegisterKindAgent {
		owner, oerr := a.rosterOwner(ctx, r, in.Owner, who.StableKey)
		if oerr != nil {
			return opsinfra.ActorRegisterOut{}, oerr
		}
		sp = RosterActor{Kind: schema.ActorKindAgent, StableKey: agentStableKeyPrefix + in.Label, Name: in.Label, Label: in.Label, OwnerID: owner.ID}
	}
	if sp.Handle != "" {
		if herr := a.rosterHandleFree(ctx, r, sp.Handle, sp.StableKey); herr != nil {
			return opsinfra.ActorRegisterOut{}, herr
		}
	}
	id, created, err := a.RosterRegister(ctx, r.src, sp)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	a.rosterPush(ctx, r)
	return opsinfra.ActorRegisterOut{ID: id, Kind: in.Kind, Name: sp.Name, Handle: sp.Handle, Label: sp.Label, Created: created, Where: r.shown()}, nil
}

// ActorDeactivate implements opsinfra.Roster.
func (a *App) ActorDeactivate(ctx context.Context, handle string) (opsinfra.ActorDeactivateOut, error) {
	if a.opts.Roster != nil {
		return a.opts.Roster.ActorDeactivate(ctx, handle)
	}
	r, _, err := a.repoRoster(ctx)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	act, err := a.rosterByHandle(ctx, r, handle)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	if err := a.RosterDeactivate(ctx, r.src, act.ID); err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	a.rosterPush(ctx, r)
	return opsinfra.ActorDeactivateOut{ID: act.ID, Name: act.Name}, nil
}

// rosterAt is the roster a verb works on: the repo's own (no org, Q4) or
// the org folder of the resolved org's store ("<org>/<folder>", CFG1,
// P3-CMDLORE-5).
type rosterAt struct {
	src store.Source
	// where names it in messages: "this repo", "northwind org/".
	where string
}

// shown is where for output: an org folder is internal, so it is hidden
// without a terminal or in CI (§6.5).
func (r rosterAt) shown() string {
	if r.src != store.SourceRepo && quietOutput() {
		return redactedText
	}
	return r.where
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

// repoRoster picks the roster and returns the caller's identity. An agent
// never registers (F18: its owner runs `lore agent start`). With an org,
// the roster is its org folder store when OrgStores attaches one; an org
// without one is refused rather than written to the wrong place.
func (a *App) repoRoster(ctx context.Context) (rosterAt, whoIs, error) {
	r := rosterAt{src: store.SourceRepo, where: repoRosterWhere}
	if a.opts.Org != nil {
		org, err := a.opts.Org(ctx)
		if err != nil {
			return r, whoIs{}, err
		}
		if org != nil {
			src, folder, ok, serr := a.orgRosterSource(ctx, org.Name)
			if serr != nil {
				return r, whoIs{}, serr
			}
			if !ok {
				return r, whoIs{}, errs.New(errs.NotImplemented, "org "+org.Name+"'s roster is kept in its org store, which this lore build does not reach for the roster").
					WithHint("set org_folder on one of the org's stores in ~/.lore/config.yaml")
			}
			r = rosterAt{src: src, where: org.Name + " " + folder + "/"}
		}
	}
	who, err := a.Who(ctx)
	if err != nil {
		return r, whoIs{}, err
	}
	if who.Kind == schema.ActorKindAgent {
		return r, whoIs{}, errs.New(errs.InvalidInput, "an agent cannot register actors; its owner runs `lore agent start --label <l> --owner @<owner>`")
	}
	if who.StableKey == "" {
		return r, whoIs{}, errs.New(errs.InvalidInput, "no identity: lore cannot tell who you are").WithHint("run `lore whoami` to see the identity chain")
	}
	return r, whoIs{StableKey: who.StableKey, DisplayName: who.DisplayName, Handle: who.Handle}, nil
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
	return errs.New(errs.InvalidInput, fmt.Sprintf("handle @%s is taken by %s on %s's roster", handle, holder, r.where)).
		WithHint("choose another handle (`lore identity set --org <org> --handle <h>`); handles are unique per roster")
}

// rosterByHandle finds the active actor @handle on the repo's roster.
func (a *App) rosterByHandle(ctx context.Context, r rosterAt, handle string) (RosterActor, error) {
	h := strings.TrimPrefix(handle, rosterHandleMark)
	// A miss on an org roster re-reads it once after syncing its store
	// (v0.1.17 refreshOnMiss: a teammate may have just registered).
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
			return RosterActor{}, err
		}
		for _, x := range actors {
			if x.Active && x.Handle == h {
				return x, nil
			}
		}
	}
	return RosterActor{}, errs.New(errs.InvalidInput, "no one is @"+h+" on "+r.where+"'s roster").
		WithHint(r.missHint())
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
		actors, err := a.RosterActors(ctx, r.src)
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
			return RosterActor{}, errs.New(errs.InvalidInput, "you are not on "+r.where+"'s roster yet").WithHint("run `lore actor register` first")
		}
	}
	if owner.Kind != schema.ActorKindHuman {
		return RosterActor{}, errs.New(errs.InvalidInput, "an agent's owner must be a human on the roster")
	}
	return owner, nil
}
