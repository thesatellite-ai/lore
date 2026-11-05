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
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/rowroute"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Handoff states (handoffs.status_str): open until accepted or declined
// (the finished words of §1 "finished").
const (
	handoffOpen     = "open"
	handoffAccepted = "accepted"
	handoffDeclined = "declined"
	flagFor         = "for"
	legacyHandoffTo = "to"
	flagTask        = "task"
	flagSince       = "since"
	flagAck         = "ack"
	tableMemories   = "memories"
)

// Row fields the inbox reads from core documents (handoffs, findings,
// missions).
const (
	fieldToActor    = "to_actor_id"
	fieldFromActor  = "from_actor_id"
	fieldStatusStr  = "status_str"
	fieldTaskID     = "task_id"
	fieldSourceKind = "source_kind"
	fieldSourceRef  = "source_ref"
)

// Row states of `lore mission status`: a closed set. The values are printed
// in the table and are the keys of the JSON "counts" object, so they are
// CLI output: never rename one. Waiting and gated rows take the queue's
// own wait kind, so those two are defined from it.
const (
	missionStateDone      = opscoord.StateDone
	missionStateCancelled = opscoord.StateCancelled
	missionStateBlocked   = opscoord.StateBlocked
	missionStateClaimed   = opscoord.StateClaimed
	missionStateExpired   = opscoord.StateExpired
	missionStateReady     = opscoord.StateReady
	missionStateWaiting   = string(queue.WaitDependency)
	missionStateGated     = string(queue.WaitGate)
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

// coreStoreTarget reports a coordination target on a 0.1.18 store: its
// rows are read through the core, so the v0.1.17 store mirror (routeWrite's
// project write path, which refuses a v2 store) is not opened for it.
func coreStoreTarget(ctx context.Context, t coordTarget) bool {
	sr, ok := t.Rows.(*storeRows)
	return ok && newFormatStore(ctx, sr.sp.st, sr.sp.branch)
}

func openCoordView(ctx context.Context) (*coordView, func(), error) {
	var f commonFlags
	w, werr := openCoordWorld(ctx)
	var target coordTarget
	haveTarget := false
	if werr == nil {
		if t, err := openCoordTarget(ctx, w, ""); err == nil {
			target, haveTarget = t, true
			if len(w.Links) > 0 && !coreStoreTarget(ctx, t) {
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
		_ = client.Close() // end of command: a close error is not actionable
		if haveTarget {
			target.Close()
		}
	}
	projectID, err := resolveProjectID(ctx, rctx.ProjectID)
	if err != nil {
		done()
		return nil, nil, err
	}
	cv := &coordView{client: client, project: projectID, w: nil, labels: &actorLabels{}}
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
	} else if repoID, err := resolveRepoID(ctx, projectID, rctx.RepoMount); err == nil {
		lazyMergedCheck(ctx, projectID, repoID)
	}
	return cv, func() { cv.labels.close(); done() }, nil
}

// tasks returns the project's tasks with live rows winning.
func (cv *coordView) tasks(ctx context.Context) ([]*ent.Task, error) {
	docs, err := cv.taskDocs(ctx)
	if err != nil {
		return nil, err
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

// taskDocs returns the project's task documents, live rows winning; the
// inbox reads watchers and history from them (fields ent.Task lacks).
func (cv *coordView) taskDocs(ctx context.Context) ([]coord.Doc, error) {
	if cv.lister == nil {
		// No live target: the project's tasks through the core.
		return cv.projectDocs(ctx, tableTasks)
	}
	docs, err := cv.lister.List(ctx)
	if err != nil {
		return nil, coordError(err)
	}
	return docs, nil
}

// taskWatch is the part of a task document the "watched" inbox items read
// (TASK 14 watchers, 17.2 history).
type taskWatch struct {
	ID        string                 `json:"id"`
	Title     string                 `json:"title"`
	Mission   string                 `json:"mission_id"`
	Lead      string                 `json:"assigned_to_actor_id"`
	Assignees []string               `json:"assignees"`
	Watchers  []string               `json:"watchers"`
	History   []opstask.HistoryEntry `json:"history"`
}

// assignedRole is me's part in w: lead, assignee, or "" (TASK 14.2: the
// lead is always an assignee, so lead wins).
func (w *taskWatch) assignedRole(me string) string {
	switch {
	case w.Lead == me:
		return roleLead
	case slices.Contains(w.Assignees, me):
		return roleAssignee
	}
	return ""
}

// Roles of an "assigned" inbox item (TASK 20: assigned to you / added as
// assignee / made lead).
const (
	roleLead     = "lead"
	roleAssignee = "assignee"
)

// assignedBy is the newest history entry that gave me role on w, written
// by someone else (TASK 20, 17.2): the lead set to me, or me joining the
// assignees list. ok is false when the history has none (an old row, a
// task created already assigned, or me assigned myself).
func (w *taskWatch) assignedBy(me, role string) (opstask.HistoryEntry, bool) {
	for i := len(w.History) - 1; i >= 0; i-- {
		e := w.History[i]
		if e.By == me {
			continue
		}
		switch {
		case role == roleLead && e.Field == fieldTaskLead && e.To == me,
			role == roleAssignee && e.Field == schema.FieldTaskAssignees &&
				slices.Contains(strings.Split(e.To, ","), me) && !slices.Contains(strings.Split(e.From, ","), me):
			return e, true
		}
	}
	return opstask.HistoryEntry{}, false
}

// assignedItem is the "assigned" inbox item of an open task me leads or is
// an assignee of: who made it so and when, from the history when it says
// (TASK 20), else the task's last update.
func (cv *coordView) assignedItem(ctx context.Context, w *taskWatch, role string, updated time.Time) inboxItem {
	it := inboxItem{Mission: w.Mission, Kind: inboxAssigned, Text: w.ID + "  " + w.Title + " (" + role + ")", At: updated}
	e, ok := w.assignedBy(cv.w.ActorID, role)
	if !ok {
		return it
	}
	if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
		it.At = at
	}
	it.Who = cv.actorLabel(ctx, e.By)
	if role == roleLead {
		it.Text += ": made you lead"
	} else {
		it.Text += ": added you as assignee"
	}
	return it
}

// slaItem is the "sla" inbox item of a task me leads, is assigned to or
// watches whose SLA is at risk or breached (TASK 20, 18.4: computed on
// read, deferred time left out).
func slaItem(t *ent.Task, w *taskWatch, me string, hours map[string]int64, now time.Time) (inboxItem, bool) {
	if len(hours) == 0 || (w.assignedRole(me) == "" && !slices.Contains(w.Watchers, me)) {
		return inboxItem{}, false
	}
	c := opstask.SLAClock{Priority: string(t.Priority), Status: string(t.Status), Created: t.CreatedAt,
		Completed: timeOrZero(t.CompletedAt), DeferredUntil: timeOrZero(t.DeferredUntil), History: w.History}
	st := opstask.SLAStateOf(c, hours, now)
	if (st != opstask.SLAAtRisk && st != opstask.SLABreached) || c.Status == string(entTask.StatusDone) || c.Status == string(entTask.StatusCancelled) {
		return inboxItem{}, false
	}
	return inboxItem{Mission: w.Mission, Kind: inboxSLA, At: now,
		Text: fmt.Sprintf("%s %s: SLA %s (%s, %dh)", t.ID, t.Title, strings.ReplaceAll(st, "_", " "), t.Priority, hours[c.Priority])}, true
}

// timeOrZero is *p, or zero for nil.
func timeOrZero(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// fieldTaskLead is the tasks column holding the lead (TASK 14.2).
const fieldTaskLead = "assigned_to_actor_id"

// watchedFields are the history fields a watcher is told about (TASK 20:
// "watched task changed status, parent, due, assignees").
var watchedFields = map[string]bool{
	fieldStatus: true, schema.FieldTaskParent: true, fieldTaskDueAt: true, schema.FieldTaskAssignees: true,
}

// fieldTaskDueAt is the tasks column holding the due date.
const fieldTaskDueAt = "due_at"

// watchedItems lists, for every task me watches, its history entries on a
// watched field written by someone else (one's own edits are not news).
func (cv *coordView) watchedItems(ctx context.Context, docs []coord.Doc, me string) []inboxItem {
	var out []inboxItem
	for _, d := range docs {
		w, err := docInto[taskWatch](d)
		if err != nil || !slices.Contains(w.Watchers, me) {
			continue // an unreadable row is skipped, as in tasks()
		}
		for _, e := range w.History {
			if !watchedFields[e.Field] || e.By == me {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, e.At)
			if err != nil {
				continue
			}
			out = append(out, inboxItem{Mission: w.Mission, Kind: inboxWatched, Who: cv.actorLabel(ctx, e.By),
				Text: fmt.Sprintf("%s %s: %s %s → %s", w.ID, w.Title, e.Field, orNone(e.From), orNone(e.To)), At: at})
		}
	}
	return out
}

// taskFromDoc decodes a canonical task document into the ORM struct (the
// json tags are the column names, the same keys the document uses).
func taskFromDoc(d coord.Doc) (*ent.Task, error) { return docInto[ent.Task](d) }

// projectDocs reads the live rows of a core-owned table that belong to the
// view's project, through the core (every attached store); coreDocOf
// fills created_by_actor_id from the envelope author.
func (cv *coordView) projectDocs(ctx context.Context, table string) ([]coord.Doc, error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	// A repo linked to a store project coordinates through that store: its
	// missions, findings and handoffs are written from every repo of the
	// group, so each carries the project id of the repo that wrote it (a
	// finding from the proxy repo names the proxy's project). The store's
	// rows are this repo's view because the repo attaches that store folder,
	// so they are read whatever project wrote them; only the repo's own rows
	// are matched on the project id (§7.5 step 3: a finding reaches every
	// agent of the mission, PENDING-60 TestWorkedExample75).
	_, storeTarget := cv.lister.(*storeRows)
	var out []coord.Doc
	for _, src := range coreApp.Stores() {
		groupRows := storeTarget && src != store.SourceRepo
		rows, err := coreApp.List(ctx, src, row.Table(table))
		if err != nil {
			return nil, coreErr(err)
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			d, err := coreDocOf(r)
			if err != nil {
				return nil, errcodes.New(errcodes.Internal, "read "+r.ID.String()).WithCause(err)
			}
			if !groupRows && d.Str(fieldProjectID) != cv.project {
				continue
			}
			out = append(out, d)
		}
	}
	return out, nil
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

// leaseTime is the task's active lease time across holders (time boxes):
// the core row's closed claim spans plus the claim still on the board,
// the numbers `lore task time` reads (TIME_TRACKING 4.2, P3-TASK-LEASE-1).
// A row the core cannot read counts the open claim only.
func (cv *coordView) leaseTime(ctx context.Context, t *ent.Task) (time.Duration, []coord.HolderTime) {
	ttl := time.Duration(0)
	if cv.w != nil {
		ttl = cv.w.Lease.TTL
	}
	var open *signals.Signal
	if s, ok, _ := cv.claim(t.ID); ok {
		open = &s
	}
	var entries []timeentry.Entry
	if d, found, err := coreDoc(ctx, tableTasks, t.ID); err == nil && found {
		entries = d.TimeEntries()
	}
	return coord.LeaseTimeSpans(entries, open, cv.now(), ttl)
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

// inboxItem is one line of `lore inbox`: the operation's item (inbox.show).
type inboxItem = opscoord.InboxItem

// inboxMarker is the per (actor, checkout) ack time (§7.2 "Inbox").
type inboxMarker struct {
	Acked time.Time `json:"acked"`
}

// inboxStateDirMode is the mode of the checkout's lore state folder when
// `inbox --ack` creates it: owner and group only (it holds this person's
// ack marker, nothing others need).
const inboxStateDirMode = 0o750

func inboxMarkerPath(w *coordWorld) string {
	return filepath.Join(w.StateDir, "inbox-"+w.ActorID+".json")
}

// runInbox is inbox.show: the items that need the caller since the last
// ack (or --since), and --ack records now as seen (PASS3 ONE-PATH,
// P3-COORD-1). Expired claims, gates and blocked tasks always show.
func runInbox(ctx context.Context, in opscoord.InboxIn) (opscoord.InboxOut, error) {
	cv, done, err := openCoordView(ctx)
	if err != nil {
		return opscoord.InboxOut{}, err
	}
	defer done()
	if cv.w == nil {
		return opscoord.InboxOut{}, errcodes.New(errcodes.Unsupported, "the inbox needs a git repo")
	}
	items, err := cv.inbox(ctx)
	if err != nil {
		return opscoord.InboxOut{}, err
	}
	// SECRETS 16 / SQ10: every forced reveal since the last ack
	// (secrets_forced.go).
	forced, err := forcedRevealItems(ctx)
	if err != nil {
		return opscoord.InboxOut{}, err
	}
	items = append(items, forced...)
	var marker inboxMarker
	if b, err := os.ReadFile(inboxMarkerPath(cv.w)); err == nil {
		// A corrupt marker reads as "never acked": the inbox shows
		// everything, which is the safe direction.
		_ = json.Unmarshal(b, &marker)
	}
	cut := marker.Acked
	if in.Since > 0 {
		cut = cv.now().Add(-in.Since)
	}
	shown := []inboxItem{} // data is [] when empty, never null (R25 #3)
	for _, it := range items {
		if it.At.IsZero() || it.At.After(cut) || it.Kind == inboxExpired || it.Kind == inboxGate || it.Kind == inboxBlocked {
			shown = append(shown, it)
		}
	}
	if in.Ack {
		// Best effort: the ack marker is a display convenience. Marshal of
		// a struct holding one time.Time cannot fail, and a failed write
		// only means the next `inbox` shows the same items again.
		b, _ := json.Marshal(inboxMarker{Acked: cv.now()})
		if err := os.MkdirAll(cv.w.StateDir, inboxStateDirMode); err == nil {
			_ = os.WriteFile(inboxMarkerPath(cv.w), b, 0o600)
		}
	}
	return opscoord.InboxOut{SchemaVersion: coordJSONSchemaVersion, Kind: constants.KindTaskList, Count: len(shown), Data: shown}, nil
}

// coordJSONSchemaVersion is the schema_version of the inbox and mission
// status JSON envelopes (printJSON's, v0.1.17).
const coordJSONSchemaVersion = 1

// Inbox item kinds.
const (
	inboxAssigned = "assigned"
	inboxHandoff  = "handoff"
	inboxGate     = "gate"
	inboxExpired  = "EXPIRED"
	inboxBlocked  = "blocked"
	inboxFinding  = "finding"
	inboxOverdue  = "overdue"
	inboxWatched  = "watched"
	inboxSLA      = "sla"
)

func (cv *coordView) inbox(ctx context.Context) ([]inboxItem, error) {
	me := cv.w.ActorID
	docs, err := cv.taskDocs(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]*ent.Task, 0, len(docs))
	watch := map[string]*taskWatch{}
	for _, d := range docs {
		if t, terr := taskFromDoc(d); terr == nil {
			tasks = append(tasks, t) // unreadable rows skipped, as in tasks()
		}
		if w, werr := docInto[taskWatch](d); werr == nil {
			watch[w.ID] = w
		}
	}
	missions, err := cv.projectDocs(ctx, tableMissions)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	approverOf := map[string]bool{}
	for _, m := range missions {
		id := m.Str(docKeyID)
		if m.Str(fieldCreatedByActor) == me {
			owned[id] = true
		}
		if ap := docStrs(m, fieldApprovers); len(ap) == 0 || slices.Contains(ap, me) {
			approverOf[id] = true
		}
	}
	involved := map[string]bool{}
	var out []inboxItem
	isHuman := !cv.w.IsAgent()
	slaHours := map[string]int64{}
	if set, ok := policySet(ctx); ok {
		slaHours = opstask.SLAHours(set) // no policy: no SLA items
	}
	for _, t := range tasks {
		mission := deref(t.MissionID)
		mine := t.CreatedByActorID != nil && *t.CreatedByActorID == me
		ownsIt := owned[mission] || (mission == "" && mine)
		sig, claimed, active := cv.claim(t.ID)
		if claimed && sig.Holder == me {
			involved[mission] = true
		}
		open := t.Status == entTask.StatusTodo || t.Status == entTask.StatusInProgress
		heldByOther := claimed && active && sig.Holder != me
		// TASK 20: assigned to you / added as assignee / made lead, over the
		// lead and the assignees list (14.2); an old row without the list
		// still has its lead.
		w := watch[t.ID]
		if w == nil {
			w = &taskWatch{ID: t.ID, Title: t.Title, Mission: mission, Lead: deref(t.AssignedToActorID)}
		}
		if role := w.assignedRole(me); open && role != "" && !heldByOther {
			involved[mission] = true
			out = append(out, cv.assignedItem(ctx, w, role, t.UpdatedAt))
		}
		if it, ok := slaItem(t, w, me, slaHours, cv.now()); ok {
			out = append(out, it)
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
		if used, _ := cv.leaseTime(ctx, t); open && claimed && active && ownsIt && t.TimeboxSeconds != nil && used > time.Duration(*t.TimeboxSeconds)*time.Second {
			out = append(out, inboxItem{Mission: mission, Kind: inboxOverdue, Who: holderLabel(sig), Text: fmt.Sprintf("%s over its time box (%s)", t.ID, time.Duration(*t.TimeboxSeconds)*time.Second)})
		}
	}
	out = append(out, cv.watchedItems(ctx, docs, me)...)
	out = append(out, cv.moreInboxItems(ctx, docs, me)...) // TASK 20 rest (FB-TASK-3)
	out = append(out, cv.metaReviewItems(ctx)...)          // AGENT_IDENTITY 8.3 review queue (P3-AGENT-2)
	hs, err := cv.projectDocs(ctx, tableHandoffs)
	if err == nil {
		for _, h := range hs {
			if h.Str(fieldToActor) != me || h.Str(fieldStatusStr) != handoffOpen {
				continue
			}
			out = append(out, inboxItem{Mission: h.Str(fieldMissionID), Kind: inboxHandoff, Who: cv.actorLabel(ctx, h.Str(fieldFromActor)),
				Text: strings.TrimSpace(h.Str(fieldTaskID) + "  " + h.Str(fieldBody)), At: h.Time(docKeyCreatedAt)})
		}
	}
	fs, err := cv.projectDocs(ctx, tableMemories)
	if err == nil {
		for _, f := range fs {
			if f.Str(fieldSourceKind) != sourceKindFinding {
				continue
			}
			m := f.Str(fieldSourceRef)
			if !involved[m] && !owned[m] && !cv.involvedHere(tasks, m) {
				continue
			}
			out = append(out, inboxItem{Mission: m, Kind: inboxFinding, Who: cv.actorLabel(ctx, f.Str(fieldCreatedByActor)), Text: f.Str(fieldBody), At: f.Time(docKeyCreatedAt)})
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

// missionRow is one task line of `mission status`: the operation's row
// (mission.status).
type missionRow = opscoord.MissionRow

// runMissionStatus is mission.status (PASS3 ONE-PATH, P3-COORD-1).
func runMissionStatus(ctx context.Context, in opscoord.MissionStatusIn) (opscoord.MissionStatusOut, error) {
	cv, done, err := openCoordView(ctx)
	if err != nil {
		return opscoord.MissionStatusOut{}, err
	}
	defer done()
	return cv.missionStatus(ctx, in.ID)
}

// missionStatus is mission id at a glance: every task row with its state,
// the counts per state and per SLA state, and the v0.1.17 text table.
func (cv *coordView) missionStatus(ctx context.Context, id string) (opscoord.MissionStatusOut, error) {
	m, ok, err := coreDoc(ctx, tableMissions, id)
	if err != nil {
		return opscoord.MissionStatusOut{}, err
	}
	if !ok {
		return opscoord.MissionStatusOut{}, errcodes.New(errcodes.NotFound, "no mission "+id)
	}
	mID, mTitle := m.Str(docKeyID), m.Str(fieldTitle)
	all, err := cv.tasks(ctx)
	if err != nil {
		return opscoord.MissionStatusOut{}, err
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
	res := queue.Next(queue.Input{Tasks: qs, Filter: queue.Filter{Mission: id, AnyRepoRole: true}, Now: cv.now(), MaxAttempts: cv.maxAttempts(),
		Weights: urgencyWeights(ctx)})
	var counts opscoord.StateCounts
	var slaCounts opscoord.SLACounts
	sla := cv.newMissionSLA(ctx)
	rows := []missionRow{} // tasks is [] when empty, never null
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
			row.State, row.Who = missionStateDone, cv.actorLabel(ctx, deref(t.DoneBy))
			if t.MergedAt != nil {
				row.Detail = "merged " + t.MergedAt.UTC().Format("15:04")
			} else {
				row.Detail = "not merged yet"
			}
		case t.Status == entTask.StatusCancelled:
			row.State = missionStateCancelled
		case t.Status == entTask.StatusBlocked:
			row.State = missionStateBlocked
		case claimed && active:
			row.State, row.Who, row.Detail = missionStateClaimed, holderLabel(s), "working (until "+s.Expires.UTC().Format("15:04")+")"
		case claimed:
			row.State, row.Who = missionStateExpired, holderLabel(s)
			if b := workBranchOf(t, s); b == "" {
				row.Detail = "work lost: never pushed"
			} else {
				row.Detail = "work branch " + b
			}
		case ready[t.ID]:
			row.State = missionStateReady
		default:
			w := waiting[t.ID]
			row.State, row.Detail = string(w.Kind), w.Reason
			if w.Kind == queue.WaitGate {
				row.State = missionStateGated
			}
		}
		if used, per := cv.leaseTime(ctx, t); used > 0 || t.TimeboxSeconds != nil {
			row.Detail = strings.TrimSpace(row.Detail + " " + cv.timeboxText(ctx, t, used, per))
		}
		// 18.4: SLA on read; a closed task keeps its state in --json, the
		// text calls out only open late ones.
		if row.SLA = sla.state(t); row.SLA != "" {
			slaCounts.Add(row.SLA)
			if slaLate(row.SLA) && row.State != missionStateDone && row.State != missionStateCancelled {
				row.Detail = strings.TrimSpace(row.Detail + " " + sla.slaText(t, row.SLA))
			}
		}
		counts.Add(row.State)
		rows = append(rows, row)
	}
	data := opscoord.MissionStatus{Mission: mID, Title: mTitle, Counts: counts, SLA: slaCounts, Tasks: rows}
	return opscoord.MissionStatusOut{SchemaVersion: coordJSONSchemaVersion, Kind: constants.KindTaskList, Count: len(rows),
		Data: data, Lines: missionStatusLines(data)}, nil
}

// missionStatusLines is the v0.1.17 text of mission status: a summary line
// and one line per task.
func missionStatusLines(d opscoord.MissionStatus) []string {
	c := d.Counts
	slaLine := ""
	if n, b := d.SLA.AtRisk, d.SLA.Breached; n+b > 0 {
		slaLine = fmt.Sprintf(" · SLA: %d at risk, %d breached", n, b)
	}
	lines := []string{fmt.Sprintf("%s %s   %d tasks · %d done · %d claimed · %d ready · %d waiting · %d gated · %d expired · %d blocked%s",
		d.Mission, d.Title, len(d.Tasks), c.Done, c.Claimed, c.Ready, c.Waiting, c.Gated, c.Expired, c.Blocked, slaLine)}
	for _, r := range d.Tasks {
		lines = append(lines, fmt.Sprintf("  %-9s %s  %-28s %-12s %s", r.State, r.ID, r.Title, r.Who, r.Detail))
	}
	return lines
}

// urgencyWeights are the repo's tasks.urgency.* weights over the 13.2
// defaults (OV15B-URG, opstask.UrgencyWeights). No policy, or a policy
// that does not resolve, leaves the zero value: queue.Next then uses the
// defaults, the same ranking as before the keys existed.
func urgencyWeights(ctx context.Context) urgency.Weights {
	set, ok := policySet(ctx)
	if !ok {
		return urgency.Weights{}
	}
	w, err := opstask.UrgencyWeights(set)
	if err != nil {
		return urgency.Weights{}
	}
	return w
}

func (cv *coordView) maxAttempts() int {
	if cv.w == nil {
		return 0
	}
	return cv.w.Agents.MaxAttempts
}
