// roster_core.go — the org roster on the 0.1.18 core (LORE_TEAM_SPEC F18,
// CFG1): when the repo's org keeps its roster in a 0.1.18 store (a
// lore_store: 2 marker, made by `lore store init`), `lore actor
// register|deactivate`, `lore agent start` and `lore team add|list` write
// and read the actors and teams rows of the org folder through the core
// (wire roster API), which coreOrgStores attached as "<org>/<org folder>".
// A v0.1.17 org store, an org with no org folder store, or no org at all
// stays on roster_cmd.go's v0.1.17 path (the repo's own actors with no
// org), so v0.1.17 teammates never meet `_v: 2` rows before `lore migrate
// run`. Output lines are roster_cmd.go's.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// coreRoster is an org roster kept by the core.
type coreRoster struct {
	app    *wire.App
	target store.Source
	folder string
	// where names it in messages: "agency org/".
	where string
	// autopush: push the roster write at once, like v0.1.17 (live.autopush,
	// LORE_LIVE_AUTOPUSH=0 leaves it for `lore sync`).
	autopush bool
}

// openCoreRoster returns the core roster for the working directory, or nil
// when the v0.1.17 path applies (see the file comment).
func openCoreRoster(ctx context.Context) (*coreRoster, error) {
	if coreApp == nil {
		return nil, nil
	}
	s, err := coreSituationOnce(ctx)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, orgResolveCLIError(s.ResolveErr)
	}
	org := s.Result.Org
	if org == nil {
		return nil, nil
	}
	sc := org.OrgFolderStore()
	if sc == nil {
		return nil, nil
	}
	target := store.Source(org.Name + "/" + sc.OrgFolder)
	app, err := rosterCoreApp(ctx, target) // BUG-T3: outside a repo, a store-only App
	if err != nil {
		return nil, err
	}
	v, err := app.StoreFormat(ctx, target)
	if err != nil {
		return nil, coreErr(err)
	}
	if v < rowfs.MarkerVersion {
		return nil, nil
	}
	return &coreRoster{app: app, target: target, folder: sc.OrgFolder, where: org.Name + " " + sc.OrgFolder + "/",
		autopush: storeAutopush(s.Config, org)}, nil
}

// handleTaken returns who holds handle on the roster, other than stableKey.
func (r *coreRoster) handleTaken(ctx context.Context, handle, stableKey string) (string, error) {
	actors, err := r.app.RosterActors(ctx, r.target)
	if err != nil {
		return "", coreErr(err)
	}
	for _, a := range actors {
		if a.Active && a.Handle == handle && a.StableKey != stableKey {
			return "actor " + a.Name, nil
		}
	}
	teams, err := r.app.RosterTeams(ctx, r.target)
	if err != nil {
		return "", coreErr(err)
	}
	for _, t := range teams {
		if t.Active && t.Handle == handle {
			return "team " + t.Handle, nil
		}
	}
	return "", nil
}

// resolveHandle finds an active actor by @handle.
func (r *coreRoster) resolveHandle(ctx context.Context, h string) (wire.RosterActor, error) {
	h = strings.TrimPrefix(h, handlePrefix)
	// A miss re-reads the roster once from the store's remote (v0.1.17's
	// refreshOnMiss): a teammate who just ran `lore actor register` on
	// another machine pushed their row, and this machine's copy of the
	// store may predate it. A failed sync keeps the copy (the miss stands,
	// with the `lore sync` hint).
	for attempt := 0; attempt < rosterLookups; attempt++ {
		if attempt > 0 && r.target != store.SourceRepo {
			if _, err := r.app.Sync(ctx, r.target); err != nil {
				break
			}
		}
		actors, err := r.app.RosterActors(ctx, r.target)
		if err != nil {
			return wire.RosterActor{}, coreErr(err)
		}
		for _, a := range actors {
			if a.Active && a.Handle == h {
				return a, nil
			}
		}
	}
	return wire.RosterActor{}, errcodes.New(errcodes.InvalidInput, "no one is @"+h+" on "+r.where+"'s roster").
		WithHint(r.missHint())
}

// rosterLookups bounds a handle lookup: the cached store copy, then once
// more after syncing the roster's store.
const rosterLookups = 2

// missHint is the hint for a handle missing from this roster: the repo's
// own roster (no org) has no store copy to be stale, so the person has not
// registered (roster_cmd.go's v0.1.17 wording).
func (r *coreRoster) missHint() string {
	if r.target == store.SourceRepo {
		return repoRosterMissHint
	}
	return rosterMissHint
}

