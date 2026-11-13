package period

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// RejoinIn is the input of actor.rejoin.
type RejoinIn struct {
	Actor   string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	Type    string `json:"type" flag:"type" desc:"employee, contractor, intern or client_contact"`
	On      string `json:"on,omitempty" flag:"on" desc:"first day of the new stint, YYYY-MM-DD (default today); after the last stint's end"`
	OrgRole string `json:"org_role,omitempty" flag:"org-role" desc:"the org-wide title for this stint"`
	Note    string `json:"note,omitempty" flag:"note" desc:"a note on the stint"`
}

// RejoinOut is the rejoin preview, and after the confirm the result.
type RejoinOut struct {
	ID     string    `json:"id"`
	Name   string    `json:"display_name"`
	Period PeriodOut `json:"period"`
	// Memberships ended at the last offboard: not restored (TP-16).
	Memberships []MemberEnd `json:"memberships"`
	// TrashedMemberships are the memberships an offboard moved to the
	// trash because they had not started yet (MemberTrashReasonUnstarted,
	// NotStarted and StartsOn set): listed with `lore trash restore <id>`,
	// never restored by the rejoin (TP-16).
	TrashedMemberships []MemberEnd `json:"trashed_memberships"`
	// Agents owned and inactive: they stay inactive.
	Agents  []AgentOut `json:"agents"`
	From    string     `json:"from"`
	Applied bool       `json:"applied"`
	Where   string     `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Preview is the TEAM_PROFILES 5.2 step 3 block.
func (o RejoinOut) Preview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejoin %s (%s) on %s:\n", o.Name, o.ID, o.Period.StartedOn)
	fmt.Fprintf(&b, "  period     %s\n", periodLine(o.Period))
	lead := "  projects   "
	if len(o.Memberships) == 0 {
		b.WriteString(lead + "(none ended at the last offboard)\n")
	}
	for _, m := range o.Memberships {
		fmt.Fprintf(&b, "%s%s (%s): lore project member add %s %s --role %s\n", lead, m.Project, strings.Join(m.Roles, listSep), m.Project, o.ID, strings.Join(m.Roles, clearSep))
		lead = "             "
	}
	if len(o.Memberships) > 0 {
		b.WriteString("             memberships are not restored (TP-16)\n")
	}
	lead = "  trashed    "
	for _, m := range o.TrashedMemberships {
		fmt.Fprintf(&b, "%s%s (%s) starting %s, %s: lore trash restore %s\n", lead, m.Project, strings.Join(m.Roles, listSep), m.StartsOn, MemberTrashReasonUnstarted, m.ID)
		lead = "             "
	}
	if len(o.TrashedMemberships) > 0 {
		b.WriteString("             in the trash, not restored\n")
	}
	lead = "  agents     "
	for _, a := range o.Agents {
		fmt.Fprintf(&b, "%s%s stays inactive: lore actor status %s active\n", lead, a.Name, a.ID)
		lead = "             "
	}
	fmt.Fprintf(&b, "  status     %s%s%s\n", o.From, arrow, schema.ActorStatusActive)
	return b.String()
}

// Text is the preview and the done line.
func (o RejoinOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o RejoinOut) text() string {
	if !o.Applied {
		return o.Preview()
	}
	return o.Preview() + fmt.Sprintf("%s %s %s rejoined on %s as %s\n", success, o.ID, o.Name, o.Period.StartedOn, o.Period.Type)
}

// rejoin is actor.rejoin (TEAM_PROFILES 5.2, TP-14, TP-16, TP-20): a new
// period on the same actor, so old rows keep resolving to the same person.
func (o ops) rejoin(ctx context.Context, in RejoinIn) (RejoinOut, error) {
	if !validType(in.Type) {
		return RejoinOut{}, errs.New(errs.InvalidInput, "--type "+quote(in.Type)+" is not one of "+strings.Join(schema.PeriodTypes(), listSep))
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return RejoinOut{}, err
	}
	a, err := r.ResolveActor(ctx, in.Actor)
	if err != nil {
		return RejoinOut{}, err
	}
	if !isHuman(a) {
		return RejoinOut{}, errs.New(errs.InvalidInput, "rejoin is for people; an agent is reactivated by its owner").
			WithHint("lore actor status " + a.ID.String() + " active")
	}
	from := teamprofile.StatusOf(a.Fields)
	if teamprofile.Pickable(from) {
		return RejoinOut{}, errs.New(errs.StatusChange, nameOf(a)+" is "+from+", not left or archived").
			WithHint("nothing to rejoin; lore actor status " + in.Actor + " active ends a leave")
	}
	today := r.Today()
	on, err := day("on", in.On, today)
	if err != nil {
		return RejoinOut{}, err
	}
	if on.After(today) {
		return RejoinOut{}, errs.New(errs.InvalidInput, "--on "+dayText(on)+" is in the future; a rejoin is not scheduled").
			WithHint("use --on " + dayText(today) + " (today) or a past day: run it on or after their first day back")
	}
	ps, err := periodsOf(ctx, r, a)
	if err != nil {
		return RejoinOut{}, err
	}
	next := teamprofile.Period{ActorID: a.ID, Type: in.Type, StartedOn: on, OrgRole: in.OrgRole, Note: in.Note}
	var lastEnd time.Time
	for _, p := range ps {
		if p.Open() {
			return RejoinOut{}, errs.New(errs.PeriodOverlap, "the stint from "+dayText(p.StartedOn)+" is still open").
				WithHint("end it first: lore actor period edit " + p.ID.String() + " --ended-on <their last day>")
		}
		if p.EndedOn.After(lastEnd) {
			lastEnd = p.EndedOn
		}
	}
	// B2-24: a stint that ended today, rejoined today (a removal undone
	// the same day), is reopened; a new stint starting that day would overlap
	// it (an end day is inclusive), which left no way back before tomorrow.
	var reopen *teamprofile.Period
	for i := range ps {
		if p := ps[i]; !p.Synthesised() && !p.Open() && teamprofile.Day(p.EndedOn).Equal(teamprofile.Day(on)) && teamprofile.Day(on).Equal(teamprofile.Day(today)) {
			reopen = &ps[i]
		}
	}
	if reopen != nil {
		next.StartedOn = reopen.StartedOn
	} else if err = teamprofile.CheckPeriods(append(ps, next)); err != nil {
		return RejoinOut{}, err
	}
	out := RejoinOut{ID: a.ID.String(), Name: nameOf(a), Period: periodOut(next), From: from, Memberships: []MemberEnd{}, Agents: []AgentOut{}, Where: r.Where()}
	// The memberships the last offboard ended (TP-16: listed, not restored).
	ended, err := memberships(ctx, o.env, r, a.ID, func(m row.Row) bool {
		return !lastEnd.IsZero() && text(m, schema.FieldMemberEndedOn) == dayText(lastEnd) &&
			text(m, schema.FieldMemberEndReason) == schema.MemberEndReasonOffboarded
	})
	if err != nil {
		return RejoinOut{}, err
	}
	for _, m := range ended {
		out.Memberships = append(out.Memberships, MemberEnd{ID: m.row.ID.String(), Project: m.project, Roles: m.roles})
	}
	trashed, err := trashedUnstarted(ctx, o.env, r, a.ID)
	if err != nil {
		return RejoinOut{}, err
	}
	out.TrashedMemberships = make([]MemberEnd, 0, len(trashed))
	for _, m := range trashed {
		out.TrashedMemberships = append(out.TrashedMemberships, MemberEnd{ID: m.row.ID.String(), Project: m.project, Roles: m.roles,
			StartsOn: dayText(m.started), NotStarted: true})
	}
	agents, err := ownedAgents(ctx, r, a.ID, false)
	if err != nil {
		return RejoinOut{}, err
	}
	for _, g := range agents {
		out.Agents = append(out.Agents, AgentOut{ID: g.ID.String(), Name: agentName(g)})
	}
	if !op.Confirmed(ctx) {
		q := out.Preview()
		return RejoinOut{}, errs.New(errs.NeedsConfirm, strings.TrimRight(q, "\n")).WithQuestion(q + "rejoin " + nameOf(a) + "?").WithHint(confirmHint)
	}
	// Write: the synthesised stint first (TP-22: never lost once a row
	// exists), the new period, then the actor.
	for _, p := range ps {
		if _, err = materialise(ctx, r, p); err != nil {
			return RejoinOut{}, err
		}
	}
	f, err := next.Fields()
	if err != nil {
		return RejoinOut{}, err
	}
	patch, err := statusPatch(a, schema.ActorStatusActive, on, time.Time{}, time.Time{})
	if err != nil {
		return RejoinOut{}, err
	}
	var w row.Row
	err = inOneCommit(ctx, "rejoin "+nameOf(a), func(ctx context.Context) error { // B1-21: the stint and the status, one commit
		var werr error
		if reopen != nil {
			pf := map[string]value.Value{schema.FieldPeriodEndedOn: value.Null{}, schema.FieldPeriodEndReason: value.Null{}}
			for _, k := range []string{schema.FieldPeriodType, schema.FieldPeriodOrgRole, schema.FieldPeriodNote} {
				if v, ok := f[k]; ok {
					pf[k] = v
				}
			}
			w, werr = r.Update(ctx, schema.TableActorPeriods, reopen.ID, pf)
		} else {
			w, werr = r.Create(ctx, schema.TableActorPeriods, f)
		}
		if werr != nil {
			return werr
		}
		_, werr = r.Update(ctx, schema.TableActors, a.ID, patch)
		return werr
	})
	if err != nil {
		return RejoinOut{}, err
	}
	r.Push(ctx)
	out.Period.ID, out.Applied = w.ID.String(), true
	out.Warnings = r.Warnings()
	return out, nil
}

// validType reports whether s is a period type.
func validType(s string) bool {
	for _, t := range schema.PeriodTypes() {
		if s == t {
			return true
		}
	}
	return false
}
