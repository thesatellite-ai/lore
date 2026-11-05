// task_team_core.go — the CLI's opstask.Team (ops/internal/work/team.go, exported as ops/task.Team): the
// roster and the signals ref behind v0.1.17's team flags on the core's
// task and mission commands (`task add|edit --assign @h`, `task list
// --mine|--claimed-by`, `mission add --approvers @h`). The ops ask; this
// file answers from the same machinery the coordination verbs use
// (resolveHandleActor, the core roster, the repo's signals ref).
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// cliTeam is the opstask.Team of one CLI command.
type cliTeam struct{}

var _ opstask.Team = cliTeam{}

// ResolveHandle implements opstask.Team: a person on the roster (the core
// roster of a 0.1.18 org, else the v0.1.17 one), else, when allowTeam, an
// active team of that handle. A miss keeps the roster's hint (a stale
// store copy, or nobody registered yet).
func (cliTeam) ResolveHandle(ctx context.Context, handle string, allowTeam bool) (opstask.Assignee, error) {
	id, actorErr := resolveHandleActor(ctx, handle)
	if actorErr == nil {
		return opstask.Assignee{ActorID: id}, nil
	}
	if !allowTeam {
		return opstask.Assignee{}, coreErr(actorErr)
	}
	h := strings.TrimPrefix(strings.TrimSpace(handle), handlePrefix)
	tm, err := teamByHandle(ctx, h)
	if err != nil {
		return opstask.Assignee{}, coreErr(err)
	}
	if tm != "" {
		return opstask.Assignee{TeamID: tm}, nil
	}
	hint := "people join with `lore actor register`; teams with `lore team add`"
	var miss *errcodes.CLIError
	if errors.As(actorErr, &miss) && miss.Hint == rosterMissHint {
		hint = rosterMissHint
	}
	return opstask.Assignee{}, errs.New(errs.NotFound, "no person or team with handle @"+h).WithHint(hint)
}

// teamByHandle returns the id of the active team @h ("" = none): the core
// roster of a 0.1.18 org, else the teams rows the core reads (the repo).
func teamByHandle(ctx context.Context, h string) (string, error) {
	cr, err := openCoreRoster(ctx)
	if err != nil {
		return "", err
	}
	if cr != nil {
		teams, terr := cr.app.RosterTeams(ctx, cr.target)
		if terr != nil {
			return "", coreErr(terr)
		}
		for _, t := range teams {
			if t.Active && t.Handle == h {
				return t.ID, nil
			}
		}
		return "", nil
	}
	docs, err := coreDocs(ctx, schema.TableTeams)
	if err != nil {
		return "", err
	}
	for _, d := range docs {
		if docBool(d, fieldActive) && d.Str(fieldHandle) == h {
			return d.Str(docKeyID), nil
		}
	}
	return "", nil
}

// Claims implements opstask.Team: the last fetched copy of this repo's claims
// board (the command's own live fetch brought it in), as v0.1.17's task
// list read it. No git repo or no signals ref is an error: only a filter
// asks, and it must never pass every row.
func (cliTeam) Claims(ctx context.Context) (opstask.ClaimBoard, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return nil, coreErr(err)
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return nil, coreErr(err)
	}
	v, err := st.Local(ctx)
	if err != nil {
		return nil, coreErr(coordError(fmt.Errorf("read the signals ref: %w", err)))
	}
	return claimBoard{view: v, folder: w.Folder, me: w.ActorID}, nil
}

// claimBoard is an opstask.ClaimBoard over one signals view.
type claimBoard struct {
	view   signals.View
	folder string
	me     string
}

// Me implements opstask.ClaimBoard.
func (b claimBoard) Me() string { return b.me }

// State implements opstask.ClaimBoard: v0.1.17's taskState (§3.5 STATE).
func (b claimBoard) State(t opstask.View) string {
	p := signals.ClaimPath(b.folder, t.ID)
	sig, claimed := b.view.Board[p]
	_, active := b.view.Active(p)
	switch {
	case t.Status == schema.TaskStatusDone && !t.MergedAt.IsZero():
		return "✓ done · merged " + t.MergedAt.UTC().Format(clockLayout)
	case t.Status == schema.TaskStatusDone && (t.DonePR != "" || t.DoneCommit != ""):
		return "✓ done · not merged yet"
	case t.Status == schema.TaskStatusDone:
		return ""
	case claimed && active:
		return fmt.Sprintf("▶ working: %s (until %s)", holderLabel(sig), sig.Expires.UTC().Format(clockLayout))
	case claimed:
		return fmt.Sprintf("▶ EXPIRED %s ago (%s)", agoString(b.view.Now.Sub(sig.Expires)), holderLabel(sig))
	case t.Gate != "":
		return "⏸ gate: " + coord.GateState(coord.Doc{"gate": t.Gate, "title": t.Title, "body": t.Body, "approved_hash": t.ApprovedHash})
	}
	return ""
}

// clockLayout is the STATE column's time of day.
const clockLayout = "15:04"

// Claim implements opstask.ClaimBoard.
func (b claimBoard) Claim(taskID string) (string, bool, bool) {
	p := signals.ClaimPath(b.folder, taskID)
	sig, claimed := b.view.Board[p]
	_, active := b.view.Active(p)
	return sig.Holder, claimed, active
}

// OpenClaim implements opstask.ClaimSpanner (TIME_TRACKING 5.1 rule 1):
// the claim on taskID counted live by task time show, ending now while
// the lease lives, else at the last heartbeat (D3).
func (b claimBoard) OpenClaim(taskID string) (opstask.OpenClaim, bool) {
	p := signals.ClaimPath(b.folder, taskID)
	sig, ok := b.view.Board[p]
	if !ok || sig.Kind != signals.KindClaim || sig.Since.IsZero() {
		return opstask.OpenClaim{}, false
	}
	_, active := b.view.Active(p)
	end := b.view.Now
	if !active {
		end, _ = sig.LastHeartbeat(b.view.Lease.TTL)
	}
	return opstask.OpenClaim{Holder: sig.Holder, Session: sig.Session, Since: sig.Since, End: end, Expired: !active}, true
}

// teamCommandGroups are the core noun groups whose operations take team
// flags (task, mission; plan apply creates missions with approvers).
var teamCommandGroups = []string{"task", "mission", "plan"}

// withCLITeam gives every operation command under the team groups the
// CLI's opstask.Team in its context.
func withCLITeam(root *cobra.Command) {
	for _, g := range teamCommandGroups {
		grp, _, err := root.Find([]string{g})
		if err != nil || grp.Name() != g {
			continue
		}
		withTeamUnder(grp)
	}
}

// withTeamUnder wraps every runnable command under c, sub-groups included
// (`task time show` sits in the time sub-group and reads open claims).
func withTeamUnder(c *cobra.Command) {
	for _, sub := range c.Commands() {
		withTeamUnder(sub)
		if sub.RunE == nil {
			continue
		}
		run := sub.RunE
		sub.RunE = func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(opstask.WithTeam(cmd.Context(), cliTeam{}))
			return run(cmd, args)
		}
	}
}
