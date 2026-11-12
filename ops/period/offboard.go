package period

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// OffboardIn is the input of actor.offboard.
type OffboardIn struct {
	Actor   string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	On      string `json:"on,omitempty" flag:"on" desc:"their last day, YYYY-MM-DD (default today; not in the future)"`
	Reason  string `json:"reason,omitempty" flag:"reason" desc:"why they left (stored plain on the stint)"`
	Note    string `json:"note,omitempty" flag:"note" desc:"a note on the stint"`
	Archive bool   `json:"archive,omitempty" flag:"archive" desc:"archive them too (out of actor list; --archived shows them)"`
}

// MemberEnd is one membership an offboard ends (TP-16), or one a rejoin
// lists for re-adding. One that has not started yet (StartsOn after
// today, NotStarted) cannot be ended (an ended_on before started_on is
// what member remove refuses), so the offboard moves it to the trash
// instead (reason MemberTrashReasonUnstarted, restorable) and EndsOn is
// empty: no membership outlives the stint.
type MemberEnd struct {
	ID         string   `json:"id"`
	Project    string   `json:"project"`
	Roles      []string `json:"roles"`
	EndsOn     string   `json:"ends_on,omitempty"`
	StartsOn   string   `json:"starts_on,omitempty"`
	NotStarted bool     `json:"not_started,omitempty"`
}

// MemberTrashReasonUnstarted is the trash reason of a membership an
// offboard removed before it started (MemberEnd.NotStarted); `lore trash
// restore <id>` brings it back.
const MemberTrashReasonUnstarted = "offboarded before it started"

// AgentOut is one owned agent an offboard marks inactive (TP-18).
type AgentOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OffboardOut is the offboarding preview, and after the confirm the result.
type OffboardOut struct {
	ID   string `json:"id"`
	Name string `json:"display_name"`
	On   string `json:"on"`
	// Period is the stint that ends; zero (HasPeriod false) when none is
	// open (a run that stopped after ending it).
	Period      PeriodOut   `json:"period,omitzero"`
	HasPeriod   bool        `json:"has_period"`
	Memberships []MemberEnd `json:"memberships"`
	OpenTasks   []OpenTask  `json:"open_tasks"`
	// TasksNote says why open tasks could not be listed ("" when they were).
	TasksNote string     `json:"tasks_note,omitempty"`
	Agents    []AgentOut `json:"agents"`
	// Teams are the @handles of the teams the person is taken off (B2-12:
	// a removed person no longer shows as a team member).
	Teams   []string `json:"teams,omitempty"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Applied bool     `json:"applied"`
	Where   string   `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Preview is the TEAM_PROFILES 5.1 preview block.
func (o OffboardOut) Preview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "offboard %s (%s) on %s:\n", o.Name, o.ID, o.On)
	if o.HasPeriod {
		fmt.Fprintf(&b, "  period     %s\n", periodLine(o.Period))
	} else {
		b.WriteString("  period     (no open stint)\n")
	}
	lead := "  projects   "
	if len(o.Memberships) == 0 {
		b.WriteString(lead + "(none)\n")
	}
	for _, m := range o.Memberships {
		if m.NotStarted {
			fmt.Fprintf(&b, "%s%s (%s) not started yet (starts %s): ends unstarted\n", lead, m.Project, strings.Join(m.Roles, listSep), m.StartsOn)
		} else {
			fmt.Fprintf(&b, "%s%s (%s) ends %s\n", lead, m.Project, strings.Join(m.Roles, listSep), m.EndsOn)
		}
		lead = "             "
	}
	lead = "  open tasks "
	switch {
	case o.TasksNote != "":
		b.WriteString(lead + o.TasksNote + "\n")
		lead = "             "
	case len(o.OpenTasks) == 0:
		b.WriteString(lead + "(none)\n")
	}
	for _, t := range o.OpenTasks {
		fmt.Fprintf(&b, "%s%s  %q  %s  %s  reassign: lore task edit %s --assign @<someone>\n", lead, t.ID, t.Title, t.Store, t.How, t.ID)
		lead = "             "
	}
	lead = "  agents     "
	if len(o.Agents) == 0 {
		b.WriteString(lead + "(none)\n")
	}
	for _, a := range o.Agents {
		fmt.Fprintf(&b, "%s%s becomes inactive\n", lead, a.Name)
		lead = "             "
	}
	if len(o.Teams) > 0 {
		fmt.Fprintf(&b, "  teams      @%s: taken off\n", strings.Join(o.Teams, ", @"))
	}
	fmt.Fprintf(&b, "  status     %s%s%s\n", o.From, arrow, o.To)
	return b.String()
}

