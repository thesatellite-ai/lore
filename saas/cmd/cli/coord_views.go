// coord_views.go — the coordination views and small writers of LORE_TEAM_SPEC
// §7.2: `lore finding add`, `lore handoff add`, `lore inbox`, `lore mission
// status`. Every view combines rows (lore.db, live wins) with the signals
// ref, so "who is on it right now" is always the claim as seen now.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"dbent/gen/ent"
	entHandoff "dbent/gen/ent/handoff"
	entMemory "dbent/gen/ent/memory"
	entMission "dbent/gen/ent/mission"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/rowroute"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// Handoff states (handoffs.status_str): open until accepted or declined
// (the finished words of §1 "finished").
const (
	handoffOpen     = "open"
	flagFor         = "for"
	legacyHandoffTo = "to"
	flagTask        = "task"
	flagSince       = "since"
	flagAck         = "ack"
	tableMemories   = "memories"
)

// ---------------------------------------------------------------- finding

func newFindingCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "finding", Short: "Findings: what one agent learns, every agent of the mission sees"}
	var mission string
	add := &cobra.Command{
		Use:   "add --mission <id> <text>",
		Short: "Save a finding pinned to a mission (a live memory every agent sees in its inbox)",
		Long: `Finding saves a memory pinned to a mission. It is written live (shared at once), like
the mission, and shows up in the inbox of everyone working the mission.

Example:
  lore finding add --mission mis_12 "the proxy strips X-Forwarded-Host; links must use the public base URL"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if mission == "" {
				return errcodes.New(errcodes.InvalidInput, "--mission is required")
			}
			// The finding inherits the mission's liveness: missions are
			// live, so the memory goes on the live ref too (§7.2).
			liveCmdState.table, liveCmdState.choice = tableMemories, rowroute.ChoiceLive
			id, err := addLinkedMemory(cmd.Context(), &commonFlags{}, strings.Join(args, " "), sourceKindFinding, mission)
			if err != nil {
				return err
			}
			fmt.Printf("%s %s finding on %s (live)\n", style.Success("✓"), id, mission)
			return nil
		},
	}
	add.Flags().StringVar(&mission, flagMission, "", "the mission the finding belongs to")
	cmd.AddCommand(add)
	return cmd
}

// ---------------------------------------------------------------- handoff

// handoffGroupName is the generated `lore handoff` group `add` replaces
// its generated add in (spec form: `handoff add --for @x [--task id]`).
const handoffGroupName = "handoff"

func newHandoffForCommand() *cobra.Command {
	var forActor, task, bodyFlag, legacyTo string
	add := &cobra.Command{
		Use:   "add --for <@handle> [--task <id>] <text>",
		Short: "Leave a handoff for someone (shows in their inbox)",
		Long: `Add leaves a handoff for a person or agent on the roster; it shows in their inbox,
grouped under the task's mission.

Example:
  lore handoff add --for @frontend-2 --task tsk_31 "form is half done; tests in invite.test.tsx"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if forActor == "" {
				forActor = legacyTo
			}
			if forActor == "" {
				return errcodes.New(errcodes.InvalidInput, "--for @handle is required")
			}
			text := strings.Join(args, " ")
			if text == "" {
				text = bodyFlag
			}
			body, err := textnorm.Normalize(text)
			if err != nil {
				return errcodes.New(errcodes.EmptyBody, err.Error())
			}
			if err := refuseSecrets(body); err != nil {
				return err
			}
			to := forActor
			if ids.Validate(forActor, ids.PrefixActor) != nil {
				if to, err = resolveHandleActor(ctx, forActor); err != nil {
					return err
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
			from, err := resolveCurrentActorID(ctx, client)
			if err != nil {
				return err
			}
			create := client.Handoff.Create().SetProjectID(projectID).SetFromActorID(from).SetToActorID(to).
				SetBody(body).SetStatusStr(handoffOpen)
			if task != "" {
				create.SetTaskID(task)
				if t, err := client.Task.Get(ctx, task); err == nil && t.MissionID != nil {
					create.SetMissionID(*t.MissionID)
				}
			}
			h, err := create.Save(ctx)
			if err != nil {
				return errcodes.New(errcodes.Internal, "create handoff").WithCause(err)
			}
			fmt.Printf("%s handoff %s for %s\n", style.Success("✓"), h.ID, forActor)
			return nil
		},
	}
	add.Flags().StringVar(&forActor, flagFor, "", "who the handoff is for (@handle)")
	add.Flags().StringVar(&task, flagTask, "", "the task it is about")
	add.Flags().StringVar(&bodyFlag, constants.FlagBody, "", "the handoff text (instead of the positional text)")
	// --to <actor> is the generated command's flag, kept so existing
	// scripts and docs work; --to elsewhere names a project, so new usage
	// is --for.
	add.Flags().StringVar(&legacyTo, legacyHandoffTo, "", "deprecated alias of --for")
	_ = add.Flags().MarkHidden(legacyHandoffTo)
	return add
}

