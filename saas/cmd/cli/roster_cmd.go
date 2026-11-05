// roster_cmd.go — the org roster (LORE_TEAM_SPEC F18, §7.2, C15):
// `lore actor register|deactivate`, `lore agent start`, `lore team
// add|list`. The roster is <org_folder>/.lore/data/actors (and teams/) in
// the org's store; ids derive from stable_key (natural_ids.go), so the
// same person registering on two machines writes one file. With no org,
// the repo's own actors table is the roster (coordinator answer Q4).
// Nothing here writes into a client repo. An org whose roster is in a
// 0.1.18 store goes through the core instead (roster_core.go).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	entTeam "dbent/gen/ent/team"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/rowroute"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/store"
)

// Roster flags and values.
const (
	flagRosterKind    = "kind"
	flagRosterLabel   = "label"
	flagRosterOwner   = "owner"
	flagRosterName    = "name"
	flagRosterMembers = "members"
	// agentKeyPrefix is the stable_key prefix of agent actors
	// (LORE_ACTOR=agent:<label>, F18).
	agentKeyPrefix = "agent:"
	handlePrefix   = "@"
)

// attachRosterCommands adds the roster subcommands to the existing
// `lore actor` group and the new `lore team` group. (`lore agent start` is
// lore-signals'; its roster write is rosterRegisterAgent.)
func attachRosterCommands(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.Name() == "actor" {
			c.AddCommand(newActorRegisterCommand(), newActorDeactivateCommand())
		}
	}
	team := &cobra.Command{Use: "team", Short: "Teams on the org roster (@handle resolves to a person or a team)"}
	team.AddCommand(newTeamAddCommand(), newTeamListCommand())
	root.AddCommand(team)
}

// roster is an opened roster: the org folder's project (or this repo's
// project when no org applies), as an ent client.
type roster struct {
	client *ent.Client
	// where names it in messages: "northwind org/" or "this repo".
	where string
	org   *lorecfg.Org
	// project is the roster project's id.
	project string
	// ext is the org folder's mirror when the roster lives in a store; nil
	// for "this repo".
	ext *externalProject
	// ownOpen is true when this roster opened ext (it was not already open
	// in this command), so nothing has been written to it yet and a fresh
	// re-read cannot drop a pending write.
	ownOpen bool
}

// openRoster opens the roster for the working directory.
func openRoster(ctx context.Context, c *commonFlags) (*roster, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, orgResolveCLIError(s.ResolveErr)
	}
	r := &roster{org: s.Result.Org, where: repoRosterWhere}
	if org := s.Result.Org; org != nil {
		sc := org.OrgFolderStore()
		if sc == nil {
			return nil, errcodes.New(errcodes.InvalidInput, "org "+org.Name+" has no roster: no store sets org_folder").
				WithHint("set org_folder on one of " + org.Name + "'s stores in ~/.lore/config.yaml")
		}
		before := len(openedExternal)
		p, perr := openStoreProject(ctx, s.Config, storeProjectRef{Org: org, Store: *sc, Folder: sc.OrgFolder})
		if perr != nil {
			return nil, perr
		}
		r.ext, r.ownOpen = p, len(openedExternal) > before
		useExternalProject(c, p)
		r.where = org.Name + " " + sc.OrgFolder + "/"
	}
	rctx, client, err := resolveContext(c)
	if err != nil {
		return nil, err
	}
	pid, err := resolveProjectID(ctx, rctx.ProjectID)
	if err != nil {
		_ = client.Close() // the project error is the one to report
		return nil, err
	}
	r.client, r.project = client, pid
	return r, nil
}

