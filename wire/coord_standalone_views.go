package wire

// coord_standalone_views.go — `lore inbox` and `lore mission status` for a
// host without its own coordination machinery (cmd/lore, P3-SIGCOORD-5):
// v0.1.17's views (saas coord_views.go, coord_inbox_more.go,
// inbox_parentcycle.go, inbox_metakeys.go, mission_sla.go) over the
// engine's task documents and the signals board, no ORM.
//
//	coordView ─► the world (repo or linked store ref, forTask "")
//	          ─► task documents of every attached store (engineRows.List)
//	          ─► missions, handoffs, memories, comments of the project
//	          ─► the last fetched board (Signals.Local)
//	inbox          ─► items (assigned, sla, EXPIRED, FORCED, blocked, gate, overdue,
//	                  watched, triage, child, parent, deleted, mention,
//	                  recurring, metadata, handoff, finding), --since/--ack
//	mission status ─► one row per task with its state, counts, SLA counts
//
// Forced reveals (SECRETS 16) come from the repo audit sink's hash chain
// (coord_standalone_forced.go); a sink without a chain lists none.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/queue"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Document keys and values the views read (column names, v0.1.17 text).
const (
	vkMission     = "mission_id"
	vkProject     = "project_id"
	vkAssignees   = "assignees"
	vkAttempts    = "attempts"
	vkTimebox     = "timebox_seconds"
	vkWorkBranch  = "work_branch"
	vkPriority    = "priority"
	vkCompletedAt = "completed_at"
	vkDeferred    = "deferred_until"
	vkDoneBy      = "done_by"
	vkRepo        = "repo_id"
	vkParent      = "parent_id"
	vkCommitment  = "commitment"
	vkRecur       = "recur"
	vkDueAt       = "due_at"
	vkEntityTable = "entity_table"
	vkEntityID    = "entity_id"
	vkBody        = "body"
	vkToActor     = "to_actor_id"
	vkFromActor   = "from_actor_id"
	vkStatusStr   = "status_str"
	vkTaskID      = "task_id"
	vkSourceKind  = "source_kind"
	vkSourceRef   = "source_ref"
	vkHandle      = "handle"
	vkLabel       = "label"
	vkDisplayName = "display_name"
	vkActorKind   = "kind"
	handoffOpen   = "open"
	findingKind   = "finding"
	proposed      = "proposed"
	recurIndex    = "index"
	noneText      = "(none)"
	dueLayout     = "Mon 2006-01-02"
	inboxFileMode = 0o600
	inboxDirMode  = 0o750
	coordSchemaV  = 1           // schema_version of the inbox and mission status envelopes
	kindTaskList  = "task.list" // their kind (v0.1.17 constants.KindTaskList)
	metaReviewCmd = "lore meta keys --unreviewed"
	cycleArrow    = " → "
)

// Inbox item kinds (v0.1.17 text, kept as is).
const (
	inboxAssigned  = "assigned"
	inboxHandoff   = "handoff"
	inboxGate      = "gate"
	inboxExpired   = "EXPIRED"
	inboxBlocked   = "blocked"
	inboxFinding   = "finding"
	inboxOverdue   = "overdue"
	inboxWatched   = "watched"
	inboxSLA       = "sla"
	inboxTriage    = "triage"
	inboxChild     = "child"
	inboxParent    = "parent"
	inboxDeleted   = "deleted"
	inboxMention   = "mention"
	inboxRecurring = "recurring"
	inboxMetadata  = "metadata"
	roleLead       = "lead"
	roleAssignee   = "assignee"
)

// coordView is what every coordination view reads once.
type coordView struct {
	a        *App
	w        *coordWorld
	project  string
	board    signals.View
	hasBoard bool
	docs     []coord.Doc
	labels   map[string]string
	// rosterSynced: the roster's store was synced once for a label miss.
	rosterSynced bool
}

// openView gathers the view: the world pointed at the work queue, the
// task documents and the last fetched board.
func (h standaloneCoord) openView(ctx context.Context) (*coordView, error) {
	w, err := h.openWorldFor(ctx, "")
	if err != nil {
		return nil, err
	}
	cv := &coordView{a: h.a, w: w, labels: map[string]string{}}
	if p, perr := h.a.Project(ctx); perr == nil {
		cv.project = p.String()
	}
	if v, lerr := w.signalsStore().Local(ctx); lerr == nil {
		cv.board, cv.hasBoard = v, true
	}
	if cv.docs, err = (engineRows{a: h.a}).List(ctx); err != nil {
		return nil, err
	}
	return cv, nil
}

// now is the board's time, else the App clock.
func (cv *coordView) now() time.Time {
	if cv.hasBoard {
		return cv.board.Now
	}
	return cv.a.Now()
}

// claim is task id's claim on the board: the signal, claimed, active.
func (cv *coordView) claim(id string) (signals.Signal, bool, bool) {
	if !cv.hasBoard {
		return signals.Signal{}, false, false
	}
	p := signals.ClaimPath(cv.w.folder, id)
	s, ok := cv.board.Board[p]
	_, active := cv.board.Active(p)
	return s, ok, active
}

