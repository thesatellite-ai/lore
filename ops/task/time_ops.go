package task

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// Time operation names (design 6.1, OQ4): `lore task time show|log|edit|
// rm|restore|stop`. show is a verb because a CLI word cannot be both a
// command and a group (surface/cli Mount).
const (
	OpTimeShow    = "task.time.show"
	OpTimeLog     = "task.time.log"
	OpTimeEdit    = "task.time.edit"
	OpTimeRm      = "task.time.rm"
	OpTimeRestore = "task.time.restore"
	OpTimeStop    = "task.time.stop"
	// KindTime is the output kind of task.time.show.
	KindTime = "task.time"
)

// Group-by choices of task time show (design 6.1).
const (
	groupWorker = "worker"
	groupPerson = "person"
	groupSource = "source"
	groupDay    = "day"
)

// noteMax is the longest --note (design 6.1, R12 text rules).
const noteMax = 200

// TimeShowIn is the input of task.time.show.
type TimeShowIn struct {
	ID      string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Entries bool   `json:"entries,omitempty" flag:"entries" desc:"list every entry"`
	Deleted bool   `json:"deleted,omitempty" flag:"deleted" desc:"with --entries, list removed entries too"`
	GroupBy string `json:"group_by,omitempty" flag:"group-by" desc:"worker (default) | person | source | day"`
	Since   string `json:"since,omitempty" flag:"since" desc:"only entries on or after this day (YYYY-MM-DD or a date phrase: today, mon, in 3d, …)"`
	Until   string `json:"until,omitempty" flag:"until" desc:"only entries on or before this day (YYYY-MM-DD or a date phrase)"`
	TZ      string `json:"tz,omitempty" flag:"tz" desc:"time zone for display (e.g. Europe/Prague; default local)"`
}