// Text is the preview and the done line (5.1 step 6).
func (o OffboardOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o OffboardOut) text() string {
	if !o.Applied {
		return o.Preview()
	}
	ended, unstarted := 0, 0
	for _, m := range o.Memberships {
		if m.NotStarted {
			unstarted++
		} else {
			ended++
		}
	}
	trashed := ""
	if unstarted > 0 {
		trashed = fmt.Sprintf(", %d not started moved to the trash", unstarted)
	}
	return o.Preview() + fmt.Sprintf("%s %s %s offboarded on %s (%d memberships ended%s, %d agents inactive, %d open tasks listed above)\n",
		success, o.ID, o.Name, o.On, ended, trashed, len(o.Agents), len(o.OpenTasks))
}

// offboard is actor.offboard (TEAM_PROFILES 5.1, TP-16..TP-18, TP-20).
func (o ops) offboard(ctx context.Context, in OffboardIn) (OffboardOut, error) {
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return OffboardOut{}, err
	}
	a, err := r.ResolveActor(ctx, in.Actor)
	if err != nil {
		return OffboardOut{}, err
	}
	// Step 1: people only, and only someone still here.
	if !isHuman(a) {
		return OffboardOut{}, errs.New(errs.InvalidInput, "offboard is for people; agents follow their owner").
			WithHint("lore actor status " + a.ID.String() + " left")
	}
	from := teamprofile.StatusOf(a.Fields)
	if !teamprofile.Pickable(from) {
		msg := nameOf(a) + " is already " + from
		if s := text(a, schema.FieldActorLeftAt); s != "" {
			if t, perr := time.Parse(time.RFC3339Nano, s); perr == nil {
				msg = nameOf(a) + " already left on " + dayText(t)
			}
		}
		return OffboardOut{}, errs.New(errs.StatusChange, msg).WithHint("lore actor rejoin " + in.Actor + " --type employee|contractor|intern|client_contact")
	}
	// Step 2: the day.
	today := r.Today()
	on, err := day("on", in.On, today)
	if err != nil {
		return OffboardOut{}, err
	}
	if on.After(today) {
		return OffboardOut{}, errs.New(errs.InvalidInput, "--on "+dayText(on)+" is in the future; offboarding is not scheduled").
			WithHint("run it on their last day, or with a past --on")
	}
	plan, err := o.offboardPlan(ctx, r, a, on, in)
	if err != nil {
		return OffboardOut{}, err
	}
	// Step 4: nothing is written before the confirm.
	if !op.Confirmed(ctx) {
		q := plan.out.Preview()
		return OffboardOut{}, errs.New(errs.NeedsConfirm, strings.TrimRight(q, "\n")).WithQuestion(q + "offboard " + nameOf(a) + "?").WithHint(confirmHint)
	}
	if err = plan.apply(ctx, r); err != nil {
		return OffboardOut{}, err
	}
	r.Push(ctx)
	plan.out.Applied = true
	plan.out.Warnings = r.Warnings()
	return plan.out, nil
}

