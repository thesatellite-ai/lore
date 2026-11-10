package period

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// PeriodListIn is the input of actor.period.list.
type PeriodListIn struct {
	Actor string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

// PeriodListOut is a person's stints, oldest first.
type PeriodListOut struct {
	ID      string      `json:"id"`
	Name    string      `json:"display_name"`
	Status  string      `json:"status"`
	Periods []PeriodOut `json:"periods"`
}

// Text is one line per stint.
func (o PeriodListOut) Text() string {
	var b strings.Builder
	b.WriteString(o.ID + "  " + o.Name + "  " + o.Status + "\n")
	if len(o.Periods) == 0 {
		b.WriteString("  (no stints)\n")
	}
	for _, p := range o.Periods {
		id := p.ID
		if id == "" {
			id = "(no row yet)"
		}
		b.WriteString("  " + id + "  " + periodLine(p) + "\n")
	}
	return b.String()
}

// periodList is actor.period.list (read: open to agents).
func (o ops) periodList(ctx context.Context, in PeriodListIn) (PeriodListOut, error) {
	r, done, err := o.env.Roster(ctx, false)
	defer done()
	if err != nil {
		return PeriodListOut{}, err
	}
	a, err := r.ResolveActor(ctx, in.Actor)
	if err != nil {
		return PeriodListOut{}, err
	}
	ps, err := periodsOf(ctx, r, a)
	if err != nil {
		return PeriodListOut{}, err
	}
	rows, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return PeriodListOut{}, err
	}
	writers := auditrec.ByID(rows) // a synthesised period has no row: no writers
	out := PeriodListOut{ID: a.ID.String(), Name: nameOf(a), Status: teamprofile.StatusOf(a.Fields), Periods: []PeriodOut{}}
	for _, p := range ps {
		o := periodOut(p)
		if w, ok := writers[o.ID]; ok && o.ID != "" {
			o.CreatedBy, o.UpdatedBy = w.CreatedBy, w.UpdatedBy
		}
		out.Periods = append(out.Periods, o)
	}
	return out, nil
}

// PeriodAddIn is the input of actor.period.add.
type PeriodAddIn struct {
	Actor     string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	Type      string `json:"type" flag:"type" desc:"employee, contractor, intern or client_contact"`
	StartedOn string `json:"started_on" flag:"started-on" desc:"first day, YYYY-MM-DD"`
	EndedOn   string `json:"ended_on,omitempty" flag:"ended-on" desc:"last day, YYYY-MM-DD (empty: the open stint)"`
	OrgRole   string `json:"org_role,omitempty" flag:"org-role" desc:"the org-wide title for this stint"`
	EndReason string `json:"end_reason,omitempty" flag:"end-reason" desc:"why it ended"`
	Note      string `json:"note,omitempty" flag:"note" desc:"a note"`
}

