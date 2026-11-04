// coord_cmd.go — the new top-level coordination commands of lore for teams
// (LORE_TEAM_SPEC §1 "Commands", §7): `lore agent start`, `lore hold
// claim|release` (v0.1.17's `lore resource claim|release`, renamed for good
// by RQ11; `lore resource` now names resource kinds) and more. addCoordCommands is ONE line in main.go, so
// adding a P4 command never touches the shared root file again.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// addCoordCommands registers every top-level command this file family adds
// and `apply` under the existing `plan` group. It runs after the generated
// entity groups are registered, so the group exists.
func addCoordCommands(root *cobra.Command) {
	root.AddCommand(newAgentCommand(), newHoldCommand(), newGateCommand(), newFindingCommand(), newInboxCommand())
}

// attachToGroup adds sub under the existing top-level group name (one
// group per name in `lore --help`), replacing a same-named generated
// subcommand; it creates the group when none exists.
func attachToGroup(root *cobra.Command, name, short string, sub *cobra.Command) {
	for _, c := range root.Commands() {
		if c.Name() != name {
			continue
		}
		for _, old := range c.Commands() {
			if old.Name() == sub.Name() {
				c.RemoveCommand(old)
			}
		}
		c.AddCommand(sub)
		return
	}
	g := &cobra.Command{Use: name, Short: short}
	g.AddCommand(sub)
	root.AddCommand(g)
}

// ---------------------------------------------------------------- agent

// sessionIDBytes of randomness make a session id ("s-" + hex) unique per
// agent start on any machine.
const (
	sessionPrefix  = "s-"
	sessionIDBytes = 4
	flagLabel      = "label"
	flagOwner      = "owner"
)

func newAgentCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Agent sessions (one actor per session label)"}
	var label, owner string
	start := &cobra.Command{
		Use:   "start",
		Short: "Register an agent actor for this session and print its environment",
		Long: `Start registers an agent actor (kind agent, the label, owned by a person) on the
org roster and prints LORE_ACTOR and LORE_SESSION for the agent's shell. Every
claim, heartbeat and done by the agent then carries that actor and session (F18).

Example:
  eval "$(lore agent start --label agent-frontend --owner @alice | tail -1)"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateAgentLabel(label); err != nil {
				return errcodes.New(errcodes.InvalidInput, err.Error())
			}
			ctx := cmd.Context()
			w, err := openCoordWorld(ctx)
			if err != nil {
				return err
			}
			if w.IsAgent() {
				return errcodes.New(errcodes.InvalidInput, "an agent cannot start another agent; run `lore agent start` as its owner")
			}
			key := identity.KindAgent + ":" + label
			actorID, err := ids.Deterministic(ids.PrefixActor, key)
			if err != nil {
				return errcodes.New(errcodes.Internal, "derive the agent's actor id").WithCause(err)
			}
			session, err := newSessionID()
			if err != nil {
				return errcodes.New(errcodes.Internal, "make a session id").WithCause(err)
			}
			ownerShown := owner
			if ownerShown == "" {
				ownerShown = w.Caller().Label
			}
			pushed, err := registerAgent(cmd, w, agentSpec{ActorID: actorID, Key: key, Label: label, Owner: owner})
			if err != nil {
				return err
			}
			where := "registered"
			if pushed != "" {
				where = "registered in " + pushed
			}
			fmt.Printf("%s %s agent %s (owner %s) %s; LORE_ACTOR=%s LORE_SESSION=%s\n",
				style.Success("✓"), actorID, label, ownerShown, where, key, session)
			fmt.Printf("export %s=%s %s=%s\n", identity.EnvActor, key, identity.EnvSession, session)
			return nil
		},
	}
	start.Flags().StringVar(&label, flagLabel, "", "the agent's label (one actor per label), e.g. agent-frontend")
	start.Flags().StringVar(&owner, flagOwner, "", "the owning person (@handle); default: you")
	_ = start.MarkFlagRequired(flagLabel)
	cmd.AddCommand(start)
	return cmd
}

func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return sessionPrefix + hex.EncodeToString(b), nil
}

// agentSpec is an agent actor to put on the roster.
type agentSpec struct {
	ActorID, Key, Label, Owner string
}

// registerAgent writes the agent actor to the org roster (in the org
// store; the repo's own actors table when no org applies) through the
// stores work's roster writer, which also checks the owner is a person on
// the roster. It returns where it registered.
func registerAgent(cmd *cobra.Command, _ *coordWorld, a agentSpec) (string, error) {
	id, where, err := rosterRegisterAgent(cmd.Context(), a.Label, a.Owner)
	if err != nil {
		return "", err
	}
	if id != a.ActorID {
		return "", errcodes.New(errcodes.Internal, fmt.Sprintf("roster gave agent id %s, expected %s from its stable key", id, a.ActorID))
	}
	return where, nil
}

// ---------------------------------------------------------------- hold

// Hold places (§7.2; CLI_REFERENCE 3.3 --on).
const (
	scopeProject = "project"
	scopeMachine = "machine"
	flagTTL      = "ttl"
	flagOn       = "on"
	// defaultHoldTTL is a hold's length when --ttl is not given:
	// the claim lease, so one heartbeat interval keeps both alive.
	defaultHoldTTL = 0
)

// newHoldCommand is `lore hold claim|release` (RQ11: no alias; `--scope` is
// an unknown flag here, `--on` replaces it, XQ6).
func newHoldCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "hold", Short: "Hold a shared resource (a test suite, a staging slot) with a lease"}
	var ttl time.Duration
	var scope string
	claim := &cobra.Command{
		Use:   "claim <name>",
		Short: "Hold a resource exclusively (same lease, token and renew as a task claim)",
		Long: `Claim holds a named resource on the project's signals ref, exclusively, until --ttl
passes or you release it; running claim again renews your hold. Fails closed (exit
15, E_OFFLINE) when the push cannot be confirmed.

Example:
  lore hold claim full-suite --ttl 30m && task test && lore hold release full-suite`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scope == scopeMachine {
				return errcodes.New(errcodes.NotImplemented, "--on machine defers to a machine-wide lock; run the command under your machine's own lock tool (for example flock) instead")
			}
			ctx := cmd.Context()
			w, err := openCoordWorld(ctx)
			if err != nil {
				return err
			}
			if err := w.requireActor(); err != nil {
				return err
			}
			st, err := w.repoSignalsChecked()
			if err != nil {
				return err
			}
			d := ttl
			if d <= defaultHoldTTL {
				d = w.Lease.TTL
			}
			var held signals.Signal
			_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
				tok, err := st.Tokens.New()
				if err != nil {
					return signals.Change{}, err
				}
				s, err := signals.DecideHold(v, signals.HoldRequest{Caller: w.Caller(), Folder: w.Folder, Name: args[0], TTL: d, Token: tok})
				if err != nil {
					return signals.Change{}, err
				}
				held = s
				return signals.Change{Put: map[string]signals.Signal{signals.ResourcePath(w.Folder, args[0]): s}}, nil
			})
			if err != nil {
				return coordError(err)
			}
			fmt.Printf("%s holding %s until %s\n", style.Success("✓"), args[0], held.Expires.UTC().Format(time.RFC3339))
			return nil
		},
	}
	claim.Flags().DurationVar(&ttl, flagTTL, 0, "how long the hold lasts (default: lease.ttl)")
	claim.Flags().StringVar(&scope, flagOn, scopeProject, "project (the project's signals ref) or machine")
	release := &cobra.Command{
		Use:   "release <name>",
		Short: "Release a resource you hold",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w, err := openCoordWorld(ctx)
			if err != nil {
				return err
			}
			if err := w.requireActor(); err != nil {
				return err
			}
			st, err := w.repoSignalsChecked()
			if err != nil {
				return err
			}
			_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
				p, err := signals.DecideUnhold(v, w.Caller(), w.Folder, args[0])
				return signals.Change{Delete: []string{p}}, err
			})
			if err != nil {
				return coordError(err)
			}
			fmt.Printf("%s released %s\n", style.Success("✓"), args[0])
			return nil
		},
	}
	cmd.AddCommand(claim, release)
	return cmd
}

// agentLabelPattern is what a label may hold: it becomes part of the
// stable key (agent:<label>), a session's display name and a path segment,
// so it is kept to a safe, case-folded alphabet.
var agentLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// validateAgentLabel refuses labels outside agentLabelPattern.
func validateAgentLabel(label string) error {
	if !agentLabelPattern.MatchString(label) {
		return fmt.Errorf("agent label %q: use lower-case letters, digits, '.', '_' or '-' (max 63, starting with a letter or digit)", label)
	}
	return nil
}
