// Package timeentry is one closed span of work on a task: the items of the
// task row's time_entries list (TIME_TRACKING_DESIGN 6.2, 7.1).
//
// Why on the row: time entries travel with the task (live ref, main, a
// move to another store), so every machine sees the same total. Runs stay
// machine-local (Q-TIME): a run that ends writes its span here as an entry
// of kind agent, and the run id survives only as LocalRef.
//
// Layer L0: imports stdlib and model only.
//
// Invariants:
//   - ID is permanent and never reused; it is the merge key of the list
//     (logic/merge StrategyListByID): same id on both sides, the newer
//     UpdatedAt wins for the whole entry.
//   - An entry is never physically removed: removal sets DeletedAt (a
//     tombstone), and a tombstoned entry never counts toward a total
//     (TQ4: no time-entry prune, removed entries stay tombstones for good).
//   - Seconds is stored, not recomputed, so every reader agrees.
package timeentry

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Tag tags ids of time entries (prefix "tme").
type Tag struct{}

// Prefix implements ids.Tag.
func (Tag) Prefix() ids.Prefix { return ids.PrefixTimeEntry }

// Kind is who did the work: the closed set the totals break down by.
type Kind string

// Kinds of work.
const (
	// KindAgent is work by an agent actor (from runs and claims).
	KindAgent Kind = "agent"
	// KindPerson is work by a person (from status spans).
	KindPerson Kind = "person"
	// KindManual is time logged by hand (lore task time log).
	KindManual Kind = "manual"
)

// Kinds lists every Kind.
func Kinds() []Kind { return []Kind{KindAgent, KindPerson, KindManual} }

// Source is the event that produced the entry (design section 4).
type Source string

// Sources of time.
const (
	// SourceStatus is a closed status span (a person in_progress).
	SourceStatus Source = "status"
	// SourceClaim is a closed claim span.
	SourceClaim Source = "claim"
	// SourceRun is an ended or cancelled run.
	SourceRun Source = "run"
	// SourceManual is a hand-logged entry.
	SourceManual Source = "manual"
)

// Sources lists every Source.
func Sources() []Source { return []Source{SourceStatus, SourceClaim, SourceRun, SourceManual} }

// TimeLayout is the text form of every time in an entry: UTC RFC3339 with
// milliseconds, like HLC walls.
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// dayLayout is the calendar day form of Entry.Day.
const dayLayout = "2006-01-02"

