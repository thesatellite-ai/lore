// task_list_team.go — the coordination half of `lore task list` (LORE_TEAM_
// SPEC §3.5 "Every view combines the row and the signal", §1 flags
// --claimed-by, --mine, --all-repos): each task shows who works on it right
// now (from the signals ref) next to its row state.
package main

import (
	"context"
	"fmt"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/schema"
)

const (
	flagClaimedBy = "claimed-by"
	flagMine      = "mine"
)

// fieldActive is the roster rows' active flag.
const fieldActive = "active"

// taskListTeamFlags are the team filters of `task list`.
type taskListTeamFlags struct {
	claimedBy string
	mine      bool
	allRepos  bool
}

func bindTaskListTeamFlags(cmd *cobra.Command, f *taskListTeamFlags) {
	cmd.Flags().StringVar(&f.claimedBy, flagClaimedBy, "", "only tasks claimed right now by @handle")
	cmd.Flags().BoolVar(&f.mine, flagMine, false, "assigned to me or my team, and unclaimed or claimed by me")
	cmd.Flags().BoolVar(&f.allRepos, constants.FlagAllRepos, false, "every repo of the project (the default for team views)")
}

// boardReader is the one signals read the team filters need: the last
// fetched copy of the claims board (signals.Store.Local; a seam for tests).
type boardReader interface {
	Local(ctx context.Context) (signals.View, error)
}

// filterTeamRows applies --claimed-by / --mine to rows using the claims
// board of folder as read by rd, for actorID (this clone's actor). A board
// that cannot be read is an error whenever a filter is set (a filter it
// cannot answer is never shown as every row matching); without filters
// the rows are shown without coordination state.
//
// The second parameter is unused (teams are read through the core); it
// stays for the existing test seam.
func filterTeamRows(ctx context.Context, _ *ent.Client, f *taskListTeamFlags, rows []*ent.Task, rd boardReader, folder, actorID string) ([]*ent.Task, map[string]string, error) {
	// The command's own live fetch (F3) already brought refs/lore/* — the
	// signals ref included — into the tracking refs: read that copy, no
	// second round trip. It also records what this clone saw (expiry).
	v, err := rd.Local(ctx)
	if err != nil {
		// A filter cannot be answered without the board: refuse rather
		// than show every row as if it matched.
		if f.claimedBy != "" || f.mine {
			return nil, nil, coordError(fmt.Errorf("read the signals ref: %w", err))
		}
		return rows, nil, nil
	}
	var holder string
	if f.claimedBy != "" {
		if holder, err = resolveHandleActor(ctx, f.claimedBy); err != nil {
			return nil, nil, err
		}
	}
	myTeams := map[string]bool{}
	if f.mine {
		teams, err := coreDocs(ctx, tableTeams)
		if err != nil {
			return nil, nil, err
		}
		for _, tm := range teams {
			if !docBool(tm, fieldActive) {
				continue
			}
			for _, m := range docStrs(tm, schema.FieldTeamMembers) {
				if m == actorID {
					myTeams[tm.Str(docKeyID)] = true
				}
			}
		}
	}
	states := map[string]string{}
	var out []*ent.Task
	for _, t := range rows {
		p := signals.ClaimPath(folder, t.ID)
		sig, claimed := v.Board[p]
		_, active := v.Active(p)
		if holder != "" && !(claimed && active && sig.Holder == holder) {
			continue
		}
		if f.mine {
			assigned := (t.AssignedToActorID != nil && *t.AssignedToActorID == actorID) || (t.AssignedTeam != nil && myTeams[*t.AssignedTeam])
			free := !claimed || !active || sig.Holder == actorID
			if !assigned || !free {
				continue
			}
		}
		states[t.ID] = taskState(t, sig, claimed, active, v.Now)
		out = append(out, t)
	}
	return out, states, nil
}

// taskState is the §3.5 STATE column.
func taskState(t *ent.Task, sig signals.Signal, claimed, active bool, now time.Time) string {
	switch {
	case t.Status == entTask.StatusDone && t.MergedAt != nil:
		return "✓ done · merged " + t.MergedAt.UTC().Format("15:04")
	case t.Status == entTask.StatusDone && (t.DonePr != nil || t.DoneCommit != nil):
		return "✓ done · not merged yet"
	case t.Status == entTask.StatusDone:
		return ""
	case claimed && active:
		return fmt.Sprintf("▶ working: %s (until %s)", holderLabel(sig), sig.Expires.UTC().Format("15:04"))
	case claimed:
		return fmt.Sprintf("▶ EXPIRED %s ago (%s)", agoString(now.Sub(sig.Expires)), holderLabel(sig))
	case t.Gate != nil:
		return "⏸ gate: " + gateWord(t)
	}
	return ""
}

func gateWord(t *ent.Task) string {
	d := coord.Doc{"gate": string(*t.Gate), "title": t.Title, "body": deref(t.Body), "approved_hash": deref(t.ApprovedHash)}
	return coord.GateState(d)
}

// agoString renders a duration as "4 min" / "2 h".
func agoString(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}

// flagSignals is `task show --signals`.
const flagSignals = "signals"

// printTaskSignals prints the task's claim as this reader sees it now
// (F7 Debug: holder, token prefix, expires, renewed_seq).
func printTaskSignals(ctx context.Context, id string) error {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return err
	}
	return printClaimOn(ctx, st, w.Folder, id)
}

// printClaimOn prints the claim on task id in folder of the signals ref st
// as this clone sees it now (claimText).
func printClaimOn(ctx context.Context, st *signals.Store, folder, id string) error {
	txt, err := claimText(ctx, st, folder, id)
	if err != nil {
		return err
	}
	fmt.Print(txt)
	return nil
}
