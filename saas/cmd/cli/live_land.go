// live_land.go — landing (LORE_TEAM_SPEC.md F5): finished rows and max-age
// copies are written to main's .lore/data by a commit built on the fetched
// default branch and pushed as <sha>:refs/heads/<main>; rows main holds
// leave live. Runs after any command that wrote live rows, on `lore sync`,
// and on `lore land`.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	entDecision "dbent/gen/ent/decision"
	entMission "dbent/gen/ent/mission"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// statusField is the column holding a row's lifecycle state.
const statusField = "status"

// liveFinishedStatuses lists, per kind, the statuses that count as
// finished (rule 6a). Kinds not listed have no lifecycle (rules, memories,
// comments, notes, actors, task lists, …) and are finished on every write
// (TEAM-027): each live write lands a copy at once.
//
// decisions: the spec says superseded/rejected; the decision enum has no
// "rejected", and "deprecated" is its closest state (dbent/schema/enums.go).
var liveFinishedStatuses = map[string]map[string]bool{
	tableTasks:     {string(entTask.StatusDone): true, string(entTask.StatusCancelled): true},
	tableMissions:  {string(entMission.StatusDone): true, string(entMission.StatusCancelled): true},
	tableDecisions: {string(entDecision.StatusSuperseded): true, string(entDecision.StatusDeprecated): true},
	tableHandoffs:  {handoffAccepted: true, handoffDeclined: true},
}

// liveFinished applies liveFinishedStatuses to a row body.
func liveFinished(table string, body map[string]any) bool {
	states, hasLifecycle := liveFinishedStatuses[table]
	if !hasLifecycle {
		return true
	}
	s, _ := body[statusField].(string)
	return states[s]
}

// liveLayout is lore's branch layout of a live folder (§3.6).
var liveLayout = live.Layout{DataPath: mainDataPath, AddPurged: lsync.AddPurged}

// landSelect picks the rows `lore land` names; nil = no row is named (an
// automatic pass).
type landSelect func(k live.RowKey, f live.File) bool

// landPlanRow is one row of a landing plan.
type landPlanRow struct {
	Key      live.RowKey
	File     live.File
	Decision live.LandDecision
	Leave    bool
}

// mainView reads the fetched default branch's copy of rows.
type mainView struct {
	s      *liveSession
	ref    string
	folder string
	purged map[string]bool
}

func (s *liveSession) mainView(ctx context.Context, folder string) *mainView {
	v := &mainView{s: s, ref: "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx), folder: folder, purged: map[string]bool{}}
	if b, ok, err := s.repo.ReadFile(ctx, v.ref, mainDataPath(folder, lsync.PurgedFileName)); err == nil && ok {
		if doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth); err == nil {
			list, _ := doc["ids"].([]any)
			for _, x := range list {
				if id, ok := x.(string); ok {
					v.purged[id] = true
				}
			}
		}
	}
	return v
}

func (v *mainView) body(ctx context.Context, k live.RowKey) []byte {
	b, ok, err := v.s.repo.ReadFile(ctx, v.ref, mainDataPath(v.folder, k.Table+"/"+k.ID+".json"))
	if err != nil || !ok {
		return nil
	}
	return b
}

// plan decides, for every live row of the folder, whether it leaves live
// and whether it lands (F5 Confirm + ShouldLand).
func (s *liveSession) plan(ctx context.Context, folder string, named landSelect) ([]landPlanRow, error) {
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		return nil, err
	}
	rows, _ := tree.Rows(folder)
	mv := s.mainView(ctx, folder)
	now := s.clk.Now()
	keys := make([]live.RowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	out := make([]landPlanRow, 0, len(keys))
	for _, k := range keys {
		f := rows[k]
		finished := f.Deleted() || liveFinished(k.Table, f.Body)
		mainBody := mv.body(ctx, k)
		isNamed := named != nil && named(k, f)
		p := landPlanRow{Key: k, File: f}
		p.Leave = !isNamed && live.Confirm(live.ConfirmInputs{
			File: f, Finished: finished, Now: now, MaxAge: s.cfg.MaxAge, MainBody: mainBody, MainPurged: mv.purged[k.ID],
		})
		p.Decision = live.ShouldLand(live.LandInputs{
			File: f, Finished: finished, Named: isNamed, Now: now, MaxAge: s.cfg.MaxAge, MainBody: mainBody, MainPurged: mv.purged[k.ID],
		})
		if named != nil && !isNamed {
			continue // `lore land <filter>`: rows outside the filter are not shown or touched
		}
		out = append(out, p)
	}
	return out, nil
}

// landOutcome is what one landing pass did, for the command's output.
type landOutcome struct {
	Landed    int
	Copied    int
	Left      int
	Status    live.LandStatus
	Commit    string
	Detail    string
	Conflicts []string
}