// projectDocs are the live rows of table for this view: a linked store's
// rows whatever project wrote them (the group's), the repo's own rows of
// this project only (v0.1.17 projectDocs).
func (cv *coordView) projectDocs(ctx context.Context, table row.Table) []coord.Doc {
	return cv.projectDocsIn(ctx, table, cv.a.Stores())
}

// projectDocsIn is projectDocs over the stores srcs only (the lazy merged
// check keeps the repo's rows alone when the linked store is out of reach,
// E-FATES-4).
func (cv *coordView) projectDocsIn(ctx context.Context, table row.Table, srcs []store.Source) []coord.Doc {
	var out []coord.Doc
	for _, src := range srcs {
		group := cv.w.spot.store && src != store.SourceRepo
		rows, err := cv.a.List(ctx, src, table)
		if err != nil {
			continue // an unreadable store hides its rows, not the view
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			d, derr := docOfRow(r)
			if derr != nil || (!group && d.Str(vkProject) != cv.project) {
				continue
			}
			out = append(out, d)
		}
	}
	return out
}

// label names an actor for views: @handle, else its label, else its
// display name without an agent prefix; the id when no store knows it.
func (cv *coordView) label(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if l, ok := cv.labels[id]; ok {
		return l
	}
	l := id
	for attempt := 0; attempt < rosterLookups; attempt++ {
		if attempt > 0 && !cv.syncRoster(ctx) {
			break
		}
		if _, r, ok, err := cv.a.locateRow(ctx, schema.TableActors, id); err == nil && ok {
			if d, derr := docOfRow(r); derr == nil {
				l = labelOf(d.Str(vkHandle), d.Str(vkLabel), d.Str(vkDisplayName))
			}
			break
		}
	}
	cv.labels[id] = l
	return l
}

// syncRoster syncs the org roster's store once per view, so an agent
// registered since this clone last fetched is named by its label (v0.1.17
// opened the roster fresh); false when there is nothing (more) to sync.
func (cv *coordView) syncRoster(ctx context.Context) bool {
	if cv.rosterSynced {
		return false
	}
	cv.rosterSynced = true
	r, err := cv.a.coordRoster(ctx)
	if err != nil || r.src == store.SourceRepo {
		return false
	}
	_, err = cv.a.Sync(ctx, r.src)
	return err == nil
}

// labelOf is v0.1.17 actorDisplay over plain fields.
func labelOf(handle, label, displayName string) string {
	if handle != "" {
		return rosterHandleMark + handle
	}
	if label != "" {
		return label
	}
	return strings.TrimPrefix(displayName, agentLabelPrefix)
}

// docHas reports a non-null field.
func docHas(d coord.Doc, k string) bool { v, ok := d[k]; return ok && !v.IsNull() }

// docOpenStatus: todo or in progress.
func docOpenStatus(d coord.Doc) bool {
	s := d.Status()
	return s == schema.TaskStatusTodo || s == schema.TaskStatusInProgress
}

// docUnfinished: not done, not cancelled.
func docUnfinished(d coord.Doc) bool {
	s := d.Status()
	return s != schema.TaskStatusDone && s != schema.TaskStatusCancelled
}

// orNone is s, or "(none)" for "".
func orNone(s string) string {
	if s == "" {
		return noneText
	}
	return s
}

// taskWatch is the part of a task document the watched/assigned items read.
type taskWatch struct {
	ID        string                 `json:"id"`
	Title     string                 `json:"title"`
	Mission   string                 `json:"mission_id"`
	Lead      string                 `json:"assigned_to_actor_id"`
	Assignees []string               `json:"assignees"`
	Watchers  []string               `json:"watchers"`
	History   []opstask.HistoryEntry `json:"history"`
}

// watchOf decodes d's watch part (zero on a row that does not decode).
func watchOf(d coord.Doc) taskWatch {
	var w taskWatch
	b, err := canonjson.Object(d).MarshalJSON()
	if err == nil {
		_ = json.Unmarshal(b, &w) // a malformed list reads as none, as v0.1.17 skipped the row
	}
	if w.ID == "" {
		w.ID, w.Title, w.Mission, w.Lead = d.Str(docKeyID), d.Str(fieldTitle), d.Str(vkMission), d.Str(fieldTaskLead)
	}
	return w
}

// assignedRole is me's part in w: lead, assignee, or "".
func (w taskWatch) assignedRole(me string) string {
	switch {
	case w.Lead == me:
		return roleLead
	case slices.Contains(w.Assignees, me):
		return roleAssignee
	}
	return ""
}

