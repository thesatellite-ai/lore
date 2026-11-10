package coord

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/store"
)

// Host is the coordination machinery behind these operations: the signals
// ref (claims, holds, leases, fencing tokens), the roster and the store
// links. It lives in the host (P3-COORD-1: the v0.1.17 signals packages are
// not in this module yet), which supplies one through Env.Coordinator; each
// method runs one verb and returns what it printed in v0.1.17 as typed
// output, never writing to a terminal itself.
type Host interface {
	TaskClaim(ctx context.Context, in ClaimIn) (Report, error)
	TaskHeartbeat(ctx context.Context, in HeartbeatIn) (Report, error)
	TaskMerged(ctx context.Context, in MergedIn) (Report, error)
	TaskNext(ctx context.Context, in NextIn) (NextOut, error)
	TaskNote(ctx context.Context, in NoteIn) (Report, error)
	TaskReassign(ctx context.Context, in ReassignIn) (Report, error)
	// TaskReassignPlan lists what `task reassign --from` would change
	// (checks --assign and the caller first, writes nothing); the op then
	// previews it and reassigns each task by id under one batch (TASK 18.1).
	TaskReassignPlan(ctx context.Context, in ReassignIn) (ReassignPlan, error)
	TaskRelease(ctx context.Context, in ReleaseIn) (Report, error)
	TaskRevoke(ctx context.Context, in RevokeIn) (Report, error)
	HoldClaim(ctx context.Context, in HoldClaimIn) (Report, error)
	HoldRelease(ctx context.Context, in HoldReleaseIn) (Report, error)
	GateRequest(ctx context.Context, in GateIn) (Report, error)
	GateApprove(ctx context.Context, in GateIn) (Report, error)
	Inbox(ctx context.Context, in InboxIn) (InboxOut, error)
	MissionStatus(ctx context.Context, in MissionStatusIn) (MissionStatusOut, error)
}

// Report is the output of a coordination verb that v0.1.17 answered with
// text only: the lines it printed, in order (each without its newline).
type Report struct {
	Lines []string `json:"lines"`
}

// Text implements op.Texter: the lines, exactly as v0.1.17 printed them.
func (r Report) Text() string { return joinLines(r.Lines) }

// joinLines is lines as terminal text, each ending in a newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// ClaimIn is the input of task.claim.
type ClaimIn struct {
	ID         string   `json:"id" arg:"0" desc:"the task or mission id (tsk_…, msn_…)"`
	Owns       []string `json:"owns,omitempty" flag:"owns" desc:"paths this claim owns (directory prefixes ending in / or exact files)"`
	Strict     bool     `json:"strict,omitempty" flag:"strict" desc:"refuse instead of warning when --owns overlaps another claim"`
	Coordinate bool     `json:"coordinate,omitempty" flag:"coordinate" desc:"take a coordinator lease on a parent with open sub-tasks (TASK 6.9)"`
	From       string   `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
}

// HeartbeatIn is the input of task.heartbeat.
type HeartbeatIn struct {
	ID     string `json:"id,omitempty" arg:"0" desc:"the claimed task (tsk_…); leave out to renew every claim of this session"`
	Daemon bool   `json:"daemon,omitempty" flag:"daemon" desc:"keep renewing until the claim ends or the parent exits"`
}

// MergedIn is the input of task.merged.
type MergedIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
	Repo string `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
}