// openRosterOnCore is the roster the core keeps for the working
// directory: the org's 0.1.18 roster (openCoreRoster), or, in a repo with
// no org, the repo store's own actors and teams rows (coordinator answer
// Q4). actors and teams are core-owned in a checkout, so a lore.db-only
// write there is never exported (the handle would be lost). nil: the
// v0.1.17 path (a v0.1.17 org store) or a client repo, whose roster is
// never the repo itself.
func openRosterOnCore(ctx context.Context) (*coreRoster, error) {
	cr, err := openCoreRoster(ctx)
	if cr != nil || err != nil || coreApp == nil {
		return cr, err
	}
	s, err := coreSituationOnce(ctx)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, orgResolveCLIError(s.ResolveErr)
	}
	if s.Result.Org != nil {
		return nil, nil
	}
	if client, cerr := coreRepoIsClient(ctx); cerr != nil || client {
		return nil, nil
	}
	// No org: the repo's own actors rows are the roster (Q4) and go LIVE,
	// so every clone resolves the handle after its next fetch (coordinator
	// answer Q12), as the v0.1.17 path did (BUG-T1). liveBackend honours
	// this explicit choice for the actors table (liveChoiceFor).
	liveCmdState.table, liveCmdState.choice = tableActors, rowroute.ChoiceLive
	return &coreRoster{app: coreApp, target: store.SourceRepo, where: repoRosterWhere}, nil
}

// repoRosterWhere names the repo's own roster in messages.
const repoRosterWhere = "this repo"

// handleTaken returns who holds handle on the roster, other than stableKey.
func (r *roster) handleTaken(ctx context.Context, handle, stableKey string) (string, error) {
	a, err := r.client.Actor.Query().Where(entActor.Handle(handle), entActor.Active(true), entActor.StableKeyNEQ(stableKey)).First(ctx)
	if err == nil {
		return "actor " + a.DisplayName, nil
	}
	if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
	}
	t, err := r.client.Team.Query().Where(entTeam.Handle(handle), entTeam.Active(true)).First(ctx)
	if err == nil {
		return "team " + t.Handle, nil
	}
	if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
	}
	return "", nil
}

// tableActors is the roster table (.lore/data/actors).
const tableActors = string(constants.EntityActor)

// defaultHandle is the roster handle when the identity names none: the
// local part of the email in a human stable key (human:dev3@acme.example →
// dev3), so `@dev3` resolves in a team without org config (cookbook 2).
func defaultHandle(handle, stableKey string) string {
	if handle != "" {
		return handle
	}
	email := emailOfKey(stableKey)
	if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
		return local
	}
	return ""
}

// actorSpec is one registration.
type actorSpec struct {
	Kind      entActor.Kind
	StableKey string
	Name      string
	Handle    string
	Label     string
	OwnerID   string
}

// register creates or refreshes an actor (idempotent: same stable_key, same
// id, same file). A handle another active actor or team holds is refused.
func (r *roster) register(ctx context.Context, sp actorSpec) (*ent.Actor, bool, error) {
	if sp.Handle != "" {
		holder, err := r.handleTaken(ctx, sp.Handle, sp.StableKey)
		if err != nil {
			return nil, false, err
		}
		if holder != "" {
			return nil, false, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("handle @%s is taken by %s on %s's roster", sp.Handle, holder, r.where)).
				WithHint("choose another handle (`lore identity set --org <org> --handle <h>`); handles are unique per roster")
		}
	}
	// actorByKey also finds this person's row when it arrived as a client
	// repo projection (no-org repos export projections too), so a second
	// machine refreshes that row instead of colliding on its id (N1).
	existing, err := actorByKey(ctx, r.client, sp.StableKey, true)
	switch {
	case err == nil:
		up := existing.Update().SetDisplayName(sp.Name).SetActive(true).ClearLeftAt()
		if sp.Handle != "" {
			up.SetHandle(sp.Handle)
		}
		if sp.Label != "" {
			up.SetLabel(sp.Label)
		}
		if sp.OwnerID != "" {
			up.SetOwnerActorID(sp.OwnerID)
		}
		a, serr := up.Save(ctx)
		if serr != nil {
			return nil, false, errcodes.New(errcodes.Internal, "update the roster").WithCause(serr)
		}
		return a, false, nil
	case !ent.IsNotFound(err):
		return nil, false, errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
	}
	cr := r.client.Actor.Create().SetKind(sp.Kind).SetStableKey(sp.StableKey).SetDisplayName(sp.Name).SetActive(true)
	if sp.Handle != "" {
		cr.SetHandle(sp.Handle)
	}
	if sp.Label != "" {
		cr.SetLabel(sp.Label)
	}
	if sp.OwnerID != "" {
		cr.SetOwnerActorID(sp.OwnerID)
	}
	a, err := cr.Save(ctx)
	if err != nil {
		return nil, false, errcodes.New(errcodes.Internal, "write the roster").WithCause(err)
	}
	return a, true, nil
}

