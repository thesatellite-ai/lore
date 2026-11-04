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
	v, err := coreApp.StoreFormat(ctx, target)
	if err != nil {
		return nil, coreErr(err)
	}
	if v < rowfs.MarkerVersion {
		return nil, nil
	}
	return &coreRoster{app: coreApp, target: target, folder: sc.OrgFolder, where: org.Name + " " + sc.OrgFolder + "/",
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
	actors, err := r.app.RosterActors(ctx, r.target)
	if err != nil {
		return wire.RosterActor{}, coreErr(err)
	}
	for _, a := range actors {
		if a.Active && a.Handle == h {
			return a, nil
		}
	}
	return wire.RosterActor{}, errcodes.New(errcodes.InvalidInput, "no one is @"+h+" on "+r.where+"'s roster").
		WithHint(r.missHint())
}

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
	return id, created, r.push(ctx)
}

// push syncs the roster's store now when autopush is on; a failure is a
// note (the rows are committed in the local clone; `lore sync` pushes).
func (r *coreRoster) push(ctx context.Context) error {
	if !r.autopush {
		return nil
	}
	if _, err := r.app.SyncStores(ctx, string(r.target)); err != nil {
		fmt.Fprintln(os.Stderr, "lore: the roster change is committed in the local store copy; pushing failed ("+redactIfQuiet(err.Error())+"); `lore sync` pushes it")
	}
	return nil
}

// registerHuman is `lore actor register` on the core roster.
func (r *coreRoster) registerHuman(ctx context.Context, key, name, handle string) error {
	h := defaultHandle(handle, key)
	id, created, err := r.register(ctx, wire.RosterActor{Kind: schema.ActorKindHuman, StableKey: key, Name: name, Handle: h})
	if err != nil {
		return err
	}
	verb := "registered"
	if !created {
		verb = "already registered (refreshed)"
	}
	shown := ""
	if h != "" {
		shown = " (@" + h + ")"
	}
	fmt.Printf("✓ %s %s%s %s in %s\n", id, name, shown, verb, redactIfQuiet(r.where))
	return nil
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
func (r *coreRoster) deactivate(ctx context.Context, handle string) error {
	a, err := r.resolveHandle(ctx, handle)
	if err != nil {
		return err
	}
	if err := r.app.RosterDeactivate(ctx, r.target, a.ID); err != nil {
		return coreErr(err)
	}
	if err := r.push(ctx); err != nil {
		return err
	}
	fmt.Printf("✓ %s %s deactivated (kept on the roster; old rows still resolve)\n", a.ID, a.Name)
	return nil
}

// teamAdd is `lore team add` on the core roster.
func (r *coreRoster) teamAdd(ctx context.Context, handle, name, members string) error {
	actors, err := r.app.RosterActors(ctx, r.target)
	if err != nil {
		return coreErr(err)
	}
	for _, a := range actors {
		if a.Active && a.Handle == handle {
			return errcodes.New(errcodes.InvalidInput, "handle @"+handle+" is taken by actor "+a.Name)
		}
	}
	var ids []string
	for _, m := range strings.Split(members, ",") {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		a, err := r.resolveHandle(ctx, m)
		if err != nil {
			return err
		}
		ids = append(ids, a.ID)
	}
	id, _, err := r.app.RosterTeamPut(ctx, r.target, r.folder, handle, name, ids)
	if err != nil {
		return coreErr(err)
	}
	if err := r.push(ctx); err != nil {
		return err
	}
	fmt.Printf("✓ %s @%s (%d member(s)) in %s\n", id, handle, len(ids), redactIfQuiet(r.where))
	return nil
}

// teamList is `lore team list` on the core roster.
func (r *coreRoster) teamList(ctx context.Context) error {
	teams, err := r.app.RosterTeams(ctx, r.target)
	if err != nil {
		return coreErr(err)
	}
	n := 0
	for _, t := range teams {
		if !t.Active {
			continue
		}
		n++
		fmt.Printf("@%-16s %-24s %d member(s)\n", t.Handle, t.Name, len(t.Members))
	}
	if n == 0 {
		fmt.Println("no teams on " + redactIfQuiet(r.where) + "'s roster")
	}
	return nil
}