// assignedBy is the newest history entry that gave me role, by someone else.
func (w taskWatch) assignedBy(me, role string) (opstask.HistoryEntry, bool) {
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

// leaseTime is the task's active lease time across holders.
func (cv *coordView) leaseTime(d coord.Doc) (time.Duration, []coord.HolderTime) {
	var open *signals.Signal
	if s, ok, _ := cv.claim(d.Str(docKeyID)); ok {
		open = &s
	}
	return coord.LeaseTimeSpans(d.TimeEntries(), open, cv.now(), cv.w.lease.TTL)
}

// timeboxText renders "55m (timebox 1h; frontend-1 18m + frontend-2 37m)".
func (cv *coordView) timeboxText(ctx context.Context, d coord.Doc, used time.Duration, per []coord.HolderTime) string {
	out := shortDuration(used.Round(time.Minute))
	var inner []string
	if docHas(d, vkTimebox) {
		inner = append(inner, "timebox "+shortDuration(time.Duration(d.Int(vkTimebox))*time.Second))
	}
	if len(per) > 1 {
		parts := make([]string, 0, len(per))
		for _, h := range per {
			parts = append(parts, cv.label(ctx, h.ActorID)+" "+shortDuration(h.Time.Round(time.Minute)))
		}
		inner = append(inner, strings.Join(parts, " + "))
	}
	if len(inner) > 0 {
		out += " (" + strings.Join(inner, "; ") + ")"
	}
	return out
}

// workBranchOf is the row's work_branch, else the claim's.
func workBranchOf(d coord.Doc, s signals.Signal) string {
	if b := d.Str(vkWorkBranch); b != "" {
		return b
	}
	return s.WorkBranch
}

// policySet is the repo's policy (ok=false when unreadable).
func (cv *coordView) policySet(ctx context.Context) (policy.Set, bool) {
	set, err := cv.a.Policy(ctx, store.SourceRepo)
	return set, err == nil
}

// slaClock is d's SLA clock.
func slaClock(d coord.Doc, history []opstask.HistoryEntry) opstask.SLAClock {
	return opstask.SLAClock{Priority: d.Str(vkPriority), Status: d.Status(), Created: d.Time(docKeyCreatedAt),
		Completed: d.Time(vkCompletedAt), DeferredUntil: d.Time(vkDeferred), History: history}
}

// ---- inbox ----

// inboxMarker is the per (actor, checkout) ack time (§7.2 "Inbox").
type inboxMarker struct {
	Acked time.Time `json:"acked"`
}

// Inbox implements opscoord.Host: the items that need the caller since
// the last ack (or --since); --ack records now as seen. Expired claims,
// gates and blocked tasks always show.
func (h standaloneCoord) Inbox(ctx context.Context, in opscoord.InboxIn) (opscoord.InboxOut, error) {
	cv, err := h.openView(ctx)
	if err != nil {
		return opscoord.InboxOut{}, err
	}
	items := cv.inbox(ctx)
	forced, err := cv.forcedItems(ctx) // SECRETS 16 / SQ10 (coord_standalone_forced.go)
	if err != nil {
		return opscoord.InboxOut{}, err
	}
	items = append(items, forced...)
	marker := filepath.Join(cv.w.stateDir, "inbox-"+cv.w.actorID+".json")
	var m inboxMarker
	if b, rerr := os.ReadFile(filepath.Clean(marker)); rerr == nil {
		_ = json.Unmarshal(b, &m) // a corrupt marker reads as never acked: the safe direction
	}
	cut := m.Acked
	if in.Since > 0 {
		cut = cv.now().Add(-in.Since)
	}
	shown := []opscoord.InboxItem{}
	for _, it := range items {
		if it.At.IsZero() || it.At.After(cut) || it.Kind == inboxExpired || it.Kind == inboxGate || it.Kind == inboxBlocked {
			shown = append(shown, it)
		}
	}
	if in.Ack {
		// Best effort: a failed write only shows the same items next time.
		if b, merr := json.Marshal(inboxMarker{Acked: cv.now()}); merr == nil && os.MkdirAll(cv.w.stateDir, inboxDirMode) == nil {
			_ = os.WriteFile(marker, b, inboxFileMode)
		}
	}
	return opscoord.InboxOut{SchemaVersion: coordSchemaV, Kind: kindTaskList, Count: len(shown), Data: shown}, nil
}

// inbox lists every item for the caller (v0.1.17 coordView.inbox).
func (cv *coordView) inbox(ctx context.Context) []opscoord.InboxItem {
	me := cv.w.actorID
	missions := cv.projectDocs(ctx, schema.TableMissions)
	owned, approverOf := map[string]bool{}, map[string]bool{}
	for _, m := range missions {
		id := m.Str(docKeyID)
		if m.Str(docKeyAuthor) == me {
			owned[id] = true
		}
		if ap := m.Strs(fieldApprovers); len(ap) == 0 || slices.Contains(ap, me) {
			approverOf[id] = true
		}
	}
	involved := map[string]bool{}
	var out []opscoord.InboxItem
	slaHours := map[string]int64{}
	if set, ok := cv.policySet(ctx); ok {
		slaHours = opstask.SLAHours(set)
	}
	for _, d := range cv.docs {
		id, title, mission := d.Str(docKeyID), d.Str(fieldTitle), d.Str(vkMission)
		ownsIt := owned[mission] || (mission == "" && d.Str(docKeyAuthor) == me)
		sig, claimed, active := cv.claim(id)
		if claimed && sig.Holder == me {
			involved[mission] = true
		}
		open := docOpenStatus(d)
		heldByOther := claimed && active && sig.Holder != me
		w := watchOf(d)
		if role := w.assignedRole(me); open && role != "" && !heldByOther {
			involved[mission] = true
			out = append(out, cv.assignedItem(ctx, w, role, d.Time(docKeyUpdatedAt)))
		}
		if len(slaHours) > 0 && (w.assignedRole(me) != "" || slices.Contains(w.Watchers, me)) && docUnfinished(d) {
			c := slaClock(d, w.History)
			if st := opstask.SLAStateOf(c, slaHours, cv.now()); st == opstask.SLAAtRisk || st == opstask.SLABreached {
				out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxSLA, At: cv.now(),
					Text: fmt.Sprintf("%s %s: SLA %s (%s, %dh)", id, title, strings.ReplaceAll(st, "_", " "), c.Priority, slaHours[c.Priority])})
			}
		}
		if open && claimed && !active && ownsIt {
			wb := "work lost: never pushed"
			if b := workBranchOf(d, sig); b != "" {
				wb = "work branch pushed (" + b + ")"
			}
			out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxExpired, Who: holderName(sig),
				Text: fmt.Sprintf("%s claimed by %s, lease expired %s ago; %s", id, holderName(sig), agoText(cv.now().Sub(sig.Expires)), wb)})
		}
		if d.Status() == schema.TaskStatusBlocked && ownsIt && cv.w.agents.MaxAttempts > 0 && d.Int(vkAttempts) >= cv.w.agents.MaxAttempts {
			out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxBlocked, Text: fmt.Sprintf("%s %s: failed %d times", id, title, d.Int(vkAttempts))})
		}
		if open && !cv.w.isAgent() && coord.Gated(d) && approverOf[mission] {
			out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxGate, Text: fmt.Sprintf("%s %s: gate needs approval (lore gate approve %s)", id, title, id)})
		}
		if open && claimed && active && ownsIt && docHas(d, vkTimebox) {
			box := time.Duration(d.Int(vkTimebox)) * time.Second
			if used, _ := cv.leaseTime(d); used > box {
				out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxOverdue, Who: holderName(sig), Text: fmt.Sprintf("%s over its time box (%s)", id, box)})
			}
		}
	}
	out = append(out, cv.watchedItems(ctx, me)...)
	out = append(out, cv.moreItems(ctx, me)...)
	out = append(out, cv.metaReviewItems(ctx)...)
	for _, hd := range cv.projectDocs(ctx, schema.TableHandoffs) {
		if hd.Str(vkToActor) != me || hd.Str(vkStatusStr) != handoffOpen {
			continue
		}
		out = append(out, opscoord.InboxItem{Mission: hd.Str(vkMission), Kind: inboxHandoff, Who: cv.label(ctx, hd.Str(vkFromActor)),
			Text: strings.TrimSpace(hd.Str(vkTaskID) + "  " + hd.Str(vkBody)), At: hd.Time(docKeyCreatedAt)})
	}
	for _, f := range cv.projectDocs(ctx, schema.TableMemories) {
		if f.Str(vkSourceKind) != findingKind {
			continue
		}
		m := f.Str(vkSourceRef)
		if !involved[m] && !owned[m] && !cv.involvedHere(m) {
			continue
		}
		out = append(out, opscoord.InboxItem{Mission: m, Kind: inboxFinding, Who: cv.label(ctx, f.Str(docKeyAuthor)), Text: f.Str(vkBody), At: f.Time(docKeyCreatedAt)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mission < out[j].Mission })
	return out
}

