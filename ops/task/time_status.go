package task

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Time tracking on the core (TIME_TRACKING_DESIGN 4-7, OQ4):
//
//	start/edit/done/cancel/reopen ──► setStatus (D2, the ONE status writer)
//	    person → in_progress: time_open += span      agent: hint only (TQ6)
//	    leaving in_progress: time_open → time_entries (closeInto: TQ2 flag, TQ4 coalesce)
//	task time stop ──► stopSpan (own span only, status unchanged)
//	task time show ──► TimeEntries + TimeOpen ──► logic/timecalc.Compute
//	task time log|edit|rm|restore ──► time_entries (tombstones, D14/D15)

// FieldTimeOpen is the task row's list of open status spans (design 4.1).
const FieldTimeOpen = "time_open"

// Time policy defaults, in minutes like the policy keys (OV7-1); used when
// the surface has no policy reader.
const (
	defaultLongSpanMin    = 600
	defaultCoalesceGapMin = 2
	defaultMaxEntryMin    = 1440
)

// agentStartHint is printed when an agent starts a task (TQ6).
const agentStartHint = "  hint: agents get time from claims and runs; this start adds no time. Claim the task to count time: lore task claim "

// timePolicy is the effective time.* keys at target.
type timePolicy struct {
	longSpan, coalesceGap, maxEntry time.Duration
}

// timePolicyAt reads time.long_span / coalesce_gap / max_entry (minutes).
func (o ops) timePolicyAt(ctx context.Context, target store.Source) (timePolicy, error) {
	tp := timePolicy{defaultLongSpanMin * time.Minute, defaultCoalesceGapMin * time.Minute, defaultMaxEntryMin * time.Minute}
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return tp, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return timePolicy{}, err
	}
	for _, k := range []struct {
		key policy.Key
		dst *time.Duration
	}{{policy.KeyTimeLongSpan, &tp.longSpan}, {policy.KeyTimeCoalesceGap, &tp.coalesceGap}, {policy.KeyTimeMaxEntry, &tp.maxEntry}} {
		if n, isSet := set.Limit(k.key); isSet {
			*k.dst = time.Duration(n) * time.Minute
		}
	}
	return tp, nil
}

// caller is the acting actor and whether it is an agent (TQ6). A surface
// without an identity has no caller: status changes then add no spans.
type caller struct {
	id    ids.ID[ids.Actor]
	agent bool
}

// callerOf resolves the caller from the Env (Actorer) and its actor row.
func (o ops) callerOf(ctx context.Context) caller {
	a, ok := o.env.(Actorer)
	if !ok {
		return caller{}
	}
	id, err := a.Actor(ctx)
	if err != nil || id.IsZero() {
		return caller{}
	}
	c := caller{id: id}
	if r, ok := o.actorRow(ctx, id.String()); ok {
		c.agent = work.Field(r, "kind") == schema.ActorKindAgent
	}
	return c
}

// actorRow finds an actor row by id in any attached store.
func (o ops) actorRow(ctx context.Context, id string) (row.Row, bool) {
	all, err := work.ListAll(ctx, o.env, schema.TableActors, "")
	if err != nil {
		return row.Row{}, false
	}
	for _, s := range all {
		if s.Row.ID.String() == id {
			return s.Row, true
		}
	}
	return row.Row{}, false
}