// byKey finds an active actor by stable key.
func (r *coreRoster) byKey(ctx context.Context, key string) (wire.RosterActor, bool, error) {
	actors, err := r.app.RosterActors(ctx, r.target)
	if err != nil {
		return wire.RosterActor{}, false, coreErr(err)
	}
	for _, a := range actors {
		if a.Active && a.StableKey == key {
			return a, true, nil
		}
	}
	return wire.RosterActor{}, false, nil
}

// register creates or refreshes sp, refusing a handle someone else holds.
func (r *coreRoster) register(ctx context.Context, sp wire.RosterActor) (string, bool, error) {
	if sp.Handle != "" {
		holder, err := r.handleTaken(ctx, sp.Handle, sp.StableKey)
		if err != nil {
			return "", false, err
		}
		if holder != "" {
			return "", false, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("handle @%s is taken by %s on %s's roster", sp.Handle, holder, r.where)).
				WithHint("choose another handle (`lore identity set --org <org> --handle <h>`); handles are unique per roster")
		}
	}
	id, created, err := r.app.RosterRegister(ctx, r.target, sp)
	if err != nil {
		return "", false, coreErr(err)
	}
	r.push(ctx)
	return id, created, nil
}

// push syncs the roster's store now when autopush is on. A failure is only
// a note, never an error: the rows are committed in the local clone and
// `lore sync` pushes them.
func (r *coreRoster) push(ctx context.Context) {
	if !r.autopush {
		return
	}
	if _, err := r.app.SyncStores(ctx, string(r.target)); err != nil {
		fmt.Fprintln(os.Stderr, "lore: the roster change is committed in the local store copy; pushing failed ("+redactIfQuiet(err.Error())+"); `lore sync` pushes it")
	}
}

// registerHuman is `lore actor register` on the core roster.
func (r *coreRoster) registerHuman(ctx context.Context, key, name, handle string) (opsinfra.ActorRegisterOut, error) {
	h := defaultHandle(handle, key)
	id, created, err := r.register(ctx, wire.RosterActor{Kind: schema.ActorKindHuman, StableKey: key, Name: name, Handle: h})
	if err != nil {
		return opsinfra.ActorRegisterOut{}, err
	}
	return opsinfra.ActorRegisterOut{ID: id, Kind: opsinfra.RegisterKindHuman, Name: name, Handle: h, Created: created, Where: redactIfQuiet(r.where)}, nil
}

// registerAgent is rosterRegisterAgent on the core roster.
func (r *coreRoster) registerAgent(ctx context.Context, label, ownerHandle string) (string, error) {
	var owner wire.RosterActor
	var err error
	if ownerHandle != "" {
		owner, err = r.resolveHandle(ctx, ownerHandle)
	} else {
		owner, err = r.caller(ctx)
	}
	if err != nil {
		return "", err
	}
	if owner.Kind != schema.ActorKindHuman {
		return "", errcodes.New(errcodes.InvalidInput, "an agent's owner must be a human on the roster")
	}
	id, _, err := r.register(ctx, wire.RosterActor{Kind: schema.ActorKindAgent, StableKey: agentKeyPrefix + label, Name: label, Label: label, OwnerID: owner.ID})
	return id, err
}

// caller is the caller's own active actor on the roster.
func (r *coreRoster) caller(ctx context.Context) (wire.RosterActor, error) {
	id, err := resolveIdentityHere(ctx)
	if err != nil {
		return wire.RosterActor{}, err
	}
	a, ok, err := r.byKey(ctx, id.StableKey)
	if err != nil {
		return wire.RosterActor{}, err
	}
	if !ok {
		return wire.RosterActor{}, errcodes.New(errcodes.InvalidInput, "you are not on "+r.where+"'s roster yet").WithHint("run `lore actor register` first")
	}
	return a, nil
}

// deactivate is `lore actor deactivate` on the core roster.
func (r *coreRoster) deactivate(ctx context.Context, handle string) (opsinfra.ActorDeactivateOut, error) {
	a, err := r.resolveHandle(ctx, handle)
	if err != nil {
		return opsinfra.ActorDeactivateOut{}, err
	}
	if err := r.app.RosterDeactivate(ctx, r.target, a.ID); err != nil {
		return opsinfra.ActorDeactivateOut{}, coreErr(err)
	}
	r.push(ctx)
	return opsinfra.ActorDeactivateOut{ID: a.ID, Name: a.Name}, nil
}