// assignedItem is the "assigned" item: who made it so and when, from the
// history when it says (TASK 20), else the task's last update.
func (cv *coordView) assignedItem(ctx context.Context, w taskWatch, role string, updated time.Time) opscoord.InboxItem {
	it := opscoord.InboxItem{Mission: w.Mission, Kind: inboxAssigned, Text: w.ID + "  " + w.Title + " (" + role + ")", At: updated}
	e, ok := w.assignedBy(cv.w.actorID, role)
	if !ok {
		return it
	}
	if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil {
		it.At = at
	}
	it.Who = cv.label(ctx, e.By)
	if role == roleLead {
		it.Text += ": made you lead"
	} else {
		it.Text += ": added you as assignee"
	}
	return it
}

// watchedFields are the history fields a watcher is told about (TASK 20).
var watchedFields = map[string]bool{fieldStatus: true, schema.FieldTaskParent: true, vkDueAt: true, schema.FieldTaskAssignees: true}

// watchedItems: history entries on a watched field of a task me watches,
// written by someone else.
func (cv *coordView) watchedItems(ctx context.Context, me string) []opscoord.InboxItem {
	var out []opscoord.InboxItem
	for _, d := range cv.docs {
		w := watchOf(d)
		if !slices.Contains(w.Watchers, me) {
			continue
		}
		for _, e := range w.History {
			if !watchedFields[e.Field] || e.By == me {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, e.At)
			if err != nil {
				continue
			}
			out = append(out, opscoord.InboxItem{Mission: w.Mission, Kind: inboxWatched, Who: cv.label(ctx, e.By),
				Text: fmt.Sprintf("%s %s: %s %s → %s", w.ID, w.Title, e.Field, orNone(e.From), orNone(e.To)), At: at})
		}
	}
	return out
}