// NextIn is the input of task.next.
type NextIn struct {
	Mission string   `json:"mission,omitempty" flag:"mission" desc:"only tasks of this mission"`
	Repo    string   `json:"repo,omitempty" flag:"repo" desc:"only tasks of this repo (a repo name of the group)"`
	Role    string   `json:"role,omitempty" flag:"role" desc:"plan role (e.g. coordinator)"`
	Owns    []string `json:"owns,omitempty" flag:"owns" desc:"paths the claim owns"`
	Why     bool     `json:"why,omitempty" flag:"why" desc:"explain the urgency score term by term"`
	DryRun  bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"rank and explain without claiming (nothing written)"`
}

// NextOut is the output of task.next. With --dry-run, Ready and Waiting are
// the ranking (TASK 13.3, the v0.1.17 --dry-run --json shape); a claim
// fills Claimed. Lines is the human form either way.
type NextOut struct {
	Ready   []Ranked  `json:"ready"`
	Waiting []Waiting `json:"waiting"`
	DryRun  bool      `json:"dry_run"`
	// Claimed is the task next claimed or resumed ("" with --dry-run).
	Claimed string   `json:"claimed,omitempty"`
	Lines   []string `json:"-"`
}

// Text implements op.Texter.
func (o NextOut) Text() string { return joinLines(o.Lines) }

// Ranked is one ready task of a --dry-run ranking.
type Ranked struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Urgency string         `json:"urgency"`
	Terms   []urgency.Part `json:"terms"`
}

// Waiting is one task of a --dry-run ranking that is not ready, and why.
type Waiting struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// NoteIn is the input of task.note.
type NoteIn struct {
	ID   string   `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Text []string `json:"text" arg:"1" rest:"true" desc:"the note (several words are joined)"`
	// ShareTo is v0.1.17's --to of task note (a json name other than "to",
	// which the runner reads as the target store: the note's place is
	// decided by the host's store links, not by the engine's target).
	ShareTo    string `json:"share_to,omitempty" flag:"to" desc:"where the row goes: repo | <org> | <org>/<folder> | me | me/<org>"`
	Everywhere bool   `json:"everywhere,omitempty" flag:"everywhere" desc:"with --to me: file the note under everywhere/ (loads in every repo)"`
	ForceOrg   bool   `json:"force_org,omitempty" flag:"force-org" desc:"write even though LORE_ORG or lore org use disagrees with a store link"`
	From       string `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
}

// Body is the note text, its words joined by single spaces.
func (in NoteIn) Body() string { return strings.Join(in.Text, " ") }

// ReassignIn is the input of task.reassign.
type ReassignIn struct {
	ID     string `json:"id,omitempty" arg:"0" desc:"the task id (tsk_…); leave out with --from"`
	Assign string `json:"assign,omitempty" flag:"assign" desc:"the new assignee (@handle)"`
	From   string `json:"from,omitempty" flag:"from" desc:"reassign every open task of this person"`
}

// ReassignTask is one task a `task reassign --from` batch changes.
type ReassignTask struct {
	ID    string // tsk_…
	Title string // may be empty
}

// ReassignPlan is what `task reassign --from` would change: the open tasks
// of the --from person, the store their rows live in and the acting
// person's actor id (the batch log names it on every event).
type ReassignPlan struct {
	Tasks []ReassignTask
	Store store.Source
	Actor string // act_…
}

// ReassignOut is the output of task.reassign: the lines v0.1.17 printed,
// the tasks reassigned and, for --from, the batch id every one of them was
// written under (TASK 18.1, TSQ6; "" for a single task).
type ReassignOut struct {
	Lines []string `json:"lines"`
	Batch string   `json:"batch,omitempty"`
	Tasks []string `json:"tasks"`
}

// Text implements op.Texter: the lines.
func (r ReassignOut) Text() string { return joinLines(r.Lines) }

// ReleaseIn is the input of task.release.
type ReleaseIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
}

// RevokeIn is the input of task.revoke.
type RevokeIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
}

// HoldClaimIn is the input of hold.claim.
type HoldClaimIn struct {
	Name string        `json:"name" arg:"0" desc:"the resource to hold (a test suite, a staging slot)"`
	TTL  time.Duration `json:"ttl,omitempty" flag:"ttl" desc:"how long the hold lasts (default: lease.ttl)"`
	On   string        `json:"on,omitempty" flag:"on" desc:"project (the project's signals ref, the default) or machine"`
}

// HoldReleaseIn is the input of hold.release.
type HoldReleaseIn struct {
	Name string `json:"name" arg:"0" desc:"the resource to release"`
}

// GateIn is the input of gate.request and gate.approve.
type GateIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from,omitempty" flag:"from" alias:"store" desc:"the source of the id: repo | <org> | <org>/<folder>"`
}

// InboxIn is the input of inbox.show.
type InboxIn struct {
	Since time.Duration `json:"since,omitempty" flag:"since" desc:"only items newer than this (e.g. 2h)"`
	Ack   bool          `json:"ack,omitempty" flag:"ack" desc:"mark everything shown as seen"`
}

// InboxOut is the output of inbox.show: v0.1.17's JSON envelope
// (schema_version, kind, count, data) around the items shown.
type InboxOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Count         int         `json:"count,omitempty"`
	Data          []InboxItem `json:"data"`
}

// InboxItem is one inbox line.
type InboxItem struct {
	Mission string    `json:"mission"`
	Kind    string    `json:"kind"`
	Who     string    `json:"who,omitempty"`
	Text    string    `json:"text"`
	At      time.Time `json:"at"`
}

// Inbox display constants (v0.1.17 text, kept as is).
const (
	inboxEmpty     = "(inbox empty)"
	inboxNoMission = "-"
)

