// task_list_team.go — the coordination half of `lore task list` (LORE_TEAM_
// SPEC §3.5 "Every view combines the row and the signal", §1 flags
// --claimed-by, --mine, --all-repos): each task shows who works on it right
// now (from the signals ref) next to its row state.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"encoding/json"

	"dbent/gen/ent"
	entComment "dbent/gen/ent/comment"
	entTask "dbent/gen/ent/task"
	entTeam "dbent/gen/ent/team"
	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

const (
	flagClaimedBy = "claimed-by"
	flagMine      = "mine"
)

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

// applyTaskListTeam filters rows by the team flags and returns each task's
// coordination state. Without a git repo or signals (offline: the last
// fetched copy) it shows rows only.
func applyTaskListTeam(ctx context.Context, client *ent.Client, f *taskListTeamFlags, rows []*ent.Task) ([]*ent.Task, map[string]string, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		if f.claimedBy != "" || f.mine {
			return nil, nil, err
		}
		return rows, nil, nil
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		if f.claimedBy != "" || f.mine {
			return nil, nil, err
		}
		return rows, nil, nil
	}
	return filterTeamRows(ctx, client, f, rows, st, w.Folder, w.ActorID)
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
func filterTeamRows(ctx context.Context, client *ent.Client, f *taskListTeamFlags, rows []*ent.Task, rd boardReader, folder, actorID string) ([]*ent.Task, map[string]string, error) {
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
		teams, err := client.Team.Query().Where(entTeam.Active(true)).All(ctx)
		if err != nil {
			return nil, nil, errcodes.New(errcodes.Internal, "read teams for --mine").WithCause(err)
		}
		for _, tm := range teams {
			for _, m := range tm.MemberActorIds {
				if m == actorID {
					myTeams[tm.ID] = true
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

// actorName names an actor for `task show`: handle, label, display name —
// from lore.db, else from the fetched default branch (a projection that
// landed after this checkout was last updated).
func actorName(ctx context.Context, client *ent.Client, id string) string {
	a, err := client.Actor.Get(ctx, id)
	if err != nil {
		if d, ok := defaultBranchDoc(ctx, tableActors, id); ok {
			if l := d.Str("label"); l != "" {
				return l
			}
			if n := d.Str("display_name"); n != "" {
				return n
			}
		}
		return id
	}
	switch {
	case a.Handle != nil && *a.Handle != "":
		return "@" + *a.Handle
	case a.Label != nil && *a.Label != "":
		return *a.Label
	}
	return a.DisplayName
}

// defaultBranchDoc reads a row of this project from the fetched default
// branch (its durable home, fixed rule 1).
func defaultBranchDoc(ctx context.Context, table, id string) (coord.Doc, bool) {
	root := liveProjectRoot(mustGetwd())
	s := liveFor(ctx, root)
	if root == "" || s == nil {
		return nil, false
	}
	target := "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx)
	b, ok, err := s.ref.Repo.ReadFile(ctx, target, mainDataPath(s.folderFor(root), table+"/"+id+".json"))
	if err != nil || !ok {
		return nil, false
	}
	d, err := decodeDoc(b)
	return d, err == nil
}

func mustGetwd() string {
	d, _ := os.Getwd()
	return d
}

// newerTask returns the default branch's copy of t when it is newer than
// the local one (a landed row that left live while this checkout's branch
// data is older; same rule as the coordination reads, tie → main).
func newerTask(ctx context.Context, t *ent.Task) *ent.Task {
	d, ok := defaultBranchDoc(ctx, tableTasks, t.ID)
	if !ok {
		return t
	}
	mt, err := taskFromDoc(d)
	if err != nil || mt.UpdatedAt.Before(t.UpdatedAt) {
		return t
	}
	return mt
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
// as this clone sees it now.
func printClaimOn(ctx context.Context, st *signals.Store, folder, id string) error {
	v, err := st.Read(ctx)
	if err != nil {
		return coordError(err)
	}
	p := signals.ClaimPath(folder, id)
	s, ok := v.Board[p]
	fmt.Println()
	if !ok {
		fmt.Printf("  claim:    none on %s\n", st.Ref)
		return nil
	}
	_, active := v.Active(p)
	state := "active"
	if !active {
		state = "expired (as seen by this clone)"
	}
	fmt.Printf("  claim:    %s, held by %s (session %s", state, holderLabel(s), s.Session)
	if s.Host != "" {
		fmt.Printf(", %s", s.Host)
	}
	fmt.Printf(")\n  token:    %s…  epoch %d\n  expires:  %s  renewed_seq %d\n", s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], s.ClaimEpoch,
		s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq)
	if s.WorkBranch != "" {
		fmt.Printf("  branch:   %s\n", s.WorkBranch)
	}
	if len(s.Owns) > 0 {
		fmt.Printf("  owns:     %s\n", strings.Join(s.Owns, ", "))
	}
	return nil
}

// printTaskNotes lists the task's notes (comments), oldest first, so a
// replacement agent reads where its predecessor stopped (§7.5 step 5) —
// this repo's and, with their label, internal ones pointing at it from a
// store (F15: pointers flow inward and are shown to the team).
//
// A read that fails is said on stderr (as list commands report external
// rows they could not read), never shown as "no notes": an agent taking
// over must know its predecessor's notes may be missing.
func printTaskNotes(ctx context.Context, client *ent.Client, id string) {
	cs, err := client.Comment.Query().Where(entComment.EntityTable(tableTasks), entComment.EntityID(id)).
		Order(ent.Asc(entComment.FieldCreatedAt)).All(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Muted("lore: could not read the notes of "+id+": "+err.Error()))
	}
	external := externalCommentsOn(ctx, id)
	if len(cs) == 0 && len(external) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("  notes:")
	for _, c := range cs {
		fmt.Printf("    %s  %s\n", c.CreatedAt.UTC().Format("2006-01-02 15:04"), c.Body)
	}
	for _, line := range external {
		fmt.Println("    " + line)
	}
}

// externalCommentsOn returns "body  [label]" for every store or personal
// comment whose target is task id.
// Problems reading the external rows are printed on stderr.
func externalCommentsOn(ctx context.Context, id string) []string {
	rows, notes := externalRowsHere(ctx, cachedb.Query{Table: tableComments})
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, style.Muted("lore: "+n))
	}
	var out []string
	for _, r := range rows {
		var doc map[string]any
		if err := json.Unmarshal(r.Doc, &doc); err != nil {
			fmt.Fprintln(os.Stderr, style.Muted("lore: skipped an unreadable note "+r.ID+" "+r.Label+": "+err.Error()))
			continue
		}
		if doc["entity_id"] != id {
			continue
		}
		out = append(out, r.Body+"  "+r.Label)
	}
	return out
}