// involvedHere: the caller held a task of mission m in this checkout.
func (cv *coordView) involvedHere(m string) bool {
	for _, r := range cv.w.held().List(cv.w.session()) {
		for _, d := range cv.docs {
			if d.Str(docKeyID) == r.Task && d.Str(vkMission) == m {
				return true
			}
		}
	}
	return false
}

// moreItems are the TASK 20 items computed on read (FB-TASK-3): triage,
// child, parent (gone, closed, cycle), deleted claim, mention, recurring.
func (cv *coordView) moreItems(ctx context.Context, me string) []opscoord.InboxItem {
	byID := make(map[string]coord.Doc, len(cv.docs))
	kids := map[string][]coord.Doc{}
	for _, d := range cv.docs {
		byID[d.Str(docKeyID)] = d
		if p := d.Str(vkParent); p != "" {
			kids[p] = append(kids[p], d)
		}
	}
	assigned := func(d coord.Doc) bool {
		return d.Str(fieldTaskLead) == me || slices.Contains(d.Strs(vkAssignees), me)
	}
	mine := func(d coord.Doc) bool { return d.Str(docKeyAuthor) == me || assigned(d) }
	var out []opscoord.InboxItem
	triage := cv.isTriageOwner(ctx, me)
	for _, d := range cv.docs {
		id, title, mission := d.Str(docKeyID), d.Str(fieldTitle), d.Str(vkMission)
		if triage && docUnfinished(d) && d.Str(vkCommitment) == proposed {
			out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxTriage, Who: cv.label(ctx, d.Str(docKeyAuthor)), At: d.Time(docKeyCreatedAt),
				Text: fmt.Sprintf("%s %s: proposed; triage it (lore task accept|decline %s)", id, title, id)})
		}
		if ch := kids[id]; len(ch) > 0 && mine(d) {
			out = append(out, cv.childItems(ctx, d, ch, me)...)
		}
		if p := d.Str(vkParent); p != "" && docUnfinished(d) && assigned(d) {
			if pd, ok := byID[p]; !ok {
				out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxParent,
					Text: fmt.Sprintf("%s %s: its parent %s is gone (in the trash or not attached); lore task edit %s --no-parent", id, title, p, id)})
			} else if !docUnfinished(pd) {
				out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxParent, At: pd.Time(vkCompletedAt),
					Text: fmt.Sprintf("%s %s: its parent %s %s is %s while this is open", id, title, p, pd.Str(fieldTitle), pd.Status())})
			}
		}
		if it, ok := cv.recurringItem(ctx, d, me, assigned(d)); ok {
			out = append(out, it)
		}
	}
	out = append(out, cycleItems(cv.docs, mine)...)
	out = append(out, cv.deletedClaimItems(byID, me)...)
	out = append(out, cv.mentionItems(ctx, byID, me)...)
	return out
}

// childItems: each sub-task finished by someone else, and the "close it"
// line when an open parent has no open sub-task left.
func (cv *coordView) childItems(ctx context.Context, parent coord.Doc, ch []coord.Doc, me string) []opscoord.InboxItem {
	var out []opscoord.InboxItem
	open := 0
	pid, mission := parent.Str(docKeyID), parent.Str(vkMission)
	for _, c := range ch {
		if docUnfinished(c) {
			open++
			continue
		}
		by := c.Str(vkDoneBy)
		if by == me {
			continue
		}
		out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxChild, Who: cv.label(ctx, by), At: c.Time(vkCompletedAt),
			Text: fmt.Sprintf("%s %s: sub-task %s %s is %s", pid, parent.Str(fieldTitle), c.Str(docKeyID), c.Str(fieldTitle), c.Status())})
	}
	if open == 0 && docUnfinished(parent) {
		out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxChild,
			Text: fmt.Sprintf("%s %s: no open sub-tasks left; close it (lore task done %s)", pid, parent.Str(fieldTitle), pid)})
	}
	return out
}

