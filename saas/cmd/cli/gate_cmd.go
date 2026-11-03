// gate_cmd.go — `lore gate request|approve` (LORE_TEAM_SPEC §7.4 "Gates",
// TEAM-028): a gate stops agents until a person approves; only a human
// from the identity file or config approves (never LORE_ACTOR), a mission's
// approvers list is honoured, and approved_hash covers title + body so an
// edit voids the approval.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// flagApprovers is `mission add --approvers`.
const flagApprovers = "approvers"

// gateHashShown is how much of approved_hash messages print.
const gateHashShown = 8

// resolveApprovers turns @handles into actor ids.
func resolveApprovers(ctx context.Context, handles []string) ([]string, error) {
	out := make([]string, 0, len(handles))
	for _, h := range handles {
		id, err := resolveHandleActor(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func newGateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "gate", Short: "Human gates: stop agents until a person approves"}
	var reqFrom, apFrom string
	req := &cobra.Command{
		Use:   "request <id>",
		Short: "Put a gate on a task (agents stop at it until a person approves)",
		Long: `Request sets the task's gate to requested. Run by the agent holding the task, it is
fenced like any holder write (exit 12–14 when the claim is gone).

Example:
  lore gate request tsk_33`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runGateRequest(cmd.Context(), args[0], reqFrom) },
	}
	ap := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a gate (a person on the roster, from the identity file, never an agent)",
		Long: `Approve records approved_by, approved_at and a hash of title + body: editing either
voids the approval. Only a human identity from ~/.lore/config.yaml (or the identity
file) approves — never an agent, never LORE_ACTOR — and only a listed approver when
the mission names approvers. Fails closed (exit 15) when the push is not confirmed.

Example:
  lore gate approve tsk_33`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runGateApprove(cmd.Context(), args[0], apFrom) },
	}
	bindFromFlag(req, &reqFrom)
	bindFromFlag(ap, &apFrom)
	cmd.AddCommand(req, ap)
	return cmd
}

func runGateRequest(ctx context.Context, id, from string) error {
	w, t, err := openTaskCoord(ctx, id, from)
	if err == nil {
		defer t.Close()
		cs, err := readClaim(ctx, w, t, id)
		if err != nil {
			return coordError(err)
		}
		if cs.mine || cs.hadClaim {
			if _, err := coord.HolderWrite(ctx, w.env(t), id, false, coord.RequestGate); err != nil {
				return coordError(err)
			}
			fmt.Printf("%s gate requested on %s; agents stop until a person runs `lore gate approve %s`\n", style.Success("✓"), id, id)
			return nil
		}
		if isStoreTarget(t) {
			if _, err := coord.Update(ctx, w.env(t), id, func(d coord.Doc, now time.Time) error {
				_, err := coord.RequestGate(d, now)
				return err
			}); err != nil {
				return coordError(err)
			}
			fmt.Printf("%s gate requested on %s; agents stop until a person runs `lore gate approve %s`\n", style.Success("✓"), id, id)
			return nil
		}
	}
	var f commonFlags
	rctx, client, err := resolveContext(&f)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	task, err := lookupTask(ctx, client, projectID, id)
	if err != nil {
		return err
	}
	if _, err := client.Task.UpdateOne(task).SetGate(entTask.GateRequested).ClearApprovedBy().ClearApprovedAt().ClearApprovedHash().Save(ctx); err != nil {
		return errcodes.New(errcodes.Internal, "request gate").WithCause(err)
	}
	fmt.Printf("%s gate requested on %s; agents stop until a person runs `lore gate approve %s`\n", style.Success("✓"), id, id)
	return nil
}

func runGateApprove(ctx context.Context, id, from string) error {
	if v := strings.TrimSpace(os.Getenv(identity.EnvActor)); v != "" {
		return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("approvals come from your identity file, never from %s (%s is set here)", identity.EnvActor, identity.EnvActor)).
			WithHint("an agent session cannot approve; run `lore gate approve` in your own shell")
	}
	w, t, err := openTaskCoord(ctx, id, from)
	if err != nil {
		return err
	}
	defer t.Close()
	if w.Ident.Kind != identity.KindHuman || w.Ident.Step == identity.StepEnv || w.Ident.Step == identity.StepFlag {
		return errcodes.New(errcodes.InvalidInput, "only a person approves a gate (TEAM-028); you are "+w.Ident.StableKey).
			WithHint("set your identity with `lore identity set` and approve from your own shell")
	}
	if err := checkApprover(ctx, id, w.ActorID); err != nil {
		return err
	}
	ap, err := coord.Approve(ctx, w.env(t), coord.ApproveRequest{ID: id, ApproverID: w.ActorID})
	if err != nil {
		return coordError(err)
	}
	fmt.Printf("%s %s approved by %s (%s), hash %s…\n", style.Success("✓"), id, w.Ident.DisplayName, w.Ident.Step.String(), ap.Hash[:gateHashShown])
	return nil
}

// checkApprover enforces the mission's approvers list, when it has one.
func checkApprover(ctx context.Context, id, actorID string) error {
	var f commonFlags
	_, client, err := resolveContext(&f)
	if err != nil {
		return err
	}
	defer client.Close()
	task, err := client.Task.Get(ctx, id)
	if err != nil || task.MissionID == nil {
		return nil // the row is only in live or has no mission: no list to check
	}
	m, err := client.Mission.Get(ctx, *task.MissionID)
	if err != nil || len(m.Approvers) == 0 {
		return nil
	}
	if !slices.Contains(m.Approvers, actorID) {
		return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("mission %s names its approvers; you are not one of them", m.ID)).
			WithHint("ask one of: " + strings.Join(m.Approvers, ", "))
	}
	return nil
}