// resolveHandle finds an active actor by @handle on the roster.
func (r *roster) resolveHandle(ctx context.Context, h string) (*ent.Actor, error) {
	h = strings.TrimPrefix(h, handlePrefix)
	a, err := r.client.Actor.Query().Where(entActor.Handle(h), entActor.Active(true)).First(ctx)
	if ent.IsNotFound(err) && r.refreshOnMiss(ctx) {
		a, err = r.client.Actor.Query().Where(entActor.Handle(h), entActor.Active(true)).First(ctx)
	}
	if ent.IsNotFound(err) {
		return nil, errcodes.New(errcodes.InvalidInput, "no one is @"+h+" on "+r.where+"'s roster").
			WithHint(r.missHint())
	}
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
	}
	return a, nil
}

// Next steps after an @handle is not on the roster. A store roster's
// usual cause is a store copy older than the teammate's registration; a
// repo roster (no org) has no store copy, so the person has not
// registered (or their registration is not pulled yet).
const (
	rosterMissHint     = "run `lore sync` (your store copy may be older than a teammate's registration), or check the handle with `lore team list`"
	repoRosterMissHint = "register first: `lore actor register` (as that person)"
)

// missHint is the hint for a handle missing from this roster.
func (r *roster) missHint() string {
	if r.ext != nil {
		return rosterMissHint
	}
	return repoRosterMissHint
}

// refreshOnMiss re-reads the roster from a fresh store fetch when the open
// was served by the fetch cache (a teammate registered seconds ago is not
// in it yet), at most once per roster. It reports whether the roster was
// re-read; a failed fetch keeps the cached roster (the miss then stands,
// with rosterMissHint). Only a roster this lookup opened itself is
// re-read: a mirror opened earlier in the command may hold writes.
func (r *roster) refreshOnMiss(ctx context.Context) bool {
	if r.ext == nil || !r.ownOpen || !r.ext.fetchCached || r.ext.refresh == nil {
		return false
	}
	if err := r.ext.refresh(ctx); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn("lore: could not refresh "+r.where+"'s roster: "+redactIfQuiet(err.Error())))
		return false
	}
	return true
}