// recurringItem: an open recurring instance (index > 1) assigned to me,
// created by someone else (TASK 10).
func (cv *coordView) recurringItem(ctx context.Context, d coord.Doc, me string, assigned bool) (opscoord.InboxItem, bool) {
	if !assigned || !docUnfinished(d) || d.Str(docKeyAuthor) == me {
		return opscoord.InboxItem{}, false
	}
	rc, ok := d[vkRecur].AsObject()
	if !ok || coord.Doc(rc).Int(recurIndex) < 2 {
		return opscoord.InboxItem{}, false
	}
	due := ""
	if t := d.Time(vkDueAt); !t.IsZero() {
		due = " (due " + t.Format(dueLayout) + ")"
	}
	return opscoord.InboxItem{Mission: d.Str(vkMission), Kind: inboxRecurring, Who: cv.label(ctx, d.Str(docKeyAuthor)), At: d.Time(docKeyCreatedAt),
		Text: fmt.Sprintf("%s %s: next recurring instance created for you%s", d.Str(docKeyID), d.Str(fieldTitle), due)}, true
}

// cycleItems: one parent item per parent cycle holding an open task of
// mine (6.13, P3-TASK-CYCLE-1).
func cycleItems(docs []coord.Doc, mine func(coord.Doc) bool) []opscoord.InboxItem {
	byID := make(map[string]coord.Doc, len(docs))
	nodes := make([]tasktree.Node, 0, len(docs))
	for _, d := range docs {
		id := d.Str(docKeyID)
		byID[id] = d
		st := tasktree.Open
		switch d.Status() {
		case schema.TaskStatusDone:
			st = tasktree.Done
		case schema.TaskStatusCancelled:
			st = tasktree.Cancelled
		}
		nodes = append(nodes, tasktree.Node{ID: id, Parent: d.Str(vkParent), State: st})
	}
	var out []opscoord.InboxItem
	for _, cyc := range tasktree.Build(nodes).Cycles() {
		mission, hit := "", false
		for _, id := range cyc {
			if d := byID[id]; docUnfinished(d) && mine(d) {
				mission, hit = d.Str(vkMission), true
				break
			}
		}
		if !hit {
			continue
		}
		top := cyc[0]
		out = append(out, opscoord.InboxItem{Mission: mission, Kind: inboxParent,
			Text: fmt.Sprintf("parent cycle %s%s%s: %s reads as top level; lore task tree check --fix (or lore task edit %s --parent <id> | --no-parent)",
				strings.Join(cyc, cycleArrow), cycleArrow, top, top, top)})
	}
	return out
}

// deletedClaimItems: my claims whose task is no longer a live row (6.12).
func (cv *coordView) deletedClaimItems(byID map[string]coord.Doc, me string) []opscoord.InboxItem {
	if !cv.hasBoard {
		return nil
	}
	var out []opscoord.InboxItem
	for p, s := range cv.board.Board {
		if s.Kind != signals.KindClaim || s.Holder != me || p != signals.ClaimPath(cv.w.folder, s.Task) || coord.IsMission(s.Task) {
			continue
		}
		if _, live := byID[s.Task]; live {
			continue
		}
		out = append(out, opscoord.InboxItem{Kind: inboxDeleted, At: s.Since,
			Text: fmt.Sprintf("%s: the task you claimed was deleted; stop work on it and release the claim (lore task release %s)", s.Task, s.Task)})
	}
	return out
}

// mentionItems: "@me" in a task body or a comment on a task, written by
// someone else; one item per task (the newest).
func (cv *coordView) mentionItems(ctx context.Context, byID map[string]coord.Doc, me string) []opscoord.InboxItem {
	re := cv.mentionPattern(ctx, me)
	if re == nil {
		return nil
	}
	seen := map[string]opscoord.InboxItem{}
	add := func(task coord.Doc, it opscoord.InboxItem) {
		id := task.Str(docKeyID)
		if prev, ok := seen[id]; ok && !it.At.After(prev.At) {
			return
		}
		it.Mission = task.Str(vkMission)
		seen[id] = it
	}
	for _, d := range cv.docs {
		if d.Str(docKeyAuthor) != me && re.MatchString(d.Str(vkBody)) {
			add(d, opscoord.InboxItem{Kind: inboxMention, Who: cv.label(ctx, d.Str(docKeyAuthor)), At: d.Time(docKeyCreatedAt),
				Text: fmt.Sprintf("%s %s: you are mentioned in the description", d.Str(docKeyID), d.Str(fieldTitle))})
		}
	}
	for _, src := range cv.a.Stores() {
		rows, err := cv.a.List(ctx, src, schema.TableComments)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			c, derr := docOfRow(r)
			if derr != nil || c.Str(vkEntityTable) != string(schema.TableTasks) || c.Str(docKeyAuthor) == me || !re.MatchString(c.Str(vkBody)) {
				continue
			}
			task, ok := byID[c.Str(vkEntityID)]
			if !ok {
				continue
			}
			add(task, opscoord.InboxItem{Kind: inboxMention, Who: cv.label(ctx, c.Str(docKeyAuthor)), At: c.Time(docKeyCreatedAt),
				Text: fmt.Sprintf("%s %s: you are mentioned in a comment", task.Str(docKeyID), task.Str(fieldTitle))})
		}
	}
	out := make([]opscoord.InboxItem, 0, len(seen))
	for _, it := range seen {
		out = append(out, it)
	}
	return out
}