// landPass runs one landing pass for a folder: rows main already holds
// leave live (only when leave is set: commands that fetched), finished and
// named rows land, open rows at max age are copied.
func (s *liveSession) landPass(ctx context.Context, folder string, named landSelect, leave bool) (landOutcome, error) {
	var out landOutcome
	if s.blocked != nil {
		return out, s.blocked
	}
	plan, err := s.plan(ctx, folder, named)
	if err != nil {
		return out, err
	}
	removeIf := map[live.RowKey][]byte{}
	var items []live.LandItem
	for _, p := range plan {
		if p.Leave && leave {
			b, err := p.File.Encode()
			if err == nil {
				removeIf[p.Key] = b
			}
			continue
		}
		switch p.Decision.Action {
		case live.LandFinal, live.LandCopy:
			items = append(items, live.LandItem{Folder: folder, Key: p.Key, File: p.File, Action: p.Decision.Action})
		default:
			if named != nil && p.Decision.Action == live.LandStay && strings.HasPrefix(p.Decision.Reason, "conflict") {
				out.Conflicts = append(out.Conflicts, p.Key.ID)
			}
		}
	}
	items = withoutLoneSupporting(items)
	exact := map[live.RowKey]live.File{}
	if len(items) > 0 {
		docs := map[live.RowKey]live.File{}
		for _, it := range items {
			docs[it.Key] = it.File
		}
		// F16: a landing commit never passes a git hook; check it here.
		if err := leakCheckDocs(ctx, "landing", liveDocs(folder, docs)); err != nil {
			return out, err
		}
		res, err := s.ref.Land(ctx, live.LandRequest{Branch: s.defaultBranch(ctx), Items: items, Layout: liveLayout})
		if err != nil {
			return out, err
		}
		out.Status, out.Commit, out.Detail = res.Status, res.Commit, res.Detail
		switch res.Status {
		case live.LandDone, live.LandNothing:
			_ = writeLiveNote(s.stateDir, liveNoteProtected, "") // best effort: the note only feeds `lore live doctor`
			now := s.clk.Now()
			for _, it := range items {
				if it.Action == live.LandCopy {
					f := it.File
					f.Block.LastCopyAt = now.UTC()
					exact[it.Key] = f
					out.Copied++
				} else {
					out.Landed++
				}
			}
		case live.LandProtected:
			_ = writeLiveNote(s.stateDir, liveNoteProtected, res.Detail) // best effort: the note only feeds `lore live doctor`
		}
	}
	if len(removeIf)+len(exact) > 0 {
		res, err := s.ref.Write(ctx, live.WriteRequest{Folder: folder, Exact: exact, RemoveIf: removeIf, BinaryVersion: version})
		if err != nil {
			return out, err
		}
		for _, k := range res.Written {
			if _, left := removeIf[k]; left {
				out.Left++
			}
		}
	}
	return out, nil
}

// withoutLoneSupporting drops companion rows (supporting rows that went
// live only alongside a command's live rows, rowroute.DecideAll) when no
// other row lands with them: a plain `lore task add` must not move main
// (F2). A supporting row written live on purpose (`lore actor register`)
// is no companion and lands like any row finished on write.
func withoutLoneSupporting(items []live.LandItem) []live.LandItem {
	for _, it := range items {
		if !it.File.Block.Companion {
			return items
		}
	}
	return nil
}

// liveNoteProtected records that main refused a direct landing (the rows
// ride along, F6). Its text is the remote's message.
const liveNoteProtected = "land-protected"

// autoLand runs the landing pass at the end of a command that wrote live
// rows (or `lore sync`), and prints what happened.
func (s *liveSession) autoLand(ctx context.Context, folder string) {
	if s.blocked != nil || s.ref.Remote == nil || !s.autopush && liveCmdState.fetchKind() != live.FetchAlways {
		return
	}
	always := liveCmdState.fetchKind() == live.FetchAlways
	if !always && s.wrote == 0 {
		return
	}
	out, err := s.landPass(ctx, folder, nil, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Error(liveLogPrefix+"land: "+err.Error()))
		return
	}
	printLandOutcome(out)
}

func printLandOutcome(out landOutcome) {
	switch out.Status {
	case live.LandDone:
		msg := fmt.Sprintf("landed on main (%s)", shortSHA(out.Commit))
		if out.Copied > 0 {
			msg += fmt.Sprintf("; %d open row(s) copied, they stay live", out.Copied)
		}
		fmt.Fprintln(os.Stderr, style.Success(liveLogPrefix+msg+"; leaves live once main is fetched"))
	case live.LandProtected:
		fmt.Fprintln(os.Stderr, liveLogPrefix+"main is protected: the rows ride on your next push (lore: land N rows)")
	case live.LandOffline:
		fmt.Fprintln(os.Stderr, liveLogPrefix+"not landed yet (offline); the next command retries")
	case live.LandRetriesOut:
		fmt.Fprintln(os.Stderr, liveLogPrefix+"not landed yet (main kept moving); the next command retries")
	case live.LandRefused:
		fmt.Fprintln(os.Stderr, style.Warn(liveLogPrefix+"main refused the landing: "+out.Detail))
	}
	if out.Left > 0 {
		fmt.Fprintf(os.Stderr, "%s%d row(s) left live (main holds them)\n", liveLogPrefix, out.Left)
	}
}