func newActorRegisterCommand() *cobra.Command {
	var f commonFlags
	var kind, label, owner string
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Put yourself (or, with --kind agent, an agent you own) on the org roster",
		Example: `  lore actor register
  lore actor register --kind agent --label agent-frontend --owner @alice
  lore agent start --label agent-frontend --owner @alice   # same, and prints the session env`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			id, err := resolveIdentityHere(ctx)
			if err != nil {
				return err
			}
			if id.Kind == identityKindAgent {
				return errcodes.New(errcodes.InvalidInput, "an agent cannot register actors; its owner runs `lore agent start --label <l> --owner @<owner>`")
			}
			switch kind {
			case string(entActor.KindAgent):
				aid, where, rerr := rosterRegisterAgent(ctx, label, owner)
				if rerr != nil {
					return rerr
				}
				fmt.Printf("✓ %s agent %s registered in %s\n", aid, label, redactIfQuiet(where))
				return nil
			case "", string(entActor.KindHuman):
			default:
				return errcodes.New(errcodes.InvalidInput, "--kind is human (default) or agent")
			}
			cr, err := openRosterOnCore(ctx)
			if err != nil {
				return err
			}
			if cr != nil {
				return cr.registerHuman(ctx, id.StableKey, id.DisplayName, id.Handle)
			}
			r, err := openRoster(ctx, &f)
			if err != nil {
				return err
			}
			defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
			if r.org == nil {
				// No org: the repo's own actors table is the roster (Q4), and
				// the row is written LIVE so every clone resolves the handle
				// after its next fetch (coordinator answer Q12).
				liveCmdState.table, liveCmdState.choice = tableActors, rowroute.ChoiceLive
			}
			a, created, err := r.register(ctx, actorSpec{Kind: entActor.KindHuman, StableKey: id.StableKey, Name: id.DisplayName, Handle: defaultHandle(id.Handle, id.StableKey)})
			if err != nil {
				return err
			}
			verb := "registered"
			if !created {
				verb = "already registered (refreshed)"
			}
			h := ""
			if a.Handle != nil {
				h = " (@" + *a.Handle + ")"
			}
			fmt.Printf("✓ %s %s%s %s in %s\n", a.ID, a.DisplayName, h, verb, redactIfQuiet(r.where))
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, flagRosterKind, "", "human (default) or agent")
	cmd.Flags().StringVar(&label, flagRosterLabel, "", "with --kind agent: the session label")
	cmd.Flags().StringVar(&owner, flagRosterOwner, "", "with --kind agent: the owning human as @handle (default: you)")
	return cmd
}

func newActorDeactivateCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:     "deactivate <@handle>",
		Short:   "Mark someone as gone (active=false, left_at); actors are never deleted",
		Example: `  lore actor deactivate @carol`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cr, err := openRosterOnCore(ctx)
			if err != nil {
				return err
			}
			if cr != nil {
				return cr.deactivate(ctx, args[0])
			}
			r, err := openRoster(ctx, &f)
			if err != nil {
				return err
			}
			defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
			a, err := r.resolveHandle(ctx, args[0])
			if err != nil {
				return err
			}
			if _, err := a.Update().SetActive(false).SetLeftAt(cliClock().Now().UTC()).Save(ctx); err != nil {
				return errcodes.New(errcodes.Internal, "update the roster").WithCause(err)
			}
			fmt.Printf("✓ %s %s deactivated (kept on the roster; old rows still resolve)\n", a.ID, a.DisplayName)
			return nil
		},
	}
	return cmd
}

// rosterRegisterAgent writes the agent actor for one session label on the
// org roster (F18): kind agent, stable_key agent:<label> (so the id is the
// same on every machine), label, owner = ownerHandle (an active human on
// the roster) or, when empty, the caller's own roster actor. It is the
// store half of `lore agent start` (lore-signals' coord_cmd.go). Returns
// the actor id and where it was registered.
func rosterRegisterAgent(ctx context.Context, label, ownerHandle string) (string, string, error) {
	if label == "" || strings.ContainsAny(label, " \t:/@") {
		return "", "", errcodes.New(errcodes.InvalidInput, "an agent label is one word (no spaces, ':', '/', '@')")
	}
	cr, err := openRosterOnCore(ctx)
	if err != nil {
		return "", "", err
	}
	if cr != nil {
		id, rerr := cr.registerAgent(ctx, label, ownerHandle)
		return id, cr.where, rerr
	}
	var f commonFlags
	r, err := openRoster(ctx, &f)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
	var owner *ent.Actor
	if ownerHandle != "" {
		owner, err = r.resolveHandle(ctx, ownerHandle)
	} else {
		owner, err = r.callerOnRoster(ctx)
	}
	if err != nil {
		return "", "", err
	}
	if owner.Kind != entActor.KindHuman {
		return "", "", errcodes.New(errcodes.InvalidInput, "an agent's owner must be a human on the roster")
	}
	a, _, err := r.register(ctx, actorSpec{Kind: entActor.KindAgent, StableKey: agentKeyPrefix + label, Name: label, Label: label, OwnerID: owner.ID})
	if err != nil {
		return "", "", err
	}
	return a.ID, r.where, nil
}