// myNames are the names an @mention or tasks.triage_owner may use for me.
func (cv *coordView) myNames(ctx context.Context, me string) []string {
	names := []string{strings.ToLower(me)}
	if _, r, ok, err := cv.a.locateRow(ctx, schema.TableActors, me); err == nil && ok {
		if d, derr := docOfRow(r); derr == nil {
			for _, k := range []string{vkHandle, vkLabel, vkDisplayName} {
				if v := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d.Str(k)), rosterHandleMark)); v != "" && !strings.ContainsAny(v, " \t") {
					names = append(names, v)
				}
			}
		}
	}
	if l := strings.ToLower(strings.TrimPrefix(cv.label(ctx, me), rosterHandleMark)); l != "" && !strings.ContainsAny(l, " \t") {
		names = append(names, l)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// mentionPattern matches "@<one of my names>" as a whole word, any case.
func (cv *coordView) mentionPattern(ctx context.Context, me string) *regexp.Regexp {
	names := cv.myNames(ctx, me)
	alts := make([]string, 0, len(names))
	for _, n := range names {
		alts = append(alts, regexp.QuoteMeta(n))
	}
	re, err := regexp.Compile(`(?i)(^|[^\w@])@(` + strings.Join(alts, "|") + `)\b`)
	if err != nil {
		return nil
	}
	return re
}

// isTriageOwner: tasks.triage_owner names me.
func (cv *coordView) isTriageOwner(ctx context.Context, me string) bool {
	set, ok := cv.policySet(ctx)
	if !ok {
		return false
	}
	ref, isSet := set.Text(policy.KeyTasksTriageOwner)
	if !isSet {
		return false
	}
	return slices.Contains(cv.myNames(ctx, me), strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ref), rosterHandleMark)))
}

// metaReviewItems is the review-queue line (AGENT_IDENTITY 8.3): no time,
// so --ack and --since never hide it. A read error hides the line only.
func (cv *coordView) metaReviewItems(ctx context.Context) []opscoord.InboxItem {
	keys, err := opssettings.UnreviewedKeys(ctx, cv.a)
	if err != nil || len(keys) == 0 {
		return nil
	}
	return []opscoord.InboxItem{{Kind: inboxMetadata, Text: metaReviewText(keys, func(id string) string { return cv.keyAuthor(ctx, id) })}}
}

// metaReviewText is the review-queue line (METADATA 3.14 sample, v0.1.17
// text): the count of distinct key names, their distinct authors in
// first-seen order, each name once, and the command to review them.
func metaReviewText(keys []opssettings.UnreviewedKey, author func(id string) string) string {
	var names, who []string
	for _, k := range keys {
		if !slices.Contains(names, k.Name) {
			names = append(names, k.Name)
		}
		if k.CreatedBy == "" {
			continue
		}
		if a := author(k.CreatedBy); a != "" && !slices.Contains(who, a) {
			who = append(who, a)
		}
	}
	noun := "keys await"
	if len(names) == 1 {
		noun = "key awaits"
	}
	by := ""
	if len(who) > 0 {
		by = " (added by " + strings.Join(who, ", ") + ")"
	}
	return fmt.Sprintf("%d %s review%s: %s — %s", len(names), noun, by, strings.Join(names, ", "), metaReviewCmd)
}

// keyAuthor names a key's writer: an agent actor's own label, else the
// usual label.
func (cv *coordView) keyAuthor(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if _, r, ok, err := cv.a.locateRow(ctx, schema.TableActors, id); err == nil && ok {
		if d, derr := docOfRow(r); derr == nil && d.Str(vkActorKind) == schema.ActorKindAgent && d.Str(vkLabel) != "" {
			return d.Str(vkLabel)
		}
	}
	return cv.label(ctx, id)
}

// ---- mission status ----

