package teamprofile

import (
	"slices"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Period is one company stint (actor_periods row, TP-14), or the period
// synthesised from an actor's active/left_at when it has none (TP-22).
// Dates are calendar days at midnight UTC.
type Period struct {
	// ID is the row id; zero for a synthesised period (Synthesised).
	ID        ids.AnyID
	ActorID   ids.AnyID
	Type      string
	StartedOn time.Time
	// EndedOn is zero for the open period.
	EndedOn   time.Time
	OrgRole   string
	EndReason string
	Note      string
	// Source is schema.PeriodSourceMigrated on a period built from
	// active/left_at (synthesised, or materialised from one), else "".
	Source string
}

// Open reports whether the period has no end.
func (p Period) Open() bool { return p.EndedOn.IsZero() }

// Synthesised reports whether the period was built at read time and has
// no row yet: the first period write materialises it (TEAM_PROFILES 8).
func (p Period) Synthesised() bool { return p.ID.IsZero() }

// Fields is the actor_periods row of p, for Create/CreateWithID (the
// natural id is (actor_id, started_on), TS-5) or to materialise a
// synthesised period. Empty optional fields are left out.
func (p Period) Fields() (map[string]value.Value, error) {
	actor, err := value.RefOf(p.ActorID)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "period: the actor id is not valid", err)
	}
	f := map[string]value.Value{schema.FieldPeriodActor: actor}
	for name, s := range map[string]string{
		schema.FieldPeriodType: p.Type, schema.FieldPeriodOrgRole: p.OrgRole,
		schema.FieldPeriodEndReason: p.EndReason, schema.FieldPeriodNote: p.Note, schema.FieldPeriodSource: p.Source,
	} {
		if s == "" {
			continue
		}
		t, terr := value.TextOf(s)
		if terr != nil {
			return nil, errs.Wrap(errs.InvalidInput, "period: "+name+" is not valid text", terr)
		}
		f[name] = t
	}
	if p.StartedOn.IsZero() {
		return nil, errs.New(errs.InvalidInput, "period: started_on is required")
	}
	if f[schema.FieldPeriodStartedOn], err = dateValue(p.StartedOn); err != nil {
		return nil, err
	}
	if !p.EndedOn.IsZero() {
		if f[schema.FieldPeriodEndedOn], err = dateValue(p.EndedOn); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// Span is a stint's days, Start through End inclusive (End is the last
// day, as ended_on is for a period and a membership: TEAM_PROFILES 5.2,
// 5.4). A zero End is an open stint that runs on. Periods, memberships
// and invite accepts all decide "do these two stints share a day?" with
// Overlaps, so the boundary is the same everywhere.
type Span struct {
	Start time.Time
	End   time.Time
}

// Open reports whether the span has no end.
func (s Span) Open() bool { return s.End.IsZero() }

// Overlaps reports whether s and o share at least one day: each starts
// on or before the other's last day (an open span has no last day).
func (s Span) Overlaps(o Span) bool {
	return (o.Open() || !s.Start.After(o.End)) && (s.Open() || !o.Start.After(s.End))
}

// Span is p's days.
func (p Period) Span() Span { return Span{Start: p.StartedOn, End: p.EndedOn} }

// Day is t's calendar day as midnight UTC (the date in t's own location).
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// PeriodsOf returns actor's periods from rows (any table rows may be
// passed; only live actor_periods rows of this actor count), sorted by
// started_on. A human actor with none gets one synthesised period
// (TEAM_PROFILES 8, TP-22): type employee, started_on the actor row's
// created_at day, ended_on the left_at day when active is false (else
// open), source migrated. Agents and other kinds get no synthesised period.
// A human row with no created_at (an old row that never had one) gets none
// either: there is no day to start it on. A period row whose dates do not
// parse is E_INVALID_INPUT naming it (fail closed: a stint is never guessed).
func PeriodsOf(actor row.Row, rows []row.Row) ([]Period, error) {
	var out []Period
	for _, r := range rows {
		if r.Deleted || r.Table.String() != schema.TableActorPeriods || text(r.Fields, schema.FieldPeriodActor) != actor.ID.String() {
			continue
		}
		p, err := periodOfRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		p, ok, err := synthesised(actor)
		if err != nil || !ok {
			return nil, err
		}
		return []Period{p}, nil
	}
	slices.SortFunc(out, func(a, b Period) int { return a.StartedOn.Compare(b.StartedOn) })
	return out, nil
}

// actorFieldKind is actors.kind (schema's own constant is unexported).
const actorFieldKind = "kind"

// synthesised is the TP-22 period of a human actor with no period rows.
func synthesised(actor row.Row) (Period, bool, error) {
	if text(actor.Fields, actorFieldKind) != schema.ActorKindHuman || actor.CreatedAt.IsZero() {
		return Period{}, false, nil
	}
	p := Period{ActorID: actor.ID, Type: schema.PeriodTypeEmployee, StartedOn: Day(actor.CreatedAt), Source: schema.PeriodSourceMigrated}
	if text(actor.Fields, schema.FieldActorActive) != activeFalse {
		return p, true, nil
	}
	// Left: left_at is the end; a deactivate without it (never written by
	// lore, but a hand-edited file may lack it) ends on the last update,
	// else on the start, so the stint is never left open for someone gone.
	end := actor.UpdatedAt
	if s := text(actor.Fields, schema.FieldActorLeftAt); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return Period{}, false, errs.Wrap(errs.InvalidInput, "actor "+actor.ID.String()+": left_at "+s+" is not a time", err)
		}
		end = t
	}
	p.EndedOn = Day(end)
	if end.IsZero() || p.EndedOn.Before(p.StartedOn) {
		p.EndedOn = p.StartedOn // clock skew between machines: never end before the start
	}
	return p, true, nil
}