// shortSHALen is how much of a commit id output shows.
const shortSHALen = 7

func shortSHA(s string) string {
	if len(s) > shortSHALen {
		return s[:shortSHALen]
	}
	return s
}

// ---------------------------------------------------------------- lore land

func newLandCommand() *cobra.Command {
	var all, dryRun bool
	var kind, mission string
	cmd := &cobra.Command{
		Use:   "land [id]",
		Short: "Write live rows to main now (finished rows land; open rows are copied and stay live)",
		Long: `Live rows land on main by themselves when they are finished, and open rows
are copied at live.max_age. land does it now for one row or a selection.
--dry-run prints the plan with the reason for every row.`,
		Example: `  lore land tsk_02
  lore land --mission mis_12 --dry-run
  lore land --all --kind rules`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all && kind == "" && mission == "" {
				return errcodes.New(errcodes.InvalidInput, "name a row id, or use --all, --kind or --mission")
			}
			s, folder, err := liveHere(cmd)
			if err != nil {
				return err
			}
			sel := landSelector(args, all, kind, mission)
			if dryRun {
				return s.printLandPlan(cmd.Context(), folder, sel)
			}
			out, err := s.landPass(cmd.Context(), folder, sel, false)
			if err != nil {
				return errcodes.New(errcodes.Internal, "land").WithCause(err)
			}
			if len(out.Conflicts) > 0 {
				return errcodes.New(errcodes.InvalidInput, strings.Join(out.Conflicts, ", ")+" has a conflict (conflict markers in a text field); it cannot land").
					WithHint("settle it with `lore sync conflicts` / `lore sync resolve`, then land again")
			}
			if out.Status == "" {
				fmt.Println("nothing to land: main already holds every named row")
				return nil
			}
			printLandOutcome(out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every live row of this project")
	cmd.Flags().StringVar(&kind, "kind", "", "only rows of this kind (tasks, rules, …)")
	cmd.Flags().StringVar(&mission, "mission", "", "only the mission and its tasks")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan, change nothing")
	return cmd
}

// landSelector builds the row filter of `lore land`.
func landSelector(args []string, all bool, kind, mission string) landSelect {
	table := ""
	if kind != "" {
		table = kind
		if t := liveTableForGroup(strings.TrimSuffix(kind, "s")); t != "" {
			table = t
		}
	}
	return func(k live.RowKey, f live.File) bool {
		if len(args) == 1 && k.ID != args[0] {
			return false
		}
		if table != "" && k.Table != table {
			return false
		}
		if mission != "" {
			m, _ := f.Body["mission_id"].(string)
			if k.ID != mission && m != mission {
				return false
			}
		}
		return true
	}
}

func (s *liveSession) printLandPlan(ctx context.Context, folder string, sel landSelect) error {
	plan, err := s.plan(ctx, folder, sel)
	if err != nil {
		return errcodes.New(errcodes.Internal, "land plan").WithCause(err)
	}
	fmt.Printf("would land %d row(s) to %s (%s/):\n", countLanding(plan), s.defaultBranch(ctx), folder)
	for _, p := range plan {
		fmt.Printf("  %-40s %-5s %s\n", p.Key.ID, p.Decision.Action, p.Decision.Reason)
	}
	return nil
}

func countLanding(plan []landPlanRow) int {
	n := 0
	for _, p := range plan {
		if p.Decision.Action != live.LandStay {
			n++
		}
	}
	return n
}

// printLiveStatus adds the live part of `lore sync status`: rows that exist
// only in live with their age, and what each waits for (rule 8, side
// effect 13).
func printLiveStatus(ctx context.Context, projectRoot string) {
	s := liveFor(ctx, projectRoot)
	if s == nil {
		return
	}
	folder := s.folderFor(projectRoot)
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		fmt.Println(style.Warn("live: " + err.Error()))
		return
	}
	// Display only (the land preview): unreadable refs or pending list show
	// as none; the land itself re-reads both and fails on an error.
	snap, _ := s.ref.Snapshot(ctx)
	pending, _ := s.ref.Pending()
	fmt.Printf("live rows:        %d (%s)\n", len(plan), folder)
	if snap.Ahead() {
		fmt.Printf("live not pushed:  %d write(s) waiting\n", max(len(pending), 1))
	}
	protected := readLiveNoteText(s.stateDir, liveNoteProtected)
	now := s.clk.Now()
	for _, p := range plan {
		age := now.Sub(p.File.Block.LiveSince).Round(time.Minute)
		why := p.Decision.Reason
		switch {
		case p.Leave:
			why = "on main; leaves live on the next sync"
		case p.Decision.Action != live.LandStay && protected != "":
			why = "waiting to land: main is protected, rides on your next push"
		case p.Decision.Action != live.LandStay:
			why = "waiting to land (" + why + ")"
		}
		fmt.Printf("  %-10s %-40s live %-8s %s\n", p.Key.Table, p.Key.ID, age, why)
	}
}

func readLiveNoteText(stateDir, name string) string {
	b, err := os.ReadFile(filepath.Join(stateDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