// PeriodWriteOut is the period a write left.
type PeriodWriteOut struct {
	Actor  string    `json:"actor_id"`
	Name   string    `json:"display_name"`
	Period PeriodOut `json:"period"`
	// Created is true for period add, false for edit.
	Created bool `json:"created"`
	// Replaced is true when the add took the place of the period read from
	// active/left_at (see periodAdd).
	Replaced bool   `json:"replaced_roster_period,omitempty"`
	Where    string `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ period apd_… of Mira Patel: 2026-01-12 → now  employee".
func (o PeriodWriteOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o PeriodWriteOut) text() string {
	verb := "updated"
	if o.Created {
		verb = "added"
	}
	s := success + " period " + o.Period.ID + " of " + o.Name + " " + verb + ": " + periodLine(o.Period) + "\n"
	if o.Replaced {
		s += "  it replaces the stint read from the roster (active/left_at)\n"
	}
	return s
}

// periodAdd is actor.period.add: record a stint by hand.
//
// The period read from active/left_at (TP-22) is materialised first, so it
// is never lost once a row exists, with one exception: an OPEN read-time
// period of someone still here carries nothing but the day their actor row
// was written, and recording their real stint (worked example: Mira
// registered, then `period add --started-on 2026-01-12`) would otherwise
// collide with that guess. When the new period overlaps such a guess, it
// replaces it (Replaced). A closed read-time period holds a real left_at
// and is always kept: an overlap with it is E_PERIOD_OVERLAP.
func (o ops) periodAdd(ctx context.Context, in PeriodAddIn) (PeriodWriteOut, error) {
	if !validType(in.Type) {
		return PeriodWriteOut{}, errs.New(errs.InvalidInput, "--type "+quote(in.Type)+" is not one of "+strings.Join(schema.PeriodTypes(), listSep))
	}
	start, err := day("started-on", in.StartedOn, time.Time{})
	if err != nil {
		return PeriodWriteOut{}, err
	}
	if start.IsZero() {
		return PeriodWriteOut{}, errs.New(errs.InvalidInput, "--started-on is required")
	}
	end, err := day("ended-on", in.EndedOn, time.Time{})
	if err != nil {
		return PeriodWriteOut{}, err
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return PeriodWriteOut{}, err
	}
	a, err := r.ResolveActor(ctx, in.Actor)
	if err != nil {
		return PeriodWriteOut{}, err
	}
	if !isHuman(a) {
		return PeriodWriteOut{}, errs.New(errs.InvalidInput, "stints are for people; agents have none")
	}
	ps, err := periodsOf(ctx, r, a)
	if err != nil {
		return PeriodWriteOut{}, err
	}
	next := teamprofile.Period{ActorID: a.ID, Type: in.Type, StartedOn: start, EndedOn: end, OrgRole: in.OrgRole, EndReason: in.EndReason, Note: in.Note}
	replaced := false
	if err = teamprofile.CheckPeriods(append(slices.Clone(ps), next)); err != nil {
		if len(ps) != 1 || !ps[0].Synthesised() || !ps[0].Open() {
			return PeriodWriteOut{}, err
		}
		ps, replaced = nil, true
		if err = teamprofile.CheckPeriods([]teamprofile.Period{next}); err != nil {
			return PeriodWriteOut{}, err
		}
	}
	for _, p := range ps {
		if _, err = materialise(ctx, r, p); err != nil {
			return PeriodWriteOut{}, err
		}
	}
	f, err := next.Fields()
	if err != nil {
		return PeriodWriteOut{}, err
	}
	w, err := r.Create(ctx, schema.TableActorPeriods, f)
	if err != nil {
		return PeriodWriteOut{}, err
	}
	r.Push(ctx)
	next.ID = w.ID
	return PeriodWriteOut{Actor: a.ID.String(), Name: nameOf(a), Period: periodOut(next), Created: true, Replaced: replaced, Where: r.Where(), Warnings: r.Warnings()}, nil
}

// PeriodEditIn is the input of actor.period.edit.
type PeriodEditIn struct {
	Period    string `json:"period" arg:"0" desc:"the stint's apd_ id, or a person whose only stint it is (the one read from active/left_at has no id yet)"`
	Type      string `json:"type,omitempty" flag:"type" desc:"employee, contractor, intern or client_contact"`
	StartedOn string `json:"started_on,omitempty" flag:"started-on" desc:"first day, YYYY-MM-DD"`
	EndedOn   string `json:"ended_on,omitempty" flag:"ended-on" desc:"last day, YYYY-MM-DD"`
	OrgRole   string `json:"org_role,omitempty" flag:"org-role" desc:"the org-wide title for this stint"`
	EndReason string `json:"end_reason,omitempty" flag:"end-reason" desc:"why it ended"`
	Note      string `json:"note,omitempty" flag:"note" desc:"a note"`
	Clear     string `json:"clear,omitempty" flag:"clear" desc:"comma-separated fields to empty: ended_on, org_role, end_reason, note"`
}

// clearable are the fields --clear may empty (type and started_on are
// required).
func clearable() []string {
	return []string{schema.FieldPeriodEndedOn, schema.FieldPeriodOrgRole, schema.FieldPeriodEndReason, schema.FieldPeriodNote}
}

// periodEdit is actor.period.edit: fix a stint (overlap rules apply).
func (o ops) periodEdit(ctx context.Context, in PeriodEditIn) (PeriodWriteOut, error) {
	if in.Type != "" && !validType(in.Type) {
		return PeriodWriteOut{}, errs.New(errs.InvalidInput, "--type "+quote(in.Type)+" is not one of "+strings.Join(schema.PeriodTypes(), listSep))
	}
	var clears []string
	for _, c := range strings.Split(in.Clear, clearSep) {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		if !slices.Contains(clearable(), c) {
			return PeriodWriteOut{}, errs.New(errs.InvalidInput, "--clear "+c+" is not one of "+strings.Join(clearable(), listSep))
		}
		clears = append(clears, c)
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return PeriodWriteOut{}, err
	}
	a, ps, target, err := findPeriod(ctx, r, in.Period)
	if err != nil {
		return PeriodWriteOut{}, err
	}
	edited := ps[target]
	if in.Type != "" {
		edited.Type = in.Type
	}
	if edited.StartedOn, err = day("started-on", in.StartedOn, edited.StartedOn); err != nil {
		return PeriodWriteOut{}, err
	}
	if edited.EndedOn, err = day("ended-on", in.EndedOn, edited.EndedOn); err != nil {
		return PeriodWriteOut{}, err
	}
	for name, s := range map[string]*string{schema.FieldPeriodOrgRole: &edited.OrgRole, schema.FieldPeriodEndReason: &edited.EndReason, schema.FieldPeriodNote: &edited.Note} {
		switch {
		case slices.Contains(clears, name):
			*s = ""
		case name == schema.FieldPeriodOrgRole && in.OrgRole != "":
			*s = in.OrgRole
		case name == schema.FieldPeriodEndReason && in.EndReason != "":
			*s = in.EndReason
		case name == schema.FieldPeriodNote && in.Note != "":
			*s = in.Note
		}
	}
	if slices.Contains(clears, schema.FieldPeriodEndedOn) {
		edited.EndedOn = time.Time{}
	}
	all := slices.Clone(ps)
	all[target] = edited
	if err = teamprofile.CheckPeriods(all); err != nil {
		return PeriodWriteOut{}, err
	}
	// Materialise every read-time period (only ever one) before writing.
	stored, err := materialise(ctx, r, ps[target])
	if err != nil {
		return PeriodWriteOut{}, err
	}
	patch, err := periodPatch(edited)
	if err != nil {
		return PeriodWriteOut{}, err
	}
	if _, err = r.Update(ctx, schema.TableActorPeriods, stored.ID, patch); err != nil {
		return PeriodWriteOut{}, err
	}
	r.Push(ctx)
	edited.ID = stored.ID
	return PeriodWriteOut{Actor: a.ID.String(), Name: nameOf(a), Period: periodOut(edited), Where: r.Where(), Warnings: r.Warnings()}, nil
}

// findPeriod resolves a period edit target: an apd_ id, or a person whose
// only stint it is. It returns the actor, all their periods and the index
// of the target.
func findPeriod(ctx context.Context, r profilestore.Roster, ref string) (row.Row, []teamprofile.Period, int, error) {
	ref = strings.TrimSpace(ref)
	var a row.Row
	var want ids.AnyID
	if id, err := ids.ParseAny(ref); err == nil && id.Prefix() == ids.PrefixActorPeriod {
		pr, gerr := r.Get(ctx, schema.TableActorPeriods, id)
		if gerr != nil {
			return row.Row{}, nil, 0, gerr
		}
		aid, perr := ids.ParseAny(text(pr, schema.FieldPeriodActor))
		if perr != nil {
			return row.Row{}, nil, 0, errs.Wrap(errs.InvalidInput, "period "+ref+": actor_id is not an id", perr)
		}
		if a, gerr = r.Get(ctx, schema.TableActors, aid); gerr != nil {
			return row.Row{}, nil, 0, gerr
		}
		want = id
	} else {
		var rerr error
		if a, rerr = r.ResolveActor(ctx, ref); rerr != nil {
			return row.Row{}, nil, 0, rerr
		}
	}
	ps, err := periodsOf(ctx, r, a)
	if err != nil {
		return row.Row{}, nil, 0, err
	}
	if want.IsZero() {
		if len(ps) != 1 {
			return row.Row{}, nil, 0, errs.New(errs.InvalidInput, nameOf(a)+" has "+strconv.Itoa(len(ps))+" stints; name one by its apd_ id").
				WithHint("lore actor period list " + ref)
		}
		return a, ps, 0, nil
	}
	for i, p := range ps {
		if p.ID == want {
			return a, ps, i, nil
		}
	}
	return row.Row{}, nil, 0, errs.New(errs.NotFound, "no period "+ref)
}

// periodPatch is the full patch of p's editable fields (an empty optional
// field is cleared).
func periodPatch(p teamprofile.Period) (map[string]value.Value, error) {
	f, err := p.Fields()
	if err != nil {
		return nil, err
	}
	delete(f, schema.FieldPeriodActor)
	for _, name := range clearable() {
		if _, ok := f[name]; !ok {
			f[name] = value.Null{}
		}
	}
	return f, nil
}