// Text implements op.Texter: one line per item, or the empty marker.
func (o InboxOut) Text() string {
	if len(o.Data) == 0 {
		return inboxEmpty + "\n"
	}
	var b strings.Builder
	for _, it := range o.Data {
		m := it.Mission
		if m == "" {
			m = inboxNoMission
		}
		b.WriteString(m + "  " + padRight(it.Kind, inboxKindWidth) + " " + padRight(it.Who, inboxWhoWidth) + " " + it.Text + "\n")
	}
	return b.String()
}

// Column widths of the inbox text (v0.1.17's %-8s %-12s).
const (
	inboxKindWidth = 8
	inboxWhoWidth  = 12
)

// padRight pads s with spaces to width (fmt's %-Ns).
func padRight(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// MissionStatusIn is the input of mission.status.
type MissionStatusIn struct {
	ID string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
}

// MissionStatusOut is the output of mission.status: v0.1.17's JSON
// envelope around the mission's rows. Lines is the human table.
type MissionStatusOut struct {
	SchemaVersion int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	Count         int           `json:"count,omitempty"`
	Data          MissionStatus `json:"data"`
	Lines         []string      `json:"-"`
}

// Text implements op.Texter.
func (o MissionStatusOut) Text() string { return joinLines(o.Lines) }

// MissionStatus is the mission at a glance. The fields are in key order:
// v0.1.17 printed a map, so its JSON keys came out sorted.
type MissionStatus struct {
	Counts  StateCounts  `json:"counts"`
	Mission string       `json:"mission"`
	SLA     SLACounts    `json:"sla"`
	Tasks   []MissionRow `json:"tasks"`
	Title   string       `json:"title"`
}

// StateCounts counts the mission's rows per state. v0.1.17 printed a map
// holding only the states that occur, keys sorted: the fields are in that
// order and omitted at zero, so the JSON is the same.
type StateCounts struct {
	Blocked   int `json:"blocked,omitempty"`
	Cancelled int `json:"cancelled,omitempty"`
	Claimed   int `json:"claimed,omitempty"`
	Cycle     int `json:"cycle,omitempty"`
	Done      int `json:"done,omitempty"`
	Expired   int `json:"expired,omitempty"`
	Gated     int `json:"gated,omitempty"`
	Poison    int `json:"poison,omitempty"`
	Ready     int `json:"ready,omitempty"`
	Waiting   int `json:"waiting,omitempty"`
}

// SLACounts counts the mission's rows per SLA state (TASK 18.4), as
// StateCounts does.
type SLACounts struct {
	AtRisk   int `json:"at_risk,omitempty"`
	Breached int `json:"breached,omitempty"`
	OK       int `json:"ok,omitempty"`
}

// MissionRow is one task line of mission.status.
type MissionRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Repo   string `json:"repo,omitempty"`
	State  string `json:"state"`
	Who    string `json:"who,omitempty"`
	Detail string `json:"detail,omitempty"`
	// SLA is ok | at_risk | breached, "" when the priority has no SLA.
	SLA string `json:"sla,omitempty"`
}

// Mission row states (the closed set of MissionRow.State and the keys of
// StateCounts). They are CLI output and JSON keys: never rename one.
const (
	StateBlocked   = "blocked"
	StateCancelled = "cancelled"
	StateClaimed   = "claimed"
	StateCycle     = "cycle"
	StateDone      = "done"
	StateExpired   = "expired"
	StateGated     = "gated"
	StatePoison    = "poison"
	StateReady     = "ready"
	StateWaiting   = "waiting"
)

// field is the counter of state in c, nil for a state outside the set.
func (c *StateCounts) field(state string) *int {
	switch state {
	case StateBlocked:
		return &c.Blocked
	case StateCancelled:
		return &c.Cancelled
	case StateClaimed:
		return &c.Claimed
	case StateCycle:
		return &c.Cycle
	case StateDone:
		return &c.Done
	case StateExpired:
		return &c.Expired
	case StateGated:
		return &c.Gated
	case StatePoison:
		return &c.Poison
	case StateReady:
		return &c.Ready
	case StateWaiting:
		return &c.Waiting
	}
	return nil
}

// Add counts one row in state. A state outside the closed set is a
// programming error upstream; it is not counted rather than invented as a
// new JSON key.
func (c *StateCounts) Add(state string) {
	if p := c.field(state); p != nil {
		*p++
	}
}

// SLA states (MissionRow.SLA, the keys of SLACounts).
const (
	SLAOK       = "ok"
	SLAAtRisk   = "at_risk"
	SLABreached = "breached"
)

// Add counts one row in SLA state st ("" and unknown states are not
// counted).
func (c *SLACounts) Add(st string) {
	switch st {
	case SLAOK:
		c.OK++
	case SLAAtRisk:
		c.AtRisk++
	case SLABreached:
		c.Breached++
	}
}