// MissionStatus implements opscoord.Host: every task row of the mission
// with its state, the counts per state and per SLA state, and the
// v0.1.17 text table.
func (h standaloneCoord) MissionStatus(ctx context.Context, in opscoord.MissionStatusIn) (opscoord.MissionStatusOut, error) {
	cv, err := h.openView(ctx)
	if err != nil {
		return opscoord.MissionStatusOut{}, err
	}
	rows := engineRows{a: h.a}
	m, ok, err := rows.Get(ctx, in.ID)
	if err != nil {
		return opscoord.MissionStatusOut{}, err
	}
	if !ok || !coord.IsMission(in.ID) {
		return opscoord.MissionStatusOut{}, errs.New(errs.NotFound, "no mission "+in.ID)
	}
	qs := make([]queue.Task, 0, len(cv.docs))
	for _, d := range cv.docs {
		st, by := queue.Unclaimed, ""
		if s, claimed, active := cv.claim(d.Str(docKeyID)); claimed {
			st, by = queue.ClaimExpired, holderName(s)
			if active {
				st = queue.Claimed
			}
		}
		qs = append(qs, coord.ToQueueTask(d, nil, st, by))
	}
	res := queue.Next(queue.Input{Tasks: qs, Filter: queue.Filter{Mission: in.ID, AnyRepoRole: true}, Now: cv.now(), MaxAttempts: cv.w.agents.MaxAttempts,
		Weights: cv.urgencyWeights(ctx)})
	waiting := map[string]queue.Waiting{}
	for _, w := range res.Waiting {
		waiting[w.Task.ID] = w
	}
	ready := map[string]bool{}
	for _, r := range res.Ready {
		ready[r.ID] = true
	}
	hours := map[string]int64{}
	if set, sok := cv.policySet(ctx); sok {
		hours = opstask.SLAHours(set)
	}
	var counts opscoord.StateCounts
	var slaCounts opscoord.SLACounts
	out := []opscoord.MissionRow{}
	for _, d := range cv.docs {
		if d.Str(vkMission) != in.ID {
			continue
		}
		r := cv.missionRow(ctx, d, ready, waiting)
		if withMissionSLA(&r, d, hours, cv.now()) {
			slaCounts.Add(r.SLA)
		}
		counts.Add(r.State)
		out = append(out, r)
	}
	data := opscoord.MissionStatus{Mission: m.Str(docKeyID), Title: m.Str(fieldTitle), Counts: counts, SLA: slaCounts, Tasks: out}
	return opscoord.MissionStatusOut{SchemaVersion: coordSchemaV, Kind: kindTaskList, Count: len(out), Data: data, Lines: missionLines(data)}, nil
}

// withMissionSLA fills mission status's SLA column (TASK 18.4) of r from
// the task's SLA clock: breached past the priority's hours, at_risk from
// 80%, ok before; none for a priority without an SLA or with no policy
// (false: not counted). A late open row's detail also says so.
func withMissionSLA(r *opscoord.MissionRow, d coord.Doc, hours map[string]int64, now time.Time) bool {
	if len(hours) == 0 {
		return false
	}
	c := slaClock(d, watchOf(d).History)
	if r.SLA = opstask.SLAStateOf(c, hours, now); r.SLA == "" {
		return false
	}
	late := r.SLA == opstask.SLAAtRisk || r.SLA == opstask.SLABreached
	if late && r.State != opscoord.StateDone && r.State != opscoord.StateCancelled {
		r.Detail = strings.TrimSpace(r.Detail + " " + fmt.Sprintf("SLA %s (%s, %dh)", strings.ReplaceAll(r.SLA, "_", " "), c.Priority, hours[c.Priority]))
	}
	return true
}

// missionRow is one task's line of mission status.
func (cv *coordView) missionRow(ctx context.Context, d coord.Doc, ready map[string]bool, waiting map[string]queue.Waiting) opscoord.MissionRow {
	id := d.Str(docKeyID)
	r := opscoord.MissionRow{ID: id, Title: d.Str(fieldTitle), Repo: d.Str(vkRepo)}
	s, claimed, active := cv.claim(id)
	switch {
	case d.Status() == schema.TaskStatusDone:
		r.State, r.Who = opscoord.StateDone, cv.label(ctx, d.Str(vkDoneBy))
		if t := d.Time(fieldMergedAt); !t.IsZero() {
			r.Detail = "merged " + t.UTC().Format(stateClock)
		} else {
			r.Detail = "not merged yet"
		}
	case d.Status() == schema.TaskStatusCancelled:
		r.State = opscoord.StateCancelled
	case d.Status() == schema.TaskStatusBlocked:
		r.State = opscoord.StateBlocked
	case claimed && active:
		r.State, r.Who, r.Detail = opscoord.StateClaimed, holderName(s), "working (until "+s.Expires.UTC().Format(stateClock)+")"
	case claimed:
		r.State, r.Who = opscoord.StateExpired, holderName(s)
		if b := workBranchOf(d, s); b == "" {
			r.Detail = "work lost: never pushed"
		} else {
			r.Detail = "work branch " + b
		}
	case ready[id]:
		r.State = opscoord.StateReady
	default:
		w := waiting[id]
		r.State, r.Detail = string(w.Kind), w.Reason
		if w.Kind == queue.WaitGate {
			r.State = opscoord.StateGated
		}
	}
	if used, per := cv.leaseTime(d); used > 0 || docHas(d, vkTimebox) {
		r.Detail = strings.TrimSpace(r.Detail + " " + cv.timeboxText(ctx, d, used, per))
	}
	return r
}

// urgencyWeights are the repo's tasks.urgency.* weights (zero = defaults).
func (cv *coordView) urgencyWeights(ctx context.Context) urgency.Weights {
	set, ok := cv.policySet(ctx)
	if !ok {
		return urgency.Weights{}
	}
	w, err := opstask.UrgencyWeights(set)
	if err != nil {
		return urgency.Weights{}
	}
	return w
}

// missionLines is the v0.1.17 text: a summary line and one line per task.
func missionLines(d opscoord.MissionStatus) []string {
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