// ---------------------------------------------------------------- shared view

// coordView is the data every coordination view reads once: rows of the
// project the work lives in (this repo, or the linked store project for a
// linked repo) with live rows winning, and the signals ref of that project.
type coordView struct {
	w       *coordWorld
	client  *ent.Client
	project string
	board   signals.View
	// hasBoard is false when no signals ref could be read.
	hasBoard bool
	// folder is the project folder on the signals ref.
	folder string
	// lister gives the fresh task rows (live wins); nil = lore.db only.
	lister coordLister
	// labels names actors (this project, then the org roster).
	labels *actorLabels
}

func openCoordView(ctx context.Context) (*coordView, func(), error) {
	var f commonFlags
	w, werr := openCoordWorld(ctx)
	var target coordTarget
	haveTarget := false
	if werr == nil {
		if t, err := openCoordTarget(ctx, w, ""); err == nil {
			target, haveTarget = t, true
			if len(w.Links) > 0 {
				// The group's missions, findings and handoffs are in the
				// store project: read them from its mirror (stores work).
				link, lerr := defaultLink(w.Links)
				if lerr == nil {
					if _, err := routeWrite(ctx, &f, toFlags{To: w.Org.Name + "/" + link.Folder}, kindTask, ""); err != nil {
						target.Close()
						return nil, nil, err
					}
				}
			}
		}
	}
	rctx, client, err := resolveContext(&f)
	if err != nil {
		if haveTarget {
			target.Close()
		}
		return nil, nil, err
	}
	done := func() {
		_ = client.Close()
		if haveTarget {
			target.Close()
		}
	}
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		done()
		return nil, nil, err
	}
	cv := &coordView{client: client, project: projectID, w: nil, labels: &actorLabels{local: client}}
	if werr == nil {
		cv.w = w
	}
	if haveTarget {
		if mc, ok := target.Rows.(interface{ MergedCheck(context.Context) }); ok {
			mc.MergedCheck(ctx)
		}
		cv.folder = target.Folder
		if l, ok := target.Rows.(coordLister); ok {
			cv.lister = l
		}
		if v, err := target.Signals.Local(ctx); err == nil {
			cv.board, cv.hasBoard = v, true
		}
	} else if repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount); err == nil {
		lazyMergedCheck(ctx, client, projectID, repoID)
	}
	return cv, func() { cv.labels.close(); done() }, nil
}

// tasks returns the project's tasks with live rows winning.
func (cv *coordView) tasks(ctx context.Context) ([]*ent.Task, error) {
	if cv.lister == nil {
		ts, err := cv.client.Task.Query().Where(entTask.ProjectID(cv.project)).All(ctx)
		if err != nil {
			return nil, errcodes.New(errcodes.Internal, "list tasks").WithCause(err)
		}
		return ts, nil
	}
	docs, err := cv.lister.List(ctx)
	if err != nil {
		return nil, coordError(err)
	}
	out := make([]*ent.Task, 0, len(docs))
	for _, d := range docs {
		t, err := taskFromDoc(d)
		if err != nil {
			continue // a row this binary cannot read is skipped, not fatal
		}
		out = append(out, t)
	}
	return out, nil
}

