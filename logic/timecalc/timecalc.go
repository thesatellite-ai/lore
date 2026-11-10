package timecalc

import (
	"cmp"
	"slices"
	"time"
)

// Worker names one worker: a person actor, or an agent actor plus its
// session (design 5.1 rule 3, D5).
type Worker struct {
	Actor   string
	Session string
}

// Label is "actor" or "actor session" for display and stable sorting.
func (w Worker) Label() string {
	if w.Session == "" {
		return w.Actor
	}
	return w.Actor + " " + w.Session
}

// Span is one input span. Auto spans carry Start and End; a manual span
// carries Seconds (and Start/End when logged with a clock time, which
// never joins a union: manual entries are never merged, TQ5).
type Span struct {
	Worker Worker
	// Owner is the person the worker's actor belongs to (the actor itself
	// for a person): the key of the "incl. agents" subtotal (TQ1).
	Owner   string
	Start   time.Time
	End     time.Time
	Seconds int64
	Manual  bool
	// Open marks a span still running (end = read time); counted the same.
	Open bool
}

// WorkerTotal is one worker's line.
type WorkerTotal struct {
	Worker Worker
	Owner  string
	Auto   int64
	Manual int64
	// Last is the latest end among the worker's spans (zero for a
	// date-only manual entry).
	Last time.Time
	Open bool
}

// Total is Auto + Manual.
func (w WorkerTotal) Total() int64 { return w.Auto + w.Manual }

// OwnerTotal is one person's subtotal with their agents (TQ1): Own is
// the person's own time, Agents their agents' summed worker time, and
// Incl the union of the person's and agents' auto spans plus all manual.
type OwnerTotal struct {
	Owner  string
	Own    int64
	Agents int64
	Incl   int64
}

// Result is the time of one task.
type Result struct {
	Effort  int64
	Elapsed int64
	Manual  int64
	Workers []WorkerTotal
	Owners  []OwnerTotal
}

// interval is a half-open [s, e) span in unix seconds.
type interval struct{ s, e int64 }

// union returns the total length of the union of xs (overlapping or
// touching intervals count once, rule 4).
func union(xs []interval) int64 {
	if len(xs) == 0 {
		return 0
	}
	xs = slices.Clone(xs)
	slices.SortFunc(xs, func(a, b interval) int { return cmp.Or(cmp.Compare(a.s, b.s), cmp.Compare(a.e, b.e)) })
	var total int64
	cur := xs[0]
	for _, x := range xs[1:] {
		if x.s <= cur.e {
			cur.e = max(cur.e, x.e)
			continue
		}
		total += cur.e - cur.s
		cur = x
	}
	return total + cur.e - cur.s
}

// autoInterval converts an auto span; an end before its start is empty
// (11.3: counts 0, the caller flags clock_skew).
func autoInterval(s Span) interval {
	a, b := s.Start.Unix(), s.End.Unix()
	if b < a {
		b = a
	}
	return interval{a, b}
}

// Compute applies design 5.1 to spans.
func Compute(spans []Span) Result {
	type acc struct {
		w      WorkerTotal
		ivs    []interval
		manual int64
	}
	byWorker := map[Worker]*acc{}
	ownerIvs := map[string][]interval{}
	ownerManual := map[string]int64{}
	var all []interval
	var res Result
	for _, s := range spans {
		a := byWorker[s.Worker]
		if a == nil {
			a = &acc{w: WorkerTotal{Worker: s.Worker, Owner: s.Owner}}
			byWorker[s.Worker] = a
		}
		if s.End.After(a.w.Last) {
			a.w.Last = s.End
		}
		a.w.Open = a.w.Open || s.Open
		if s.Manual {
			a.manual += s.Seconds
			ownerManual[s.Owner] += s.Seconds
			res.Manual += s.Seconds
			continue
		}
		iv := autoInterval(s)
		a.ivs = append(a.ivs, iv)
		ownerIvs[s.Owner] = append(ownerIvs[s.Owner], iv)
		all = append(all, iv)
	}
	owners := map[string]*OwnerTotal{}
	for _, a := range byWorker {
		a.w.Auto = union(a.ivs)
		a.w.Manual = a.manual
		res.Effort += a.w.Total()
		res.Workers = append(res.Workers, a.w)
		o := owners[a.w.Owner]
		if o == nil {
			o = &OwnerTotal{Owner: a.w.Owner}
			owners[a.w.Owner] = o
		}
		if a.w.Worker.Actor == a.w.Owner {
			o.Own += a.w.Total()
		} else {
			o.Agents += a.w.Total()
		}
	}
	for k, o := range owners {
		o.Incl = union(ownerIvs[k]) + ownerManual[k]
		res.Owners = append(res.Owners, *o)
	}
	res.Elapsed = union(all)
	slices.SortFunc(res.Workers, func(a, b WorkerTotal) int {
		return cmp.Or(cmp.Compare(a.Owner, b.Owner), cmp.Compare(a.Worker.Label(), b.Worker.Label()))
	})
	slices.SortFunc(res.Owners, func(a, b OwnerTotal) int { return cmp.Compare(a.Owner, b.Owner) })
	return res
}

// Coalesce reports whether next (a closing auto span of the same worker)
// should be merged into prev at write time: they overlap, touch, or are
// less than gap apart (TQ4). The writer that closes the second span
// decides, so the result is deterministic.
func Coalesce(prevStart, prevEnd, nextStart, nextEnd time.Time, gap time.Duration) bool {
	// order the two by start; the later one's gap to the earlier's end decides
	if nextStart.Before(prevStart) {
		prevEnd, nextStart = nextEnd, prevStart
	}
	return nextStart.Sub(prevEnd) < gap
}

// LongSpan reports a closed auto span over limit (TQ2: a flag, never a
// cap; a zero or negative limit flags nothing).
func LongSpan(seconds int64, limit time.Duration) bool {
	return limit > 0 && seconds > int64(limit/time.Second)
}

// Overlaps reports whether [as, ae) and [bs, be) share any time (the
// manual-vs-auto hint of TQ5).
func Overlaps(as, ae, bs, be time.Time) bool {
	return as.Before(be) && bs.Before(ae)
}