// TimeLogIn is the input of task.time.log.
type TimeLogIn struct {
	ID       string      `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Duration string      `json:"duration" arg:"1" desc:"how long: 45m, 1h30m, 1.5h (no day unit; at most time.max_entry)"`
	Note     string      `json:"note,omitempty" flag:"note" desc:"one line, up to 200 characters"`
	At       string      `json:"at,omitempty" flag:"at" desc:"when: a day (2026-11-17) or a datetime with zone (2026-11-17T14:00+05:30); default: ends now"`
	By       op.ActorRef `json:"by,omitempty" flag:"by" desc:"who did the work (handle, actor id, @me); default: you"`
}

// TimeEditIn is the input of task.time.edit.
type TimeEditIn struct {
	ID       string      `json:"id" arg:"0" desc:"the time entry id (tme_…, or a unique prefix/suffix)"`
	Duration string      `json:"duration,omitempty" flag:"duration" desc:"new length (keeps the start)"`
	Start    string      `json:"start,omitempty" flag:"start" desc:"new start (datetime with zone)"`
	End      string      `json:"end,omitempty" flag:"end" desc:"new end (datetime with zone)"`
	At       string      `json:"at,omitempty" flag:"at" desc:"new day or datetime of a manual entry"`
	Note     string      `json:"note,omitempty" flag:"note" desc:"new note"`
	By       op.ActorRef `json:"by,omitempty" flag:"by" desc:"new worker"`
}

// TimeEntryIn names one entry (rm, restore).
type TimeEntryIn struct {
	ID string `json:"id" arg:"0" desc:"the time entry id (tme_…, or a unique prefix/suffix)"`
}

// TimeWorker is one line of the breakdown.
type TimeWorker struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Actor         string `json:"actor_id,omitempty"`
	Owner         string `json:"owner,omitempty"`
	AutoSeconds   int64  `json:"auto_seconds"`
	ManualSeconds int64  `json:"manual_seconds"`
	TotalSeconds  int64  `json:"total_seconds"`
	// InclAgentsSeconds is the person's subtotal with their agents,
	// overlap counted once (group-by person, TQ1).
	InclAgentsSeconds int64  `json:"incl_agents_seconds,omitempty"`
	Last              string `json:"last,omitempty"`
	Open              bool   `json:"open,omitempty"`
}

// TimeData is the time of one task.
type TimeData struct {
	TaskID         string            `json:"task_id"`
	Title          string            `json:"title"`
	Status         string            `json:"status"`
	EffortSeconds  int64             `json:"effort_seconds"`
	ElapsedSeconds int64             `json:"elapsed_seconds"`
	ManualSeconds  int64             `json:"manual_seconds"`
	GroupBy        string            `json:"group_by"`
	Workers        []TimeWorker      `json:"workers"`
	Entries        []timeentry.Entry `json:"entries,omitempty"`
	Open           []timeentry.Open  `json:"open,omitempty"`
	Notes          []string          `json:"notes,omitempty"`
	showEntries    bool
	loc            *time.Location
}

// TimeOut is the output of task.time.show.
type TimeOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Data          TimeData `json:"data"`
}

// timeOps are the time operations, appended to Ops.
func timeOps(o ops) []op.Entry {
	writeErrs := append(slices.Clone(work.WriteErrors), errs.DurationBad, errs.TimeTooLong)
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpTimeShow, Summary: "Show a task's tracked time (effort, elapsed, manual, per worker)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
			Examples: []op.Example{{Title: "per person with agents", CLI: "lore task time show tsk_<id> --group-by person", Want: "effort"}},
		}, o.timeShow),
		op.Define(op.Spec{
			Name: OpTimeLog, Summary: "Log time on a task by hand",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "log a call", CLI: `lore task time log tsk_<id> 1h30m --note "call with acme"`, Want: "logged"}},
		}, o.timeLog),
		op.Define(op.Spec{
			Name: OpTimeEdit, Summary: "Change a time entry (auto entries keep their original times)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
		}, o.timeEdit),
		op.Define(op.Spec{
			Name: OpTimeRm, Summary: "Remove a time entry (kept as a tombstone; restore undoes it)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(work.WriteErrors), errs.NeedsConfirm),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.timeRm),
		op.Define(op.Spec{
			Name: OpTimeRestore, Summary: "Undo the removal of a time entry",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.timeRestore),
		op.Define(op.Spec{
			Name: OpTimeStop, Summary: "Close your own open status span; the status stays",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
		}, o.timeStop),
	}
}

// timeShow computes a task's time (design 5.1, 2.4):
// get row → entries (+ open spans, end = now) → filter → timecalc.Compute
// → group (worker | person TQ1 | source | day) → notes (long spans TQ2).
func (o ops) timeShow(ctx context.Context, in TimeShowIn) (TimeOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return TimeOut{}, err
	}
	group := orDefault(in.GroupBy, groupWorker)
	if err = work.OneOf(Noun, "group-by", group, []string{groupWorker, groupPerson, groupSource, groupDay}); err != nil {
		return TimeOut{}, err
	}
	loc := time.Local
	if in.TZ != "" {
		if loc, err = time.LoadLocation(in.TZ); err != nil {
			return TimeOut{}, errs.Wrap(errs.InvalidInput, "--tz "+work.Quote(in.TZ)+" is not a time zone", err).WithHint("e.g. Europe/Prague or UTC")
		}
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return TimeOut{}, err
	}
	tp, err := o.timePolicyAt(ctx, src)
	if err != nil {
		return TimeOut{}, err
	}
	now := opclock.Now(o.env).UTC()
	since, err := dayText(o.clock(), "since", in.Since)
	if err != nil {
		return TimeOut{}, err
	}
	until, err := dayText(o.clock(), "until", in.Until)
	if err != nil {
		return TimeOut{}, err
	}
	inRange := func(day string) bool {
		return (since == "" || day >= since) && (until == "" || day <= until)
	}
	d := TimeData{TaskID: id.String(), Title: work.Field(r, work.FieldTitle), Status: work.Field(r, work.FieldStatus), GroupBy: group, showEntries: in.Entries, loc: loc}
	owners := map[string]string{}
	ownerOf := func(actor string) string {
		if v, ok := owners[actor]; ok {
			return v
		}
		v := actor
		if ar, ok := o.actorRow(ctx, actor); ok {
			if own := work.Field(ar, "owner_actor_id"); own != "" {
				v = own
			}
		}
		owners[actor] = v
		return v
	}
	var spans []timecalc.Span
	keyOf := func(w timecalc.Worker, source timeentry.Source, day string) string {
		switch group {
		case groupSource:
			return string(source)
		case groupDay:
			return day
		case groupPerson:
			return ownerOf(w.Actor)
		}
		return w.Label()
	}
	type keyed struct {
		key  string
		span timecalc.Span
	}
	var ks []keyed
	for _, e := range TimeEntries(r) {
		if in.Entries && (in.Deleted || !e.Deleted()) && inRange(e.Day) {
			d.Entries = append(d.Entries, e)
		}
		if e.Deleted() || !inRange(e.Day) {
			continue
		}
		w := timecalc.Worker{Actor: e.ActorID.String(), Session: e.SessionRef}
		s := timecalc.Span{Worker: w, Owner: ownerOf(w.Actor), Start: e.StartTime(), End: e.EndTime(), Seconds: e.Seconds, Manual: e.Source == timeentry.SourceManual}
		spans = append(spans, s)
		ks = append(ks, keyed{keyOf(w, e.Source, e.Day), s})
		if e.HasFlag(timeentry.FlagLongSpan) || (!s.Manual && timecalc.LongSpan(e.Seconds, tp.longSpan)) {
			d.Notes = append(d.Notes, "! "+e.ID.String()+" is "+timeentry.FormatSeconds(e.Seconds)+" ("+o.actorName(ctx, w.Actor)+", "+string(e.Source)+" span from "+e.Start+"), over time.long_span "+timeentry.FormatSeconds(int64(tp.longSpan/time.Second))+"; fix with lore task time edit")
		}
	}
	for _, s := range TimeOpen(r) {
		d.Open = append(d.Open, s)
		day := s.StartTime().Format(timeentry.DayLayout)
		if !inRange(day) {
			continue
		}
		w := timecalc.Worker{Actor: s.ActorID.String()}
		sp := timecalc.Span{Worker: w, Owner: ownerOf(w.Actor), Start: s.StartTime(), End: now, Open: true}
		spans = append(spans, sp)
		ks = append(ks, keyed{keyOf(w, s.Source, day), sp})
	}
	res := timecalc.Compute(spans)
	d.EffortSeconds, d.ElapsedSeconds, d.ManualSeconds = res.Effort, res.Elapsed, res.Manual
	if group == groupWorker || group == groupPerson {
		incl := map[string]int64{}
		for _, ot := range res.Owners {
			incl[ot.Owner] = ot.Incl
		}
		for _, w := range res.Workers {
			key := keyOf(w.Worker, "", "")
			d.Workers = append(d.Workers, TimeWorker{Key: key, Label: o.workerLabel(ctx, w.Worker), Actor: w.Worker.Actor, Owner: w.Owner,
				AutoSeconds: w.Auto, ManualSeconds: w.Manual, TotalSeconds: w.Total(), Last: lastOf(w.Last), Open: w.Open})
		}
		if group == groupPerson {
			d.Workers = foldPersons(d.Workers, incl, func(a string) string { return o.actorName(ctx, a) })
		}
	} else {
		groups := map[string][]timecalc.Span{}
		for _, k := range ks {
			groups[k.key] = append(groups[k.key], k.span)
		}
		for _, k := range slicesSorted(groups) {
			g := timecalc.Compute(groups[k])
			d.Workers = append(d.Workers, TimeWorker{Key: k, Label: k, AutoSeconds: g.Effort - g.Manual, ManualSeconds: g.Manual, TotalSeconds: g.Effort})
		}
	}
	d.Notes = append(d.Notes, manualOverlapHints(TimeEntries(r))...)
	return TimeOut{SchemaVersion: work.SchemaVersion, Kind: KindTime, Data: d}, nil
}

// foldPersons folds worker lines into one line per owner (TQ1): the
// person's own time in Auto/Manual, everything in Total (so Total − own is
// the agents' time) and the incl. agents subtotal (overlap once).
func foldPersons(ws []TimeWorker, incl map[string]int64, name func(string) string) []TimeWorker {
	by := map[string]*TimeWorker{}
	var order []string
	for _, w := range ws {
		p := by[w.Key]
		if p == nil {
			p = &TimeWorker{Key: w.Key, Label: name(w.Key), Actor: w.Key, InclAgentsSeconds: incl[w.Key]}
			by[w.Key] = p
			order = append(order, w.Key)
		}
		if w.Actor == w.Key {
			p.AutoSeconds += w.AutoSeconds
			p.ManualSeconds += w.ManualSeconds
		}
		p.TotalSeconds += w.TotalSeconds
		p.Open = p.Open || w.Open
		p.Last = max(p.Last, w.Last)
	}
	out := make([]TimeWorker, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

// slicesSorted returns the sorted keys of m.
func slicesSorted(m map[string][]timecalc.Span) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lastOf renders a worker's latest end ("" when none).
func lastOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeentry.TimeLayout)
}

// workerLabel is "display name" (+ " session ses_…" for agent sessions).
func (o ops) workerLabel(ctx context.Context, w timecalc.Worker) string {
	l := o.actorName(ctx, w.Actor)
	if w.Session != "" {
		l += " session " + w.Session
	}
	return l
}

// manualOverlapHints are TQ5/11.10 hints: two manual entries by one actor
// with the same day, length and note.
func manualOverlapHints(list []timeentry.Entry) []string {
	seen := map[string]string{}
	var out []string
	for _, e := range list {
		if e.Deleted() || e.Source != timeentry.SourceManual {
			continue
		}
		k := e.ActorID.String() + "|" + e.Day + "|" + strconv.FormatInt(e.Seconds, 10) + "|" + e.Note
		if first, ok := seen[k]; ok {
			out = append(out, "hint: "+first+" and "+e.ID.String()+" have the same worker, day, length and note; if it is the same work, remove one")
			continue
		}
		seen[k] = e.ID.String()
	}
	return out
}

// Text implements op.Texter.
func (o TimeOut) Text() string {
	d := o.Data
	loc := d.loc
	if loc == nil {
		loc = time.Local
	}
	var b strings.Builder
	b.WriteString(d.TaskID + "  " + d.Title + "   (" + d.Status + ")\n\n")
	b.WriteString("effort   " + timeentry.FormatSeconds(d.EffortSeconds) + "     elapsed  " + timeentry.FormatSeconds(d.ElapsedSeconds) +
		"     manual  " + timeentry.FormatSeconds(d.ManualSeconds) + "   (included in effort)\n\n")
	head := map[string]string{groupWorker: "WORKER", groupPerson: "PERSON", groupSource: "SOURCE", groupDay: "DAY"}[d.GroupBy]
	if d.GroupBy == groupPerson {
		b.WriteString(pad(head, 30) + pad("OWN", 10) + pad("AGENTS", 10) + "INCL. AGENTS\n")
		for _, w := range d.Workers {
			own := w.AutoSeconds + w.ManualSeconds
			b.WriteString(pad(w.Label, 30) + pad(timeentry.FormatSeconds(own), 10) + pad(timeentry.FormatSeconds(w.TotalSeconds-own), 10) + timeentry.FormatSeconds(w.InclAgentsSeconds) + "\n")
		}
	} else {
		b.WriteString(pad(head, 30) + pad("AUTO", 10) + pad("MANUAL", 10) + pad("TOTAL", 10) + "LAST\n")
		for _, w := range d.Workers {
			last := ""
			if t, err := time.Parse(time.RFC3339Nano, w.Last); err == nil {
				last = t.In(loc).Format("2006-01-02 15:04")
			}
			if w.Open {
				last += " (open)"
			}
			b.WriteString(pad(w.Label, 30) + pad(timeentry.FormatSeconds(w.AutoSeconds), 10) + pad(timeentry.FormatSeconds(w.ManualSeconds), 10) + pad(timeentry.FormatSeconds(w.TotalSeconds), 10) + last + "\n")
		}
	}
	if len(d.Workers) == 0 {
		b.WriteString("(no time yet)\n")
	}
	if d.showEntries {
		b.WriteString("\n" + pad("ID", 32) + pad("WHO", 28) + pad("SOURCE", 8) + pad("START", 18) + pad("END", 18) + pad("TIME", 9) + "NOTE / FLAGS\n")
		show := func(s string) string {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.In(loc).Format("2006-01-02 15:04")
			}
			return "-"
		}
		for _, e := range d.Entries {
			start := show(e.Start)
			if e.Start == "" {
				start = e.Day
			}
			note := e.Note
			if len(e.Flags) > 0 {
				note = strings.TrimSpace(note + " [" + strings.Join(e.Flags, ",") + "]")
			}
			if e.Deleted() {
				note = strings.TrimSpace(note + " (removed)")
			}
			b.WriteString(pad(e.ID.String(), 32) + pad(e.ActorID.String(), 28) + pad(string(e.Source), 8) + pad(start, 18) + pad(show(e.End), 18) + pad(timeentry.FormatSeconds(e.Seconds), 9) + note + "\n")
		}
		for _, s := range d.Open {
			b.WriteString(pad("open", 32) + pad(s.ActorID.String(), 28) + pad(string(s.Source), 8) + pad(show(s.Start), 18) + pad("(now)", 18) + "\n")
		}
	}
	if len(d.Notes) > 0 {
		b.WriteString("\nnotes\n")
		for _, n := range d.Notes {
			b.WriteString("  " + n + "\n")
		}
	}
	return b.String()
}

// pad left-aligns s in w columns (at least one space after).
func pad(s string, w int) string {
	if len([]rune(s)) >= w {
		return s + " "
	}
	return s + strings.Repeat(" ", w-len([]rune(s)))
}

// parseDur is the shared duration value type with the time rules (D13,
// OQ18): positive, no day unit (value.DurationOf refuses "d"), at most
// time.max_entry.
func parseDur(raw string, maxEntry time.Duration) (time.Duration, error) {
	v, err := value.DurationOf(strings.TrimSpace(raw))
	if err != nil || v.Duration() <= 0 {
		return 0, errs.New(errs.DurationBad, "duration "+work.Quote(raw)+" is not accepted").WithHint("give e.g. 45m, 1h30m or 1.5h (no day unit, more than zero)")
	}
	d := v.Duration()
	if d > maxEntry {
		return 0, errs.New(errs.TimeTooLong, "duration "+d.String()+" is over time.max_entry "+maxEntry.String()).WithHint("split it into one entry per day")
	}
	return d.Round(time.Second), nil
}

// parseNote applies the text rules of a note (R12): trimmed, NFC, one
// line of at most 200 characters, no control characters.
func parseNote(raw string) (string, error) {
	s := norm.NFC.String(strings.TrimSpace(raw))
	if len([]rune(s)) > noteMax {
		return "", errs.New(errs.InvalidInput, "--note is longer than "+strconv.Itoa(noteMax)+" characters")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", errs.New(errs.InvalidInput, "--note holds a control character or a line break").WithHint("give one line of text")
		}
	}
	return s, nil
}

// parseAt reads --at: a day (no clock time) or a datetime WITH a zone (a
// zoneless RFC3339 is refused, R12). day is set for a day, t for a time.
func parseAt(raw string) (day string, t time.Time, err error) {
	raw = strings.TrimSpace(raw)
	if d, derr := time.Parse(timeentry.DayLayout, raw); derr == nil {
		return d.Format(timeentry.DayLayout), time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if v, perr := time.Parse(layout, raw); perr == nil {
			return "", v, nil
		}
	}
	return "", time.Time{}, errs.New(errs.InvalidInput, "--at "+work.Quote(raw)+" is not a day or a datetime with a zone").
		WithHint("e.g. 2026-11-17 or 2026-11-17T14:00+05:30")
}

// meOrBy resolves --by (default the caller) to an actor id.
func (o ops) meOrBy(ctx context.Context, by op.ActorRef) (ids.ID[ids.Actor], error) {
	raw := strings.TrimSpace(string(by))
	if raw == "" {
		// the caller itself: no actor row lookup (its row is written on
		// its first write, which may be this one)
		if me := o.callerOf(ctx).id; !me.IsZero() {
			return me, nil
		}
		raw = actorMe
	}
	ref, err := o.actorRef(ctx, raw)
	if err != nil {
		return ids.ID[ids.Actor]{}, err
	}
	return ids.Parse[ids.Actor](ref)
}

// timeLog adds a manual entry (design 6.1; AGENTS OVERRIDE: anyone may log
// for anyone, created_by records the logger):
// parse duration (E_DURATION_BAD / E_TIME_TOO_LONG) → --at / --by / --note
// → append to time_entries → hint when it overlaps the worker's auto time (TQ5).
func (o ops) timeLog(ctx context.Context, in TimeLogIn) (work.WriteOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	tp, err := o.timePolicyAt(ctx, src)
	if err != nil {
		return work.WriteOut{}, err
	}
	dur, err := parseDur(in.Duration, tp.maxEntry)
	if err != nil {
		return work.WriteOut{}, err
	}
	note, err := parseNote(in.Note)
	if err != nil {
		return work.WriteOut{}, err
	}
	day, at := "", time.Time{}
	if in.At != "" {
		if day, at, err = parseAt(in.At); err != nil {
			return work.WriteOut{}, err
		}
	}
	worker, err := o.meOrBy(ctx, in.By)
	if err != nil {
		return work.WriteOut{}, err
	}
	logger := o.callerOf(ctx).id
	if logger.IsZero() {
		logger = worker
	}
	now := opclock.Now(o.env).UTC()
	eid, err := ids.New[timeentry.Tag]()
	if err != nil {
		return work.WriteOut{}, err
	}
	stamp := now.Format(timeentry.TimeLayout)
	e := timeentry.Entry{ID: eid, Kind: timeentry.KindManual, Source: timeentry.SourceManual, ActorID: worker,
		Seconds: int64(dur / time.Second), Note: note,
		CreatedAt: stamp, CreatedBy: logger.String(), UpdatedAt: stamp, UpdatedBy: logger.String()}
	if in.At == "" {
		at = now.Add(-dur)
	}
	if day != "" {
		e.Day = day
	} else {
		e.Start, e.End = at.UTC().Format(timeentry.TimeLayout), at.Add(dur).UTC().Format(timeentry.TimeLayout)
		// design 6.2: the local calendar day of the logger (11.4).
		e.Day = at.In(time.Local).Format(timeentry.DayLayout)
		if at.Location() != time.UTC && in.At != "" {
			e.Day = at.Format(timeentry.DayLayout)
		}
	}
	patch := map[string]value.Value{}
	if err = putEntries(patch, append(TimeEntries(r), e)); err != nil {
		return work.WriteOut{}, err
	}
	human := work.Success + " logged " + eid.String() + "  " + timeentry.FormatSeconds(e.Seconds) + "  for " + o.actorName(ctx, worker.String()) + "  on " + e.Day
	if logger != worker {
		human += "  (logged by " + o.actorName(ctx, logger.String()) + ")"
	}
	human += "\n"
	if e.Start != "" {
		for _, x := range TimeEntries(r) {
			if x.Deleted() || x.Source == timeentry.SourceManual || x.ActorID != worker {
				continue
			}
			if timecalc.Overlaps(e.StartTime(), e.EndTime(), x.StartTime(), x.EndTime()) {
				human += "  hint: this overlaps " + string(x.Source) + " time " + x.Start + " to " + x.End + " (" + x.ID.String() + "); if it is the same work, remove one: lore task time rm " + eid.String() + "\n"
				break
			}
		}
	}
	out, err := work.Patch(ctx, o.env, Noun, table, id, patch, human)
	out.ID = eid.String() // the new thing is the entry
	return out, err
}

// entryHit is an entry found by id with its task.
type entryHit struct {
	src   store.Source
	task  row.Row
	index int
	list  []timeentry.Entry
}

// findEntry finds a time entry by full id or a unique prefix/suffix across
// every task (design 6.1: "found by prefix like every other lore id").
func (o ops) findEntry(ctx context.Context, raw string) (entryHit, error) {
	want := strings.TrimSpace(strings.ReplaceAll(raw, "…", ""))
	if want == "" {
		return entryHit{}, errs.New(errs.InvalidInput, "time entry id is empty")
	}
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return entryHit{}, err
	}
	var hits []entryHit
	for _, s := range all {
		list := TimeEntries(s.Row)
		for i, e := range list {
			id := e.ID.String()
			if id == want {
				return entryHit{s.Source, s.Row, i, list}, nil
			}
			if len(want) >= 4 && (strings.HasPrefix(id, want) || strings.HasSuffix(id, want)) {
				hits = append(hits, entryHit{s.Source, s.Row, i, list})
			}
		}
	}
	switch len(hits) {
	case 0:
		return entryHit{}, errs.New(errs.NotFound, "no time entry "+work.Quote(raw)).WithHint("lore task time show <task> --entries lists the ids")
	case 1:
		return hits[0], nil
	}
	return entryHit{}, errs.New(errs.AmbiguousID, "time entry "+work.Quote(raw)+" matches "+strconv.Itoa(len(hits))+" entries").WithHint("give more characters of the id")
}

// save writes the hit's list back to its task.
func (o ops) save(ctx context.Context, h entryHit, human string) (work.WriteOut, error) {
	patch := map[string]value.Value{}
	if err := putEntries(patch, h.list); err != nil {
		return work.WriteOut{}, err
	}
	w, err := o.env.Update(ctx, engine.Ref{Store: h.src, Table: table, ID: h.task.ID}, patch)
	if err != nil {
		return work.WriteOut{}, err
	}
	out := work.Out(h.src, w, human)
	out.ID = h.list[h.index].ID.String()
	return out, nil
}

// stampEdit sets the audit fields of an edit.
func (o ops) stampEdit(ctx context.Context, e *timeentry.Entry) {
	now := opclock.Now(o.env).UTC().Format(timeentry.TimeLayout)
	e.UpdatedAt = now
	if c := o.callerOf(ctx).id; !c.IsZero() {
		e.UpdatedBy = c.String()
	}
}

// timeEdit changes any entry (D14): duration / start / end / at / note /
// by; an auto entry keeps its first times in orig and is flagged edited.
func (o ops) timeEdit(ctx context.Context, in TimeEditIn) (work.WriteOut, error) {
	h, err := o.findEntry(ctx, in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	tp, err := o.timePolicyAt(ctx, h.src)
	if err != nil {
		return work.WriteOut{}, err
	}
	e := h.list[h.index]
	before := e
	manual := e.Source == timeentry.SourceManual
	start, end := e.StartTime(), e.EndTime()
	if in.Start != "" {
		if _, start, err = parseAt(in.Start); err != nil || start.IsZero() {
			return work.WriteOut{}, errs.New(errs.InvalidInput, "--start needs a datetime with a zone").WithHint("e.g. 2026-11-12T09:00+01:00")
		}
	}
	if in.End != "" {
		if _, end, err = parseAt(in.End); err != nil || end.IsZero() {
			return work.WriteOut{}, errs.New(errs.InvalidInput, "--end needs a datetime with a zone").WithHint("e.g. 2026-11-12T18:00+01:00")
		}
	}
	if in.At != "" {
		day, at, aerr := parseAt(in.At)
		if aerr != nil {
			return work.WriteOut{}, aerr
		}
		if day != "" {
			e.Day, e.Start, e.End, start, end = day, "", "", time.Time{}, time.Time{}
		} else {
			length := time.Duration(e.Seconds) * time.Second
			start, end = at, at.Add(length)
		}
	}
	if in.Duration != "" {
		limit := tp.maxEntry
		if !manual {
			limit = time.Duration(1<<62 - 1) // max_entry is a manual rule (D13)
		}
		d, derr := parseDur(in.Duration, limit)
		if derr != nil {
			return work.WriteOut{}, derr
		}
		if start.IsZero() {
			e.Seconds = int64(d / time.Second)
		} else {
			end = start.Add(d)
		}
	}
	if !start.IsZero() {
		if end.Before(start) {
			return work.WriteOut{}, errs.New(errs.InvalidInput, "the entry would end before it starts")
		}
		if manual && end.Sub(start) > tp.maxEntry {
			return work.WriteOut{}, errs.New(errs.TimeTooLong, "the entry is over time.max_entry "+tp.maxEntry.String()).WithHint("split it into one entry per day")
		}
		e.Start, e.End = start.UTC().Format(timeentry.TimeLayout), end.UTC().Format(timeentry.TimeLayout)
		e.Seconds = int64(end.Sub(start) / time.Second)
		e.Day = start.Format(timeentry.DayLayout)
	}
	if in.Note != "" {
		if e.Note, err = parseNote(in.Note); err != nil {
			return work.WriteOut{}, err
		}
	}
	if in.By != "" {
		if e.ActorID, err = o.meOrBy(ctx, in.By); err != nil {
			return work.WriteOut{}, err
		}
	}
	if !manual && !e.HasFlag(timeentry.FlagEdited) {
		e.Orig = timeentry.Orig{Start: before.Start, End: before.End, Seconds: before.Seconds}
		e = e.WithFlag(timeentry.FlagEdited)
	}
	if !manual {
		long := timecalc.LongSpan(e.Seconds, tp.longSpan)
		if !long {
			e.Flags = slices.DeleteFunc(slices.Clone(e.Flags), func(f string) bool { return f == timeentry.FlagLongSpan })
		} else {
			e = e.WithFlag(timeentry.FlagLongSpan)
		}
	}
	o.stampEdit(ctx, &e)
	h.list[h.index] = e
	human := work.Success + " updated " + e.ID.String() + "  " + string(e.Source) + "  " + timeentry.FormatSeconds(e.Seconds) + " (was " + timeentry.FormatSeconds(before.Seconds) + ")\n"
	return o.save(ctx, h, human)
}

// timeRm tombstones an entry after confirmation (D14, D15: E_NEEDS_CONFIRM
// then --yes; never physically removed).
func (o ops) timeRm(ctx context.Context, in TimeEntryIn) (work.WriteOut, error) {
	h, err := o.findEntry(ctx, in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	e := h.list[h.index]
	if e.Deleted() {
		return work.WriteOut{ID: e.ID.String(), Store: work.SourceName(h.src), Human: e.ID.String() + " is already removed\n"}, nil
	}
	q := "remove " + e.ID.String() + " (" + timeentry.FormatSeconds(e.Seconds) + " for " + o.actorName(ctx, e.ActorID.String())
	if e.CreatedBy != "" && e.CreatedBy != e.ActorID.String() {
		q += ", logged by " + o.actorName(ctx, e.CreatedBy)
	}
	if err = bulk.Ask(ctx, q+")?"); err != nil {
		return work.WriteOut{}, err
	}
	o.stampEdit(ctx, &e)
	e.DeletedAt, e.DeletedBy = e.UpdatedAt, e.UpdatedBy
	h.list[h.index] = e
	return o.save(ctx, h, work.Success+" removed "+e.ID.String()+"\n")
}

// timeRestore undoes a removal. A coalesced tombstone is refused: its
// time already lives in the entry it was merged into (OV7-4).
func (o ops) timeRestore(ctx context.Context, in TimeEntryIn) (work.WriteOut, error) {
	h, err := o.findEntry(ctx, in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	e := h.list[h.index]
	if !e.Deleted() {
		return work.WriteOut{ID: e.ID.String(), Store: work.SourceName(h.src), Human: e.ID.String() + " is not removed\n"}, nil
	}
	if e.HasFlag(timeentry.FlagCoalesced) {
		return work.WriteOut{}, errs.New(errs.Refused, e.ID.String()+" was coalesced into another entry ("+e.Note+"); restoring it would count its time twice").
			WithHint("edit the entry it was merged into instead")
	}
	o.stampEdit(ctx, &e)
	e.DeletedAt, e.DeletedBy = "", ""
	h.list[h.index] = e
	return o.save(ctx, h, work.Success+" restored "+e.ID.String()+"\n")
}

// timeStop closes the caller's own open status span (design 4.1); the
// status is left alone. Not a timer: starting again is lore task start.
func (o ops) timeStop(ctx context.Context, in IDIn) (work.WriteOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return work.WriteOut{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	me := o.callerOf(ctx)
	if me.id.IsZero() {
		return work.WriteOut{}, errs.New(errs.Unsupported, "task time stop: the current identity is not known here")
	}
	patch := map[string]value.Value{}
	e, ok, err := o.stopSpan(ctx, src, r, me.id, patch)
	if err != nil {
		return work.WriteOut{}, err
	}
	if !ok {
		return work.WriteOut{}, errs.New(errs.NotFound, "you have no open status span on "+id.String()).
			WithHint("time counts from lore task start; agents get time from claims and runs")
	}
	return work.Patch(ctx, o.env, Noun, table, id, patch, work.Success+" stopped your time on "+id.String()+"  ("+timeentry.FormatSeconds(e.Seconds)+", entry "+e.ID.String()+"); status stays "+work.Field(r, work.FieldStatus)+"\n")
}
