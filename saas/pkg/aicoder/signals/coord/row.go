package coord

// row.go — the v0.1.17 map form of a row document, kept so the old
// binary's callers (which build and read rows as map[string]any) need no
// edit. Every operation converts to the root package's closed form at the
// edge (toRoot / fromRoot) and runs there (P3-SIGCOORD-1).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dbent/schema"
	"saas/pkg/aicoder/canonjson"

	root "github.com/thesatellite-ai/lore/adapters/signals/coord"
	rootjson "github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/model/timeentry"
)

// Row keys the map-form accessors below read (same names as the root
// package's; they are the .lore/data wire format).
const (
	keyStatus      = "status"
	keyUpdatedAt   = "updated_at"
	keyBlockedBy   = "blocked_by"
	keyEpoch       = "claim_epoch"
	keyClaimLog    = "claim_log"
	keyTimeEntries = "time_entries"
)

// docTimeLayout is the row-file timestamp layout (root coord docTimeLayout).
const docTimeLayout = "2006-01-02T15:04:05.000000000Z"

// sessionRefPrefix is lore's own session id prefix (root coord).
const sessionRefPrefix = "ses_"

// toRoot converts a map-form document to the root form. Values outside
// the canonjson shell's accepted set (a []string a caller put in, a typed
// struct) take one JSON round trip, the way v0.1.17's encoder saw them.
func toRoot(d Doc) (root.Doc, error) {
	if d == nil {
		return nil, nil
	}
	o, err := canonjson.ToObject(d)
	if err == nil {
		return root.Doc(o), nil
	}
	b, merr := json.Marshal(map[string]any(d))
	if merr != nil {
		return nil, fmt.Errorf("coord: row document: %w", err)
	}
	o, err = rootjson.Decode(b, rootjson.DefaultMaxDepth)
	if err != nil {
		return nil, fmt.Errorf("coord: row document: %w", err)
	}
	return root.Doc(o), nil
}

// fromRoot converts a root document back to the map form (numbers as
// json.Number, lists as []any); nil stays nil.
func fromRoot(d root.Doc) Doc {
	if d == nil {
		return nil
	}
	return Doc(canonjson.FromObject(rootjson.Object(d)))
}

// refill replaces dst's contents with src's, so an in-place mutation on a
// converted copy reaches the document the root operation holds.
func refill[K comparable, V any](dst, src map[K]V) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}

// TimeEntries decodes the row's time_entries (none when absent or bad).
func (d Doc) TimeEntries() []timeentry.Entry {
	var list []timeentry.Entry
	_ = fromJSONValue(d[keyTimeEntries], &list) // a malformed list reads as none, as in the root package
	return list
}

// Doc is one canonical row document.
type Doc map[string]any

// Clone returns a shallow copy safe to modify at the top level.
func (d Doc) Clone() Doc {
	out := make(Doc, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

// Str returns a string field ("" when absent or null).
func (d Doc) Str(k string) string {
	if s, ok := d[k].(string); ok {
		return s
	}
	return ""
}

// Int returns an integer field (0 when absent or not a number).
func (d Doc) Int(k string) int {
	switch v := d[k].(type) {
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
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
	var out []string
	switch v := d[k].(type) {
	case []string:
		out = append(out, v...)
	case []any: // decoded JSON; narrowed element by element
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// SetInt sets an integer field.
func (d Doc) SetInt(k string, n int) { d[k] = json.Number(strconv.Itoa(n)) }

// SetTime sets a timestamp field in the file layout.
func (d Doc) SetTime(k string, t time.Time) { d[k] = t.UTC().Format(docTimeLayout) }

// SetStr sets a string field; "" clears it (null), as an optional column.
func (d Doc) SetStr(k, v string) {
	if v == "" {
		d[k] = nil
		return
	}
	d[k] = v
}

// jsonValue converts a Go value to the generic form canonjson keeps
// (maps, slices, json.Number), so a JSON column round-trips byte-exactly.
func jsonValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// fromJSONValue decodes a generic JSON column value into out.
func fromJSONValue(v any, out any) error {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// ClaimLog returns the row's claim log.
func (d Doc) ClaimLog() []schema.ClaimLogEntry {
	var log []schema.ClaimLogEntry
	_ = fromJSONValue(d[keyClaimLog], &log)
	return log
}

// AppendClaimLog adds one entry, keeping the newest schema.ClaimLogMax
// (design principle 1: bounded). Every entry written keeps its session
// only as a lore session ref (claimLogSession), the older entries too, so
// a log a v0.1.17 binary or an earlier build wrote is cleaned on the next
// event.
func (d Doc) AppendClaimLog(e schema.ClaimLogEntry) error {
	log := append(d.ClaimLog(), e)
	if len(log) > schema.ClaimLogMax {
		log = log[len(log)-schema.ClaimLogMax:]
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
func (d Doc) BlockedBy() []schema.TaskDependency {
	var deps []schema.TaskDependency
	_ = fromJSONValue(d[keyBlockedBy], &deps)
	return deps
}

// SetBlockedBy replaces the dependency edges (nil clears them).
func (d Doc) SetBlockedBy(deps []schema.TaskDependency) error {
	if len(deps) == 0 {
		d[keyBlockedBy] = nil
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