// TimeOpen decodes the time_open list of a task row, dropping spans whose
// id is already a closed entry (design 7.2, OV7-2: the union merge may
// bring a closed span back; the closed entry wins at read time).
func TimeOpen(r row.Row) []timeentry.Open {
	v := r.Fields[FieldTimeOpen]
	if v == nil || v.Kind() == value.KindNull {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	list, err := timeentry.DecodeOpen(raw)
	if err != nil {
		return nil
	}
	closed := map[string]bool{}
	for _, e := range TimeEntries(r) {
		closed[e.ID.String()] = true
	}
	out := list[:0]
	for _, s := range list {
		if !closed[s.ID.String()] {
			out = append(out, s)
		}
	}
	return out
}

// setStatus is THE status transition of a task (design 4.1, D2): every
// write of the status field in this package goes through it (a source
// scan, time_status_test.go, refuses any other). It puts status into patch
// and keeps the time fields true:
//
//	→ in_progress by a person      open a status span for them (if none)
//	→ in_progress by an agent      nothing; hint (TQ6)
//	leaving in_progress            close every open span into time_entries
//	                               (coalesce TQ4, long_span flag TQ2)
//
// cur is the row before the write (zero for a new task); src its store.
// reopen marks a parent reopened by --reopen-parent: the caller works on
// the sub-task, so no span opens on the parent (OV7-3). The returned hint
// is a human line to append ("" for none).
func (o ops) setStatus(ctx context.Context, src store.Source, cur row.Row, status string, reopen bool, patch map[string]value.Value) (string, error) {
	t, err := value.TextOf(status)
	if err != nil {
		return "", err
	}
	patch[work.FieldStatus] = t
	if cur.ID.IsZero() {
		return "", nil
	}
	open := TimeOpen(cur)
	now := opclock.Now(o.env).UTC()
	who := o.callerOf(ctx)
	switch {
	case status == schema.TaskStatusInProgress && reopen:
		return "", nil
	case status == schema.TaskStatusInProgress:
		if who.id.IsZero() {
			return "", nil
		}
		if who.agent {
			return agentStartHint + cur.ID.String() + "\n", nil
		}
		for _, s := range open {
			if s.ActorID == who.id {
				return "", nil
			}
		}
		s, oerr := timeentry.NewOpen(who.id, now)
		if oerr != nil {
			return "", oerr
		}
		// OV7-5: no "counting" line, so start keeps v0.1.17's text
		// (TestTextMatchesV017); lore task time shows the open span.
		return "", putOpen(patch, append(open, s))
	case len(open) == 0:
		return "", nil
	}
	tp, err := o.timePolicyAt(ctx, src)
	if err != nil {
		return "", err
	}
	entries := TimeEntries(cur)
	for _, s := range open {
		entries = closeInto(entries, s.Close(now, who.id), tp)
	}
	if err = putEntries(patch, entries); err != nil {
		return "", err
	}
	return "", putOpen(patch, nil)
}

// stopSpan closes only actor's open status span, status unchanged (design
// 4.1 `lore task time stop`). ok is false when actor has none.
func (o ops) stopSpan(ctx context.Context, src store.Source, cur row.Row, actor ids.ID[ids.Actor], patch map[string]value.Value) (timeentry.Entry, bool, error) {
	open := TimeOpen(cur)
	keep := make([]timeentry.Open, 0, len(open))
	var mine *timeentry.Open
	for i := range open {
		if open[i].ActorID == actor && mine == nil {
			mine = &open[i]
			continue
		}
		keep = append(keep, open[i])
	}
	if mine == nil {
		return timeentry.Entry{}, false, nil
	}
	tp, err := o.timePolicyAt(ctx, src)
	if err != nil {
		return timeentry.Entry{}, false, err
	}
	e := mine.Close(opclock.Now(o.env).UTC(), actor)
	entries := closeInto(TimeEntries(cur), e, tp)
	if err = putEntries(patch, entries); err != nil {
		return timeentry.Entry{}, false, err
	}
	return e, true, putOpen(patch, keep)
}

// closeInto appends a closed auto entry, flagging it long_span (TQ2) and
// coalescing it into the worker's latest live auto entry when they are
// closer than time.coalesce_gap (TQ4). A coalesced span is kept as a
// tombstone flagged coalesced with the same id, so the union merge of
// time_open never brings the open span back (OV7-4).
func closeInto(entries []timeentry.Entry, e timeentry.Entry, tp timePolicy) []timeentry.Entry {
	if timecalc.LongSpan(e.Seconds, tp.longSpan) {
		e = e.WithFlag(timeentry.FlagLongSpan)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		p := entries[i]
		if p.Deleted() || p.Source == timeentry.SourceManual || p.ActorID != e.ActorID || p.SessionRef != e.SessionRef || p.Source != e.Source {
			continue
		}
		if !timecalc.Coalesce(p.StartTime(), p.EndTime(), e.StartTime(), e.EndTime(), tp.coalesceGap) {
			break
		}
		start, end := minTime(p.StartTime(), e.StartTime()), maxTime(p.EndTime(), e.EndTime())
		p.Start, p.End = start.UTC().Format(timeentry.TimeLayout), end.UTC().Format(timeentry.TimeLayout)
		p.Seconds = int64(end.Sub(start) / time.Second)
		p.UpdatedAt, p.UpdatedBy = e.UpdatedAt, e.UpdatedBy
		if timecalc.LongSpan(p.Seconds, tp.longSpan) {
			p = p.WithFlag(timeentry.FlagLongSpan)
		}
		entries[i] = p
		e = e.WithFlag(timeentry.FlagCoalesced)
		e.Note = "coalesced into " + p.ID.String()
		e.DeletedAt, e.DeletedBy = e.UpdatedAt, e.UpdatedBy
		break
	}
	return append(entries, e)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// putEntries writes the time_entries patch value.
func putEntries(patch map[string]value.Value, list []timeentry.Entry) error {
	raw, err := timeentry.Encode(list)
	if err != nil {
		return err
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "time_entries", err)
	}
	patch[FieldTimeEntries] = v
	return nil
}

// putOpen writes the time_open patch value.
func putOpen(patch map[string]value.Value, list []timeentry.Open) error {
	raw, err := timeentry.EncodeOpen(list)
	if err != nil {
		return err
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "time_open", err)
	}
	patch[FieldTimeOpen] = v
	return nil
}