// offboardPlan is the preview (step 3) and what the confirm writes.
type offboardPlan struct {
	out     OffboardOut
	actor   row.Row
	on      time.Time
	today   time.Time // the roster's day: a membership starting after it is trashed, not ended
	period  *teamprofile.Period
	members []membership
	agents  []row.Row
	teams   []row.Row // live teams listing the person, each written without them
	reason  string
	note    string
	to      string
}

// offboardPlan builds the preview without writing.
func (o ops) offboardPlan(ctx context.Context, r profilestore.Roster, a row.Row, on time.Time, in OffboardIn) (offboardPlan, error) {
	to := schema.ActorStatusLeft
	if in.Archive {
		to = schema.ActorStatusArchived
	}
	today := r.Today()
	p := offboardPlan{actor: a, on: on, today: today, reason: in.Reason, note: in.Note, to: to,
		out: OffboardOut{ID: a.ID.String(), Name: nameOf(a), On: dayText(on), From: teamprofile.StatusOf(a.Fields), To: to,
			Memberships: []MemberEnd{}, OpenTasks: []OpenTask{}, Agents: []AgentOut{}, Where: r.Where()}}
	ps, err := periodsOf(ctx, r, a)
	if err != nil {
		return offboardPlan{}, err
	}
	if open, ok := openPeriod(ps); ok {
		if on.Before(open.StartedOn) {
			return offboardPlan{}, errs.New(errs.PeriodOverlap, "--on "+dayText(on)+" is before their stint started on "+dayText(open.StartedOn)).
				WithHint("lore actor period list " + in.Actor + " shows the stints; pick a date on or after the start")
		}
		open.EndedOn, open.EndReason = on, in.Reason
		if in.Note != "" {
			open.Note = in.Note
		}
		p.period, p.out.Period, p.out.HasPeriod = &open, periodOut(open), true
	}
	if p.members, err = openMemberships(ctx, o.env, r, a.ID); err != nil {
		return offboardPlan{}, err
	}
	for _, m := range p.members {
		if m.started.After(today) {
			// Not started yet: trashed by apply, never ended before it began.
			p.out.Memberships = append(p.out.Memberships, MemberEnd{ID: m.row.ID.String(), Project: m.project, Roles: m.roles,
				StartsOn: dayText(m.started), NotStarted: true})
			continue
		}
		if on.Before(m.started) {
			return offboardPlan{}, errs.New(errs.PeriodOverlap, "--on "+dayText(on)+" is before their membership of "+m.project+" started on "+dayText(m.started)).
				WithHint("lore project member list " + m.project + " --history; pick a date on or after it")
		}
		p.out.Memberships = append(p.out.Memberships, MemberEnd{ID: m.row.ID.String(), Project: m.project, Roles: m.roles, EndsOn: dayText(on)})
	}
	if tr, ok := upgrade.As[TaskReader](o.env); ok {
		ts, terr := tr.OpenTasks(ctx, a.ID)
		var partial *ClaimsUnreadError
		switch {
		case errors.As(terr, &partial):
			p.out.OpenTasks = append(p.out.OpenTasks, partial.Tasks...)
			p.out.TasksNote = "claim holders could not be read here (" + partial.Reason + "); run `lore task list --claimed` in each repo"
		case terr != nil:
			// TP-17 lists, it never changes: a store that cannot be read
			// here does not block leaving, but the preview says so.
			p.out.TasksNote = "could not be read here (" + terr.Error() + "); run `lore task list --assignee " + in.Actor + "` in each repo"
		case ts != nil:
			p.out.OpenTasks = ts
		}
	} else {
		p.out.TasksNote = "not listed by this surface; run `lore task list --assignee " + in.Actor + "` in each repo"
	}
	if p.agents, err = ownedAgents(ctx, r, a.ID, true); err != nil {
		return offboardPlan{}, err
	}
	for _, g := range p.agents {
		p.out.Agents = append(p.out.Agents, AgentOut{ID: g.ID.String(), Name: agentName(g)})
	}
	if p.teams, err = teamsListing(ctx, r, a.ID.String()); err != nil {
		return offboardPlan{}, err
	}
	for _, t := range p.teams {
		p.out.Teams = append(p.out.Teams, text(t, fieldTeamHandle))
	}
	return p, nil
}