// periodOfRow reads one actor_periods row.
func periodOfRow(r row.Row) (Period, error) {
	actor, err := ids.ParseAny(text(r.Fields, schema.FieldPeriodActor))
	if err != nil {
		return Period{}, errs.Wrap(errs.InvalidInput, "period "+r.ID.String()+": actor_id is not an id", err)
	}
	p := Period{ID: r.ID, ActorID: actor, Type: text(r.Fields, schema.FieldPeriodType),
		OrgRole: text(r.Fields, schema.FieldPeriodOrgRole), EndReason: text(r.Fields, schema.FieldPeriodEndReason),
		Note: text(r.Fields, schema.FieldPeriodNote), Source: text(r.Fields, schema.FieldPeriodSource)}
	if p.StartedOn, err = parseDay(r, schema.FieldPeriodStartedOn); err != nil {
		return Period{}, err
	}
	if p.StartedOn.IsZero() {
		return Period{}, errs.New(errs.InvalidInput, "period "+r.ID.String()+": started_on is missing")
	}
	if p.EndedOn, err = parseDay(r, schema.FieldPeriodEndedOn); err != nil {
		return Period{}, err
	}
	return p, nil
}

// parseDay reads a date field of r (zero when absent).
func parseDay(r row.Row, field string) (time.Time, error) {
	s := text(r.Fields, field)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(value.DateLayout, s)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.InvalidInput, "period "+r.ID.String()+": "+field+" "+s+" is not YYYY-MM-DD", err)
	}
	return t, nil
}

// dateValue is t's day as a date value.
func dateValue(t time.Time) (value.Date, error) {
	d, err := value.DateOf(Day(t).Format(value.DateLayout))
	if err != nil {
		return value.Date{}, errs.Wrap(errs.InvalidInput, "period: a date out of range", err)
	}
	return d, nil
}

// CheckPeriods checks the period invariants of one actor (TEAM_PROFILES
// 2.5): every ended_on on or after its started_on, at most one open
// period, and no two periods sharing a day (a stint ends before the next
// starts; the open one must be the latest). The first broken rule is
// E_PERIOD_OVERLAP naming the periods. ps may be in any order.
func CheckPeriods(ps []Period) error {
	sorted := slices.Clone(ps)
	slices.SortFunc(sorted, func(a, b Period) int { return a.StartedOn.Compare(b.StartedOn) })
	for i, p := range sorted {
		if !p.Open() && p.EndedOn.Before(p.StartedOn) {
			// The fix is this period's own end, not a date after another
			// stint, so the hint names its start (scen-team).
			return errs.New(errs.PeriodOverlap, "period "+label(p)+" ends before it starts").
				WithHint("end it on or after its start (" + p.StartedOn.Format(value.DateLayout) + ")")
		}
		if i == 0 {
			continue
		}
		prev := sorted[i-1]
		if prev.Open() {
			return overlapErr("period " + label(prev) + " is still open while " + label(p) + " starts after it")
		}
		if p.Span().Overlaps(prev.Span()) {
			return overlapErr("period " + label(p) + " overlaps " + label(prev))
		}
	}
	return nil
}

// label names a period in a message: "2026-01-12 → 2026-09-30" or "… → now".
func label(p Period) string {
	end := "now"
	if !p.Open() {
		end = p.EndedOn.Format(value.DateLayout)
	}
	return p.StartedOn.Format(value.DateLayout) + " → " + end
}

// overlapErr is E_PERIOD_OVERLAP with the next step.
func overlapErr(msg string) error {
	return errs.New(errs.PeriodOverlap, msg).WithHint("lore actor period list --actor <actor> shows the stints; pick a date after the last end")
}