// Entry is one closed span. Times are TimeLayout text ("" when unset) so
// the stored JSON is byte-stable across machines.
type Entry struct {
	ID     ids.ID[Tag] `json:"id"`
	Kind   Kind        `json:"kind"`
	Source Source      `json:"source"`
	// ActorID is the worker (person or agent actor).
	ActorID ids.ID[ids.Actor] `json:"actor_id"`
	// LocalRef is the id of the machine-local thing that produced the
	// entry (the run id for SourceRun). Meaningful only on the machine that
	// wrote it: runs never travel, so another clone cannot resolve it.
	LocalRef string `json:"local_ref,omitempty"`
	// SessionRef is lore's ses_ id of the agent session (D8: never the
	// tool's own session id); empty for people and manual entries. With
	// ActorID it names the worker (design 5.1 rule 3).
	SessionRef string `json:"session_ref,omitempty"`
	// SourceID is the claim token prefix of a claim entry (design 6.2).
	SourceID string `json:"source_id,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	// Day is the calendar day of the work (UTC for auto entries).
	Day     string   `json:"day,omitempty"`
	Seconds int64    `json:"seconds"`
	Note    string   `json:"note,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	// Orig keeps the times of an auto entry before its first edit (6.2).
	Orig      Orig   `json:"orig,omitzero"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedAt string `json:"updated_at"`
	UpdatedBy string `json:"updated_by,omitempty"`
	DeletedAt string `json:"deleted_at,omitempty"`
	DeletedBy string `json:"deleted_by,omitempty"`
}

// Deleted reports a tombstoned entry.
func (e Entry) Deleted() bool { return e.DeletedAt != "" }

// Span is the input of NewSpan: a closed span of one worker.
type Span struct {
	Kind     Kind
	Source   Source
	Actor    ids.ID[ids.Actor]
	Start    time.Time
	End      time.Time
	LocalRef string
	Note     string
	// Now stamps created_at/updated_at; zero means End.
	Now time.Time
}

// NewSpan builds a fresh entry for s. It refuses an unknown kind or
// source, a missing actor, and an end before the start.
func NewSpan(s Span) (Entry, error) {
	if !known(s.Kind, Kinds()) {
		return Entry{}, errs.New(errs.InvalidInput, "time entry: unknown kind "+strconv.Quote(string(s.Kind)))
	}
	if !known(s.Source, Sources()) {
		return Entry{}, errs.New(errs.InvalidInput, "time entry: unknown source "+strconv.Quote(string(s.Source)))
	}
	if s.Actor.IsZero() {
		return Entry{}, errs.New(errs.InvalidInput, "time entry: no actor")
	}
	if s.Start.IsZero() || s.End.Before(s.Start) {
		return Entry{}, errs.New(errs.InvalidInput, "time entry: the span ends before it starts")
	}
	id, err := ids.New[Tag]()
	if err != nil {
		return Entry{}, err
	}
	now := s.Now
	if now.IsZero() {
		now = s.End
	}
	stamp := now.UTC().Format(TimeLayout)
	return Entry{
		ID: id, Kind: s.Kind, Source: s.Source, ActorID: s.Actor, LocalRef: s.LocalRef,
		Start: s.Start.UTC().Format(TimeLayout), End: s.End.UTC().Format(TimeLayout),
		Day:     s.Start.UTC().Format(dayLayout),
		Seconds: int64(s.End.Sub(s.Start) / time.Second), Note: s.Note,
		CreatedAt: stamp, CreatedBy: s.Actor.String(), UpdatedAt: stamp, UpdatedBy: s.Actor.String(),
	}, nil
}

// known reports whether v is one of set.
func known[T comparable](v T, set []T) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// Decode parses a time_entries list (JSON array). Empty input is no entries.
func Decode(raw []byte) ([]Entry, error) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []Entry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "time_entries is not a list of time entries", err)
	}
	return out, nil
}

// Encode renders a time_entries list as a JSON array.
func Encode(list []Entry) ([]byte, error) {
	if list == nil {
		list = []Entry{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode time_entries", err)
	}
	return b, nil
}

// Totals is the summed seconds of the live entries of a list.
type Totals struct {
	All    int64
	ByKind map[Kind]int64
}

// Sum adds up the entries that are not tombstoned. No overlap removal yet
// (design section 5 is a later step): each entry counts in full.
func Sum(list []Entry) Totals {
	t := Totals{ByKind: map[Kind]int64{}}
	for _, e := range list {
		if e.Deleted() {
			continue
		}
		t.All += e.Seconds
		t.ByKind[e.Kind] += e.Seconds
	}
	return t
}

// FormatSeconds renders a length like "1h 23m" ("45s" under a minute,
// "0m" for none).
func FormatSeconds(s int64) string {
	if s <= 0 {
		return "0m"
	}
	if s < 60 {
		return strconv.FormatInt(s, 10) + "s"
	}
	h, m := s/3600, (s%3600)/60
	if h == 0 {
		return strconv.FormatInt(m, 10) + "m"
	}
	return strconv.FormatInt(h, 10) + "h " + strconv.FormatInt(m, 10) + "m"
}

// Orig is an auto entry's span before its first `lore task time edit`.
type Orig struct {
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Seconds int64  `json:"seconds"`
}

// Flags of an entry (design 6.2), snake_case, a closed set.
const (
	FlagLongSpan       = "long_span"
	FlagEstimatedEnd   = "estimated_end"
	FlagClockSkew      = "clock_skew"
	FlagEdited         = "edited"
	FlagClosedByExpiry = "closed_by_expiry"
	// FlagCoalesced marks the tombstone of a span merged into an earlier
	// entry of the same worker at write time (TQ4); it is never restored.
	FlagCoalesced = "coalesced"
)

// HasFlag reports whether e carries flag f.
func (e Entry) HasFlag(f string) bool {
	for _, x := range e.Flags {
		if x == f {
			return true
		}
	}
	return false
}

// WithFlag returns e with flag f added once.
func (e Entry) WithFlag(f string) Entry {
	if !e.HasFlag(f) {
		e.Flags = append(append([]string(nil), e.Flags...), f)
	}
	return e
}

// StartTime parses Start (zero when unset or bad).
func (e Entry) StartTime() time.Time { return parseTime(e.Start) }

// EndTime parses End (zero when unset or bad).
func (e Entry) EndTime() time.Time { return parseTime(e.End) }

// parseTime reads a TimeLayout (or any RFC3339) text; "" or bad is zero.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Open is one open status span of the task row's time_open list (design
// 4.1): one per person working on the task. Closing moves its id into
// time_entries, so the merge result is the same whichever side closed it.
type Open struct {
	ID      ids.ID[Tag]       `json:"id"`
	Source  Source            `json:"source"`
	ActorID ids.ID[ids.Actor] `json:"actor_id"`
	Start   string            `json:"start"`
	// UpdatedAt is the merge stamp of logic/merge StrategyListByID.
	UpdatedAt string `json:"updated_at"`
}

// StartTime parses Start.
func (o Open) StartTime() time.Time { return parseTime(o.Start) }

// DecodeOpen parses a time_open list. Empty input is no spans.
func DecodeOpen(raw []byte) ([]Open, error) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []Open
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "time_open is not a list of open spans", err)
	}
	return out, nil
}

// EncodeOpen renders a time_open list as a JSON array.
func EncodeOpen(list []Open) ([]byte, error) {
	if list == nil {
		list = []Open{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode time_open", err)
	}
	return b, nil
}

// Close turns an open status span into a closed entry ending at end,
// keeping the span's id (design 7.2: the id moves from time_open to
// time_entries). An end before the start counts 0 and is flagged
// clock_skew (11.3: never negative). by stamps the audit fields.
func (o Open) Close(end time.Time, by ids.ID[ids.Actor]) Entry {
	start := o.StartTime()
	secs := int64(0)
	var flags []string
	if end.Before(start) {
		flags = []string{FlagClockSkew}
		end = start
	} else {
		secs = int64(end.Sub(start) / time.Second)
	}
	stamp := end.UTC().Format(TimeLayout)
	who := by.String()
	if by.IsZero() {
		who = o.ActorID.String()
	}
	return Entry{
		ID: o.ID, Kind: KindPerson, Source: o.Source, ActorID: o.ActorID,
		Start: start.UTC().Format(TimeLayout), End: stamp, Day: start.UTC().Format(dayLayout),
		Seconds: secs, Flags: flags,
		CreatedAt: stamp, CreatedBy: who, UpdatedAt: stamp, UpdatedBy: who,
	}
}

// NewOpen opens a status span for actor at start.
func NewOpen(actor ids.ID[ids.Actor], start time.Time) (Open, error) {
	if actor.IsZero() {
		return Open{}, errs.New(errs.InvalidInput, "time span: no actor")
	}
	id, err := ids.New[Tag]()
	if err != nil {
		return Open{}, err
	}
	s := start.UTC().Format(TimeLayout)
	return Open{ID: id, Source: SourceStatus, ActorID: actor, Start: s, UpdatedAt: s}, nil
}

// DayLayout is the calendar-day form of Entry.Day ("2006-01-02").
const DayLayout = dayLayout
