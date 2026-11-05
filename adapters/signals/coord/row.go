// Package coord is the task-row half of agent coordination
// (LORE_TEAM_SPEC.md F7 "Contract: the operations", §7.3, §7.4): it joins a
// claim on the signals ref with the task row it fences, so that claim,
// renew, release, holder done/note, human revoke/cancel/reassign, poison
// blocking and `task next` all follow one set of rules.
//
// Why a package of its own (not the CLI): every operation is "decide on the
// signals board AND the row, then push both atomically", and the decision
// must be testable with a real signals ref and a fake row store — no
// database, no cobra. The CLI supplies the row store (live ref or store
// project, through rowroute) and prints the outcomes.
//
// Rows are handled as canonical documents (Doc, a canonjson.Object: the
// exact shape of a .lore/data file held as a closed JSON value, no `any`),
// so this package never depends on an ORM and writes only the fields it
// owns.
package coord

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/schema"
)

// Document keys of a task row this package reads or writes (column names
// of the tasks table, schema/builtin_work.go; they are the wire format of
// .lore/data files).
const (
	keyID          = "id"
	keyTitle       = "title"
	keyBody        = "body"
	keyStatus      = "status"
	keyPriority    = "priority"
	keyCreatedAt   = "created_at"
	keyUpdatedAt   = "updated_at"
	keyStartedAt   = "started_at"
	keyCompletedAt = "completed_at"
	keyMissionID   = "mission_id"
	keyRepoID      = "repo_id"
	keyRole        = "role"
	keyAssignee    = "assigned_to_actor_id"
	keyTeam        = "assigned_team"
	keyBlockedBy   = "blocked_by"
	keyEpoch       = "claim_epoch"
	keyAttempts    = "attempts"
	keyClaimLog    = "claim_log"
	keyTimebox     = "timebox_seconds"
	keyGate        = "gate"
	keyApprovedBy  = "approved_by"
	keyApprovedAt  = "approved_at"
	keyApprovedSum = "approved_hash"
	keyDoneBy      = "done_by"
	keyDoneCommit  = "done_commit"
	keyDonePR      = "done_pr"
	keyMergedAt    = "merged_at"
	keyWorkBranch  = "work_branch"
	keyCommitment  = "commitment"
	// Urgency inputs (TASK 13.2, 6.10) read by task next.
	keyParentID = "parent_id"
	keyDueAt    = "due_at"
	keyTagIDs   = "tag_ids"
	// keyDeferredUntil is cleared by done (a finished task is not snoozed).
	keyDeferredUntil = "deferred_until"
)

// commitmentAccepted is the commitment done sets (schema
// TaskCommitmentAccepted: finishing work commits it, as `task done` always did).
const commitmentAccepted = schema.TaskCommitmentAccepted

// docTimeLayout is how timestamps are written in row files. It must equal
// lsync's timeLayout (saas/pkg/aicoder/lsync/codec.go): fixed nine
// fractional digits so string comparison orders them.
const docTimeLayout = "2006-01-02T15:04:05.000000000Z"

// ClaimEvent is the kind of one claim_log entry. Closed set; the spec
// (§7.4 "Claim log") names exactly these, plus ClaimEventExpire (expire.go).
// The values are the v0.1.17 wire format (dbent/schema ClaimEvent).
type ClaimEvent string

// The claim_log events.
const (
	ClaimEventClaim   ClaimEvent = "claim"
	ClaimEventReclaim ClaimEvent = "reclaim"
	ClaimEventRelease ClaimEvent = "release"
	ClaimEventRevoke  ClaimEvent = "revoke"
	ClaimEventApprove ClaimEvent = "approve"
)

// ClaimLogMax bounds tasks.claim_log: the writer keeps only the newest
// ClaimLogMax entries (design principle 1: nothing grows without bound).
const ClaimLogMax = 20

// ClaimLogEntry is one element of tasks.claim_log (newest last, at most
// ClaimLogMax entries). Field names are the v0.1.17 JSON keys.
type ClaimLogEntry struct {
	At          time.Time  `json:"at"`
	Event       ClaimEvent `json:"event"`
	ActorID     string     `json:"actor_id"`
	Session     string     `json:"session,omitempty"`
	PrevActorID string     `json:"prev_actor_id,omitempty"`
	Epoch       int        `json:"epoch"`
}

// DependencyUntil says which state of an upstream task satisfies a
// dependency edge (`--after tsk_30:done` / `:merged`). Closed set.
type DependencyUntil string

// The dependency modes.
const (
	// DependencyUntilDone is satisfied as soon as the upstream task is done.
	DependencyUntilDone DependencyUntil = "done"
	// DependencyUntilMerged is satisfied once the upstream task has
	// merged_at set (its code reached the default branch, F9).
	DependencyUntilMerged DependencyUntil = "merged"
)

// TaskDependency is one element of tasks.blocked_by (the --after edges).
type TaskDependency struct {
	TaskID    string          `json:"task_id"`
	ProjectID string          `json:"project_id,omitempty"`
	Until     DependencyUntil `json:"until"`
}

// Doc is one canonical row document: a canonjson.Object (column name →
// closed JSON value). Convert with canonjson.Object(d) / Doc(o).
type Doc canonjson.Object