// taskFromDoc decodes a canonical task document into the ORM struct (the
// json tags are the column names, the same keys the document uses).
func taskFromDoc(d coord.Doc) (*ent.Task, error) {
	b, err := json.Marshal(map[string]any(d))
	if err != nil {
		return nil, err
	}
	var t ent.Task
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (cv *coordView) claim(id string) (signals.Signal, bool, bool) {
	if !cv.hasBoard {
		return signals.Signal{}, false, false
	}
	p := signals.ClaimPath(cv.folder, id)
	s, ok := cv.board.Board[p]
	_, active := cv.board.Active(p)
	return s, ok, active
}

// workBranchOf is where a holder's work can be fetched: the row's
// work_branch (recorded by `task note` when it pushed) or the claim's.
func workBranchOf(t *ent.Task, s signals.Signal) string {
	if b := deref(t.WorkBranch); b != "" {
		return b
	}
	return s.WorkBranch
}

// actorLabel names an actor for views (actorLabels: this project, then
// the org roster).
func (cv *coordView) actorLabel(ctx context.Context, id string) string {
	return cv.labels.label(ctx, id)
}

// leaseTime is the task's active lease time across holders (time boxes).
func (cv *coordView) leaseTime(t *ent.Task) (time.Duration, []coord.HolderTime) {
	var done time.Time
	if t.CompletedAt != nil {
		done = *t.CompletedAt
	}
	ttl := time.Duration(0)
	if cv.w != nil {
		ttl = cv.w.Lease.TTL
	}
	return coord.LeaseTime(t.ClaimLog, done, cv.now(), ttl)
}

// timeboxText renders "55m (timebox 1h; frontend-1 18m + frontend-2 37m)".
func (cv *coordView) timeboxText(ctx context.Context, t *ent.Task, used time.Duration, per []coord.HolderTime) string {
	out := shortDuration(used.Round(time.Minute))
	var inner []string
	if t.TimeboxSeconds != nil {
		inner = append(inner, "timebox "+shortDuration(time.Duration(*t.TimeboxSeconds)*time.Second))
	}
	if len(per) > 1 {
		parts := make([]string, 0, len(per))
		for _, h := range per {
			parts = append(parts, cv.actorLabel(ctx, h.ActorID)+" "+shortDuration(h.Time.Round(time.Minute)))
		}
		inner = append(inner, strings.Join(parts, " + "))
	}
	if len(inner) > 0 {
		out += " (" + strings.Join(inner, "; ") + ")"
	}
	return out
}

func (cv *coordView) now() time.Time {
	if cv.hasBoard {
		return cv.board.Now
	}
	return cliClock().Now()
}

// ---------------------------------------------------------------- inbox

// inboxItem is one line of `lore inbox`.
type inboxItem struct {
	Mission string    `json:"mission"`
	Kind    string    `json:"kind"`
	Who     string    `json:"who,omitempty"`
	Text    string    `json:"text"`
	At      time.Time `json:"at"`
}

// inboxMarker is the per (actor, checkout) ack time (§7.2 "Inbox").
type inboxMarker struct {
	Acked time.Time `json:"acked"`
}

func inboxMarkerPath(w *coordWorld) string {
	return filepath.Join(w.StateDir, "inbox-"+w.ActorID+".json")
}

func newInboxCommand() *cobra.Command {
	var since time.Duration
	var ack, jsonOut bool
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings",
		Long: `Inbox lists, grouped by mission: tasks assigned to you, handoffs for you, gates you
can approve, expired claims and poison tasks of missions you own, and findings.
--ack marks everything seen (local to this checkout); --since limits to recent items.

Example:
  lore inbox --since 2h`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cv, done, err := openCoordView(ctx)
			if err != nil {
				return err
			}
			defer done()
			if cv.w == nil {
				return errcodes.New(errcodes.Unsupported, "the inbox needs a git repo")
			}
			items, err := cv.inbox(ctx)
			if err != nil {
				return err
			}
			var marker inboxMarker
			if b, err := os.ReadFile(inboxMarkerPath(cv.w)); err == nil {
				_ = json.Unmarshal(b, &marker)
			}
			cut := marker.Acked
			if since > 0 {
				cut = cv.now().Add(-since)
			}
			var shown []inboxItem
			for _, it := range items {
				if it.At.IsZero() || it.At.After(cut) || it.Kind == inboxExpired || it.Kind == inboxGate || it.Kind == inboxBlocked {
					shown = append(shown, it)
				}
			}
			if jsonOut {
				printJSON(constants.KindTaskList, shown, len(shown))
			} else if len(shown) == 0 {
				fmt.Println(style.Muted("(inbox empty)"))
			} else {
				for _, it := range shown {
					m := it.Mission
					if m == "" {
						m = "-"
					}
					fmt.Printf("%s  %-8s %-12s %s\n", m, it.Kind, it.Who, it.Text)
				}
			}
			if ack {
				b, _ := json.Marshal(inboxMarker{Acked: cv.now()})
				if err := os.MkdirAll(cv.w.StateDir, 0o755); err == nil {
					_ = os.WriteFile(inboxMarkerPath(cv.w), b, 0o600)
				}
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, flagSince, 0, "only items newer than this (e.g. 2h)")
	cmd.Flags().BoolVar(&ack, flagAck, false, "mark everything shown as seen")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// Inbox item kinds.
const (
	inboxAssigned = "assigned"
	inboxHandoff  = "handoff"
	inboxGate     = "gate"
	inboxExpired  = "EXPIRED"
	inboxBlocked  = "blocked"
	inboxFinding  = "finding"
	inboxOverdue  = "overdue"
)

func (cv *coordView) inbox(ctx context.Context) ([]inboxItem, error) {
	me := cv.w.ActorID
	tasks, err := cv.tasks(ctx)
	if err != nil {
		return nil, err
	}
	missions, err := cv.client.Mission.Query().Where(entMission.ProjectID(cv.project)).All(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list missions").WithCause(err)
	}
	owned := map[string]bool{}
	approverOf := map[string]bool{}
	for _, m := range missions {
		if m.CreatedByActorID != nil && *m.CreatedByActorID == me {
			owned[m.ID] = true
		}
		if len(m.Approvers) == 0 || slices.Contains(m.Approvers, me) {
			approverOf[m.ID] = true
		}
	}
	involved := map[string]bool{}
	var out []inboxItem
	isHuman := !cv.w.IsAgent()
	for _, t := range tasks {
		mission := deref(t.MissionID)
		mine := t.CreatedByActorID != nil && *t.CreatedByActorID == me
		ownsIt := owned[mission] || (mission == "" && mine)
		sig, claimed, active := cv.claim(t.ID)
		if claimed && sig.Holder == me {
			involved[mission] = true
		}
		open := t.Status == entTask.StatusTodo || t.Status == entTask.StatusInProgress
		switch {
		case open && t.AssignedToActorID != nil && *t.AssignedToActorID == me && !(claimed && active && sig.Holder != me):
			involved[mission] = true
			out = append(out, inboxItem{Mission: mission, Kind: inboxAssigned, Text: t.ID + "  " + t.Title, At: t.UpdatedAt})
		}
		if open && claimed && !active && ownsIt {
			wb := "work lost: never pushed"
			if b := workBranchOf(t, sig); b != "" {
				wb = "work branch pushed (" + b + ")"
			}
			out = append(out, inboxItem{Mission: mission, Kind: inboxExpired, Who: holderLabel(sig),
				Text: fmt.Sprintf("%s claimed by %s, lease expired %s ago; %s", t.ID, holderLabel(sig), agoString(cv.now().Sub(sig.Expires)), wb)})
		}
		if t.Status == entTask.StatusBlocked && ownsIt && cv.w.Agents.MaxAttempts > 0 && t.Attempts >= cv.w.Agents.MaxAttempts {
			out = append(out, inboxItem{Mission: mission, Kind: inboxBlocked, Text: fmt.Sprintf("%s %s: failed %d times", t.ID, t.Title, t.Attempts)})
		}
		if open && isHuman && t.Gate != nil && coord.Gated(gateDoc(t)) && approverOf[mission] {
			out = append(out, inboxItem{Mission: mission, Kind: inboxGate, Text: fmt.Sprintf("%s %s: gate needs approval (lore gate approve %s)", t.ID, t.Title, t.ID)})
		}
		if used, _ := cv.leaseTime(t); open && claimed && active && ownsIt && t.TimeboxSeconds != nil && used > time.Duration(*t.TimeboxSeconds)*time.Second {
			out = append(out, inboxItem{Mission: mission, Kind: inboxOverdue, Who: holderLabel(sig), Text: fmt.Sprintf("%s over its time box (%s)", t.ID, time.Duration(*t.TimeboxSeconds)*time.Second)})
		}
	}
	hs, err := cv.client.Handoff.Query().Where(entHandoff.ProjectID(cv.project), entHandoff.ToActorID(me), entHandoff.StatusStr(handoffOpen)).All(ctx)
	if err == nil {
		for _, h := range hs {
			out = append(out, inboxItem{Mission: deref(h.MissionID), Kind: inboxHandoff, Who: cv.actorLabel(ctx, deref(h.FromActorID)), Text: strings.TrimSpace(deref(h.TaskID) + "  " + h.Body), At: h.CreatedAt})
		}
	}
	fs, err := cv.client.Memory.Query().Where(entMemory.ProjectID(cv.project), entMemory.SourceKind(sourceKindFinding)).All(ctx)
	if err == nil {
		for _, f := range fs {
			m := deref(f.SourceRef)
			if !involved[m] && !owned[m] && !cv.involvedHere(tasks, m) {
				continue
			}
			out = append(out, inboxItem{Mission: m, Kind: inboxFinding, Who: cv.actorLabel(ctx, deref(f.CreatedByActorID)), Text: f.Body, At: f.CreatedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mission < out[j].Mission })
	return out, nil
}

// involvedHere: the caller holds (or held, per this checkout's records) a
// task of mission m.
func (cv *coordView) involvedHere(tasks []*ent.Task, m string) bool {
	for _, r := range cv.w.held().List(cv.w.Session()) {
		for _, t := range tasks {
			if t.ID == r.Task && deref(t.MissionID) == m {
				return true
			}
		}
	}
	return false
}

func gateDoc(t *ent.Task) coord.Doc {
	d := coord.Doc{"title": t.Title, "body": deref(t.Body), "approved_hash": deref(t.ApprovedHash)}
	if t.Gate != nil {
		d["gate"] = string(*t.Gate)
	}
	return d
}

// ---------------------------------------------------------------- mission status

func newMissionStatusCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status <mission-id>",
		Short: "The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cv, done, err := openCoordView(ctx)
			if err != nil {
				return err
			}
			defer done()
			return cv.missionStatus(ctx, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// missionRow is one task line of `mission status`.
type missionRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Repo   string `json:"repo,omitempty"`
	State  string `json:"state"`
	Who    string `json:"who,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (cv *coordView) missionStatus(ctx context.Context, id string, jsonOut bool) error {
	m, err := cv.client.Mission.Get(ctx, id)
	if err != nil {
		return errcodes.New(errcodes.NotFound, "no mission "+id).WithCause(err)
	}
	all, err := cv.tasks(ctx)
	if err != nil {
		return err
	}
	var qs []queue.Task
	byID := map[string]*ent.Task{}
	for _, t := range all {
		byID[t.ID] = t
		st := queue.Unclaimed
		by := ""
		if s, ok, active := cv.claim(t.ID); ok {
			st, by = queue.ClaimExpired, holderLabel(s)
			if active {
				st = queue.Claimed
			}
		}
		qt := queue.Task{ID: t.ID, Title: t.Title, Status: queue.Status(t.Status), MissionID: deref(t.MissionID),
			Gated: t.Gate != nil && coord.Gated(gateDoc(t)), Attempts: t.Attempts, Merged: t.MergedAt != nil, Claim: st, ClaimedBy: by, Role: deref(t.Role)}
		for _, d := range t.BlockedBy {
			qt.After = append(qt.After, queue.Dep{TaskID: d.TaskID, Until: queue.Until(d.Until)})
		}
		qs = append(qs, qt)
	}
	res := queue.Next(queue.Input{Tasks: qs, Filter: queue.Filter{Mission: id, AnyRepoRole: true}, Now: cv.now(), MaxAttempts: cv.maxAttempts()})
	counts := map[string]int{}
	var rows []missionRow
	waiting := map[string]queue.Waiting{}
	for _, w := range res.Waiting {
		waiting[w.Task.ID] = w
	}
	ready := map[string]bool{}
	for _, r := range res.Ready {
		ready[r.ID] = true
	}
	for _, t := range all {
		if deref(t.MissionID) != id {
			continue
		}
		row := missionRow{ID: t.ID, Title: t.Title, Repo: deref(t.RepoID)}
		s, claimed, active := cv.claim(t.ID)
		switch {
		case t.Status == entTask.StatusDone:
			row.State, row.Who = "done", cv.actorLabel(ctx, deref(t.DoneBy))
			if t.MergedAt != nil {
				row.Detail = "merged " + t.MergedAt.UTC().Format("15:04")
			} else {
				row.Detail = "not merged yet"
			}
		case t.Status == entTask.StatusCancelled:
			row.State = "cancelled"
		case t.Status == entTask.StatusBlocked:
			row.State = "blocked"
		case claimed && active:
			row.State, row.Who, row.Detail = "claimed", holderLabel(s), "working (until "+s.Expires.UTC().Format("15:04")+")"
		case claimed:
			row.State, row.Who = "expired", holderLabel(s)
			if b := workBranchOf(t, s); b == "" {
				row.Detail = "work lost: never pushed"
			} else {
				row.Detail = "work branch " + b
			}
		case ready[t.ID]:
			row.State = "ready"
		default:
			w := waiting[t.ID]
			row.State, row.Detail = string(w.Kind), w.Reason
			if w.Kind == queue.WaitGate {
				row.State = "gated"
			}
		}
		if used, per := cv.leaseTime(t); used > 0 || t.TimeboxSeconds != nil {
			row.Detail = strings.TrimSpace(row.Detail + " " + cv.timeboxText(ctx, t, used, per))
		}
		counts[row.State]++
		rows = append(rows, row)
	}
	if jsonOut {
		printJSON(constants.KindTaskList, map[string]any{"mission": m.ID, "title": m.Title, "counts": counts, "tasks": rows}, len(rows))
		return nil
	}
	fmt.Printf("%s %s   %d tasks · %d done · %d claimed · %d ready · %d waiting · %d gated · %d expired · %d blocked\n",
		m.ID, m.Title, len(rows), counts["done"], counts["claimed"], counts["ready"], counts["waiting"], counts["gated"], counts["expired"], counts["blocked"])
	for _, r := range rows {
		fmt.Printf("  %-9s %s  %-28s %-12s %s\n", r.State, r.ID, r.Title, r.Who, r.Detail)
	}
	return nil
}

func (cv *coordView) maxAttempts() int {
	if cv.w == nil {
		return 0
	}
	return cv.w.Agents.MaxAttempts
}