// fieldTeamHandle is the teams row's @handle field.
const fieldTeamHandle = "handle"

// teamsListing is every teams row whose member list holds actor (B2-12:
// offboard takes the person off them, so member show, team show and the
// task team filters stop counting someone who left).
func teamsListing(ctx context.Context, r profilestore.Roster, actor string) ([]row.Row, error) {
	ts, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, t := range ts {
		if slices.Contains(value.ItemStrings(t.Fields[schema.FieldTeamMembers]), actor) {
			out = append(out, t)
		}
	}
	return out, nil
}

// withoutMember is t's member list without actor, as the ref list the
// teams table stores.
func withoutMember(t row.Row, actor string) (value.Value, error) {
	var refs []value.Value
	for _, s := range value.ItemStrings(t.Fields[schema.FieldTeamMembers]) {
		if s == actor {
			continue
		}
		id, err := ids.ParseAny(s)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "team member id "+s+" is not valid", err)
		}
		ref, err := value.RefOf(id)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "team member id "+s+" is not a valid ref", err)
		}
		refs = append(refs, ref)
	}
	l, err := value.ListOf(value.KindRef, refs...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "the team member list", err)
	}
	return l, nil
}

// apply writes the offboard in the 5.1 step 5 order: memberships, agents,
// the period (materialised first), and last the actor.
func (p offboardPlan) apply(ctx context.Context, r profilestore.Roster) error {
	endOn, err := dateValue("--on", p.on)
	if err != nil {
		return err
	}
	offboarded, err := textValue(schema.FieldMemberEndReason, schema.MemberEndReasonOffboarded)
	if err != nil {
		return err
	}
	for _, m := range p.members { // TP-16: the same date
		if m.started.After(p.today) {
			if err = r.Delete(ctx, schema.TableProjectMembers, m.row.ID, MemberTrashReasonUnstarted); err != nil {
				return err
			}
			continue
		}
		if _, err = r.Update(ctx, schema.TableProjectMembers, m.row.ID, map[string]value.Value{
			schema.FieldMemberEndedOn: endOn, schema.FieldMemberEndReason: offboarded}); err != nil {
			return err
		}
	}
	for _, t := range p.teams { // B2-12: off every team
		ml, terr := withoutMember(t, p.actor.ID.String())
		if terr != nil {
			return terr
		}
		if _, err = r.Update(ctx, schema.TableTeams, t.ID, map[string]value.Value{schema.FieldTeamMembers: ml}); err != nil {
			return err
		}
	}
	for _, g := range p.agents { // TP-18
		patch, perr := statusPatch(g, schema.ActorStatusLeft, p.on, time.Time{}, time.Time{})
		if perr != nil {
			return perr
		}
		if _, err = r.Update(ctx, schema.TableActors, g.ID, patch); err != nil {
			return err
		}
	}
	if p.period != nil {
		// The open period as it is stored, then the end on top of it.
		stored := *p.period
		stored.EndedOn, stored.EndReason = time.Time{}, ""
		m, merr := materialise(ctx, r, stored)
		if merr != nil {
			return merr
		}
		patch := map[string]value.Value{schema.FieldPeriodEndedOn: endOn}
		for field, s := range map[string]string{schema.FieldPeriodEndReason: p.reason, schema.FieldPeriodNote: p.note} {
			if s == "" {
				continue
			}
			if patch[field], err = textValue(field, s); err != nil {
				return err
			}
		}
		if _, err = r.Update(ctx, schema.TableActorPeriods, m.ID, patch); err != nil {
			return err
		}
	}
	patch, err := statusPatch(p.actor, p.to, p.on, time.Time{}, time.Time{})
	if err != nil {
		return err
	}
	_, err = r.Update(ctx, schema.TableActors, p.actor.ID, patch)
	return err
}