// Clone returns a shallow copy safe to modify at the top level.
func (d Doc) Clone() Doc { return Doc(canonjson.Object(d).Clone()) }

// Str returns a string field ("" when absent, null or not a string).
func (d Doc) Str(k string) string { return canonjson.Object(d).Str(k) }

// Int returns an integer field (0 when absent or not a number). A
// fractional literal truncates toward zero, as v0.1.17's float64 case did.
func (d Doc) Int(k string) int {
	lit, ok := d[k].AsNumber()
	if !ok {
		return 0
	}
	if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return int(n)
	}
	if f, err := strconv.ParseFloat(lit, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return int(f)
	}
	return 0
}

// Time returns a timestamp field (zero when absent or unparsable).
func (d Doc) Time(k string) time.Time {
	s := d.Str(k)
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{docTimeLayout, time.RFC3339Nano} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Strs returns a list-of-strings field (tag_ids); non-strings are skipped.
func (d Doc) Strs(k string) []string {
	arr, _ := d[k].AsArray()
	var out []string
	for _, e := range arr {
		if s, ok := e.AsString(); ok {
			out = append(out, s)
		}
	}
	return out
}

// SetInt sets an integer field.
func (d Doc) SetInt(k string, n int) { d[k] = canonjson.Int(int64(n)) }

// SetTime sets a timestamp field in the file layout.
func (d Doc) SetTime(k string, t time.Time) { d[k] = canonjson.String(t.UTC().Format(docTimeLayout)) }

// SetStr sets a string field; "" clears it (null), as an optional column.
func (d Doc) SetStr(k, v string) {
	if v == "" {
		d[k] = canonjson.Null()
		return
	}
	d[k] = canonjson.String(v)
}

// SetNull clears a field to null.
func (d Doc) SetNull(k string) { d[k] = canonjson.Null() }

// jsonValue converts a Go struct or slice to the closed JSON value a row
// keeps, so a JSON column round-trips byte-exactly through canonjson.
func jsonValue[T any](v T) (canonjson.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return canonjson.Null(), err
	}
	return canonjson.Raw(b)
}

// fromJSONValue decodes a JSON column value into out; null or absent
// leaves out unchanged.
func fromJSONValue[T any](v canonjson.Value, out *T) error {
	if v.IsNull() {
		return nil
	}
	text, err := canonjson.CompactValue(v)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(text), out)
}

// ClaimLog returns the row's claim log.
func (d Doc) ClaimLog() []ClaimLogEntry {
	var log []ClaimLogEntry
	_ = fromJSONValue(d[keyClaimLog], &log) // a malformed log reads as empty, as in v0.1.17
	return log
}

// AppendClaimLog adds one entry, keeping the newest ClaimLogMax
// (design principle 1: bounded). Every entry written keeps its session
// only as a lore session ref (claimLogSession), the older entries too, so
// a log a v0.1.17 binary or an earlier build wrote is cleaned on the next
// event.
func (d Doc) AppendClaimLog(e ClaimLogEntry) error {
	log := append(d.ClaimLog(), e)
	if len(log) > ClaimLogMax {
		log = log[len(log)-ClaimLogMax:]
	}
	for i := range log {
		log[i].Session = claimLogSession(log[i].Session)
	}
	v, err := jsonValue(log)
	if err != nil {
		return fmt.Errorf("coord: encode claim_log: %w", err)
	}
	d[keyClaimLog] = v
	return nil
}

// claimLogSession is the session a claim_log entry may carry: lore's own
// `ses_` id, else nothing. The caller's signal session is a raw value
// (LORE_SESSION or a tool's session id for an agent, `<stable_key>@<host>`
// for a person); claim_log is a synced row field, and a tool session id
// never leaves the machine (AGENT_IDENTITY 6.2 + 9, OQ14, R11; TIME D8 does
// the same for time entries, closeClaimSpan). Writing the raw value made
// every landing of an agent-closed task fail E_LEAK_BLOCKED at the
// session-id rule (BUG-LAND-SESSION, P3-SEC-LAND-1). The holder's session
// stays on the signals ref, where fencing reads it.
func claimLogSession(s string) string {
	if strings.HasPrefix(s, sessionRefPrefix) {
		return s
	}
	return ""
}

// BlockedBy returns the row's dependency edges.
func (d Doc) BlockedBy() []TaskDependency {
	var deps []TaskDependency
	_ = fromJSONValue(d[keyBlockedBy], &deps) // malformed edges read as none, as in v0.1.17
	return deps
}

// SetBlockedBy replaces the dependency edges (nil clears them).
func (d Doc) SetBlockedBy(deps []TaskDependency) error {
	if len(deps) == 0 {
		d.SetNull(keyBlockedBy)
		return nil
	}
	v, err := jsonValue(deps)
	if err != nil {
		return fmt.Errorf("coord: encode blocked_by: %w", err)
	}
	d[keyBlockedBy] = v
	return nil
}

// Status returns the row's status.
func (d Doc) Status() string { return d.Str(keyStatus) }

// Epoch returns claim_epoch.
func (d Doc) Epoch() int { return d.Int(keyEpoch) }

// touch stamps updated_at.
func (d Doc) touch(now time.Time) { d.SetTime(keyUpdatedAt, now) }