// callerOnRoster returns the caller's own active actor on the roster.
func (r *roster) callerOnRoster(ctx context.Context) (*ent.Actor, error) {
	id, err := resolveIdentityHere(ctx)
	if err != nil {
		return nil, err
	}
	a, err := r.client.Actor.Query().Where(entActor.StableKey(id.StableKey), entActor.Active(true)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, errcodes.New(errcodes.InvalidInput, "you are not on "+r.where+"'s roster yet").WithHint("run `lore actor register` first")
	}
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
	}
	return a, nil
}

func newTeamAddCommand() *cobra.Command {
	var f commonFlags
	var name, members string
	cmd := &cobra.Command{
		Use:     "add <@handle> --members @a,@b",
		Short:   "Create or update a team; @handle then resolves to the team",
		Example: `  lore team add @frontend --name "Frontend" --members @bob,@carol`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			handle := strings.TrimPrefix(args[0], handlePrefix)
			cr, err := openRosterOnCore(ctx)
			if err != nil {
				return err
			}
			if cr != nil {
				return cr.teamAdd(ctx, handle, name, members)
			}
			r, err := openRoster(ctx, &f)
			if err != nil {
				return err
			}
			defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
			if a, qerr := r.client.Actor.Query().Where(entActor.Handle(handle), entActor.Active(true)).First(ctx); qerr == nil {
				return errcodes.New(errcodes.InvalidInput, "handle @"+handle+" is taken by actor "+a.DisplayName)
			}
			var ids []string
			for _, m := range strings.Split(members, ",") {
				if m = strings.TrimSpace(m); m == "" {
					continue
				}
				a, herr := r.resolveHandle(ctx, m)
				if herr != nil {
					return herr
				}
				ids = append(ids, a.ID)
			}
			t, err := r.client.Team.Query().Where(entTeam.ProjectID(r.project), entTeam.Handle(handle)).First(ctx)
			switch {
			case err == nil:
				up := t.Update().SetMemberActorIds(ids).SetActive(true)
				if name != "" {
					up.SetDisplayName(name)
				}
				if t, err = up.Save(ctx); err != nil {
					return errcodes.New(errcodes.Internal, "update the team").WithCause(err)
				}
			case ent.IsNotFound(err):
				cr := r.client.Team.Create().SetProjectID(r.project).SetHandle(handle).SetMemberActorIds(ids).SetActive(true)
				if name != "" {
					cr.SetDisplayName(name)
				}
				if t, err = cr.Save(ctx); err != nil {
					return errcodes.New(errcodes.Internal, "write the team").WithCause(err)
				}
			default:
				return errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
			}
			fmt.Printf("✓ %s @%s (%d member(s)) in %s\n", t.ID, handle, len(ids), redactIfQuiet(r.where))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, flagRosterName, "", "display name")
	cmd.Flags().StringVar(&members, flagRosterMembers, "", "comma-separated @handles")
	return cmd
}

func newTeamListCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the roster's teams",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cr, err := openRosterOnCore(ctx)
			if err != nil {
				return err
			}
			if cr != nil {
				return cr.teamList(ctx)
			}
			r, err := openRoster(ctx, &f)
			if err != nil {
				return err
			}
			defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
			ts, err := r.client.Team.Query().Where(entTeam.Active(true)).Order(ent.Asc(entTeam.FieldHandle)).All(ctx)
			if err != nil {
				return errcodes.New(errcodes.Internal, "read the roster").WithCause(err)
			}
			for _, t := range ts {
				name := ""
				if t.DisplayName != nil {
					name = *t.DisplayName
				}
				fmt.Printf("@%-16s %-24s %d member(s)\n", t.Handle, name, len(t.MemberActorIds))
			}
			if len(ts) == 0 {
				fmt.Println("no teams on " + redactIfQuiet(r.where) + "'s roster")
			}
			return nil
		},
	}
	return cmd
}
