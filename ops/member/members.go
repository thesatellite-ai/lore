package member

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// Project memberships (TEAM_PROFILES 2.6, 3.5, 5.4; TP-10, TP-12, TP-13,
// TP-15). One project_members row per stint of a person on a project.
//
// Flow:
//
//	member.add    ──▶ project, actor (left/archived: E_STATUS_CHANGE), roles live?
//	                  open stint?  yes ──▶ union roles (run twice = same result)
//	                               no  ──▶ shares a day with an ended stint not starting on
//	                                        --on (inside, its last day, or one starting later;
//	                                        teamprofile.Span)? E_PERIOD_OVERLAP
//	                                        ended stint with the same started_on?
//	                                        yes ──▶ reopen it (same natural id, 5.4)
//	                                        no  ──▶ Create a NEW row (TP-12)
//	member.edit   ──▶ open stint (else E_NOT_FOUND) ──▶ set its roles
//	member.remove ──▶ open stint (else E_NOT_FOUND) ──▶ ended_on + end_reason
//	                  (the row is kept, never deleted, TP-12) + the TP-13 warning
//	member.list   ──▶ open stints (--history: every stint) + the TP-13 warnings
//
// Two open stints of one (project, actor) after a merge are read as one
// (union of roles, earliest start); edit and remove write every one of
// them, so the next read is clean again.

// Field names of other tables this package reads (schema keeps them
// unexported or as literals).
const (
	fieldActorHandle  = "handle"
	fieldActorDisplay = "display_name"
	fieldProjectName  = "name"
	fieldTeamProject  = "project_id"
	fieldTeamHandle   = "handle"
	fieldTeamActive   = "active"
	boolFalse         = "false"
)

// WarnTeamNotMember is the TP-13 warning code: a member of an org @team
// who has no open membership of the project. Teams are org-wide groups
// (usable in every project of the org), so every team of the org counts,
// not only one "of" the project, and the line says "the org team".
const WarnTeamNotMember = "W_TEAM_NOT_MEMBER"

// alreadyMember is the message of an add on a current member.
const alreadyMember = "already a member"

// membership is one project_members row, read.
type membership struct {
	ID        ids.AnyID
	Project   string
	Actor     string
	Roles     []string
	StartedOn time.Time
	EndedOn   time.Time
	EndReason string
	Note      string
	// w is the row's writers (auditrec), for the list record.
	w auditrec.Writers
}

func (m membership) open() bool { return m.EndedOn.IsZero() }

// membershipOf reads a project_members row; a date that does not parse is
// E_INVALID_INPUT naming the row (a stint is never guessed).
func membershipOf(r row.Row) (membership, error) {
	m := membership{ID: r.ID, Project: fieldText(r, schema.FieldMemberProject), Actor: fieldText(r, schema.FieldMemberActor),
		Roles: value.ItemStrings(r.Fields[schema.FieldMemberRoles]), EndReason: fieldText(r, schema.FieldMemberEndReason),
		Note: fieldText(r, schema.FieldMemberNote), w: auditrec.Of(r)}
	var err error
	if m.StartedOn, err = dayField(r, schema.FieldMemberStartedOn); err != nil {
		return membership{}, err
	}
	if m.EndedOn, err = dayField(r, schema.FieldMemberEndedOn); err != nil {
		return membership{}, err
	}
	return m, nil
}

// dayField reads a date field (zero when absent).
func dayField(r row.Row, name string) (time.Time, error) {
	s := fieldText(r, name)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(value.DateLayout, s)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.InvalidInput, "membership "+r.ID.String()+": "+name+" "+s+" is not YYYY-MM-DD", err).
			WithHint("lore show " + r.ID.String())
	}
	return t, nil
}

// readMemberships lists every live project_members row of the roster.
func readMemberships(ctx context.Context, r profilestore.Roster) ([]membership, error) {
	rows, err := r.List(ctx, schema.TableProjectMembers)
	if err != nil {
		return nil, err
	}
	out := make([]membership, 0, len(rows))
	for _, x := range rows {
		if x.Deleted {
			continue
		}
		m, merr := membershipOf(x)
		if merr != nil {
			return nil, merr
		}
		out = append(out, m)
	}
	return out, nil
}

// stintsOf is every stint of actor on project, sorted by started_on.
func stintsOf(all []membership, project, actor string) []membership {
	var out []membership
	for _, m := range all {
		if m.Project == project && m.Actor == actor {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b membership) int { return a.StartedOn.Compare(b.StartedOn) })
	return out
}

// opens keeps the open stints.
func opens(ms []membership) []membership {
	var out []membership
	for _, m := range ms {
		if m.open() {
			out = append(out, m)
		}
	}
	return out
}

// unionRoles is a ∪ b in first-seen order.
func unionRoles(a, b []string) []string {
	out := slices.Clone(a)
	for _, n := range b {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// rolesValue is roles as the list-of-text field value.
func rolesValue(roles []string) (value.List, error) {
	items := make([]value.Value, 0, len(roles))
	for _, n := range roles {
		t, err := value.TextOf(n)
		if err != nil {
			return value.List{}, errs.Wrap(errs.InvalidInput, "role "+work.Quote(n), err)
		}
		items = append(items, t)
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		return value.List{}, errs.Wrap(errs.InvalidInput, "roles", err)
	}
	return l, nil
}

// dateValue is t's day as a date value.
func dateValue(t time.Time) (value.Date, error) {
	d, err := value.DateOf(teamprofile.Day(t).Format(value.DateLayout))
	if err != nil {
		return value.Date{}, errs.Wrap(errs.InvalidInput, "a date out of range", err)
	}
	return d, nil
}

// parseOn reads --on (YYYY-MM-DD), default today.
func parseOn(s string, today time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return today, nil
	}
	t, err := time.Parse(value.DateLayout, s)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.InvalidInput, "--on "+work.Quote(s)+" is not a date", err).WithHint("--on 2026-09-30")
	}
	return t, nil
}

// resolveProject resolves <project>: the ProjectFinder upgrade when env
// has it, else the roster store's own projects rows, else a prj_ id as
// given (project_id is AllowDangling, 2.6).
func resolveProject(ctx context.Context, env Env, r profilestore.Roster, ref string) (Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Project{}, errs.New(errs.InvalidInput, "no project").WithHint("lore project member list <project>")
	}
	if f := finder(env); f != nil {
		return f.FindProject(ctx, ref)
	}
	rows, err := r.List(ctx, schema.TableProjects)
	if err != nil {
		return Project{}, err
	}
	var hits []row.Row
	for _, x := range rows {
		if !x.Deleted && (x.ID.String() == ref || fieldText(x, fieldProjectName) == ref) {
			hits = append(hits, x)
		}
	}
	switch len(hits) {
	case 1:
		id, perr := ids.As[ids.Project](hits[0].ID)
		if perr != nil {
			return Project{}, errs.Wrap(errs.Internal, "project id "+hits[0].ID.String(), perr)
		}
		return Project{ID: id, Name: fieldText(hits[0], fieldProjectName)}, nil
	case 0:
	default:
		return Project{}, errs.New(errs.ProjectNameCollision, "several projects are named "+work.Quote(ref)).WithHint("name it by its prj_ id")
	}
	if id, perr := ids.Parse[ids.Project](ref); perr == nil {
		return Project{ID: id}, nil
	}
	return Project{}, errs.New(errs.NotFound, "no project "+work.Quote(ref)+" on the roster in "+r.Where()).
		WithHint("run `lore project list` to see the projects, or name it by its prj_ id")
}

// projectShown names project id for output: its name when it resolves,
// else the id.
func projectShown(ctx context.Context, env Env, r profilestore.Roster, id string, cache map[string]string) string {
	if s, ok := cache[id]; ok {
		return s
	}
	shown := id
	if p, err := resolveProject(ctx, env, r, id); err == nil {
		shown = p.Shown()
	}
	cache[id] = shown
	return shown
}

// actorShown is "@handle", else the display name, else the id.
func actorShown(a row.Row) string {
	if h := fieldText(a, fieldActorHandle); h != "" {
		return handleMark + h
	}
	if n := fieldText(a, fieldActorDisplay); n != "" {
		return n
	}
	return a.ID.String()
}

// MemberOut is one stint, as shown.
type MemberOut struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"project_id"`
	Project   string   `json:"project"`
	ActorID   string   `json:"actor_id"`
	Actor     string   `json:"actor"`
	Roles     []string `json:"roles"`
	StartedOn string   `json:"started_on"`
	EndedOn   string   `json:"ended_on,omitempty"`
	EndReason string   `json:"end_reason,omitempty"`
	Note      string   `json:"note,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

func memberOut(m membership, project, actor string) MemberOut {
	o := MemberOut{ID: m.ID.String(), ProjectID: m.Project, Project: project, ActorID: m.Actor, Actor: actor, Roles: slices.Clone(m.Roles),
		StartedOn: m.StartedOn.Format(value.DateLayout), EndReason: m.EndReason, Note: m.Note,
		CreatedBy: m.w.CreatedBy, UpdatedBy: m.w.UpdatedBy}
	if o.Roles == nil {
		o.Roles = []string{}
	}
	if !m.open() {
		o.EndedOn = m.EndedOn.Format(value.DateLayout)
	}
	return o
}

// line is one member line: "@mira  dev, reviewer  since 2026-01-12".
func (o MemberOut) line(withProject bool) string {
	var b strings.Builder
	if withProject {
		b.WriteString(o.Project + "  ")
	}
	b.WriteString(o.Actor + "  " + strings.Join(o.Roles, ", "))
	if o.EndedOn == "" {
		b.WriteString("  since " + o.StartedOn)
	} else {
		b.WriteString("  " + o.StartedOn + " → " + o.EndedOn)
		if o.EndReason != "" {
			b.WriteString(" (" + o.EndReason + ")")
		}
	}
	return b.String()
}

// MemberAddIn is the input of project.member.add.
type MemberAddIn struct {
	Project string      `json:"project" arg:"0" desc:"the project: a name or prj_ id"`
	Actor   op.ActorRef `json:"actor" arg:"1" desc:"the person: @handle, act_ id or stable key"`
	Roles   []string    `json:"roles" flag:"role" desc:"role names, comma-separated or repeated (lore role list)"`
	On      string      `json:"on,omitempty" flag:"on" desc:"first day, YYYY-MM-DD (default today)"`
	Note    string      `json:"note,omitempty" flag:"note" desc:"a note on the membership"`
}

// MemberEditIn is the input of project.member.edit.
type MemberEditIn struct {
	Project string      `json:"project" arg:"0" desc:"the project: a name or prj_ id"`
	Actor   op.ActorRef `json:"actor" arg:"1" desc:"the person: @handle, act_ id or stable key"`
	Roles   []string    `json:"roles" flag:"role" desc:"the roles the membership holds from now on (replaces them)"`
}

// MemberRemoveIn is the input of project.member.remove.
type MemberRemoveIn struct {
	Project string      `json:"project" arg:"0" desc:"the project: a name or prj_ id"`
	Actor   op.ActorRef `json:"actor" arg:"1" desc:"the person: @handle, act_ id or stable key"`
	On      string      `json:"on,omitempty" flag:"on" desc:"last day, YYYY-MM-DD (default today)"`
	Reason  string      `json:"reason,omitempty" flag:"reason" desc:"why (default: removed)"`
}

// MemberWriteOut is the membership after add, edit or remove.
type MemberWriteOut struct {
	Member MemberOut `json:"member"`
	// Verb is added, reopened, already_member, updated or removed.
	Verb     string   `json:"verb"`
	Warnings []string `json:"warnings,omitempty"`
	Where    string   `json:"where"`
}

// Member write verbs.
const (
	verbMemberAdded    = "added"
	verbMemberReopened = "reopened"
	verbMemberAlready  = "already_member"
	verbMemberUpdated  = "updated"
	verbMemberRemoved  = "removed"
)

// Text is one success line, then the warnings.
func (o MemberWriteOut) Text() string {
	m := o.Member
	roles := strings.Join(m.Roles, ", ")
	var line string
	switch o.Verb {
	case verbMemberAlready:
		line = work.Success + " " + m.Actor + " " + alreadyMember + " of " + m.Project + "; roles now " + roles
	case verbMemberUpdated:
		line = work.Success + " " + m.Actor + " on " + m.Project + ": roles now " + roles
	case verbMemberRemoved:
		line = work.Success + " " + m.Actor + " removed from " + m.Project + " on " + m.EndedOn + " (" + m.EndReason + "; history kept)"
	default: // added, reopened
		line = work.Success + " " + m.Actor + " added to " + m.Project + " as " + roles + " from " + m.StartedOn + " (" + m.ID + ")"
	}
	var b strings.Builder
	b.WriteString(line + "\n")
	for _, w := range o.Warnings {
		b.WriteString(w + "\n")
	}
	return b.String()
}

// memberCtx is what every member write resolves first.
type memberCtx struct {
	r       profilestore.Roster
	project Project
	actor   row.Row
	all     []membership
	stints  []membership
}

// openMember opens the roster and resolves project and actor.
func openMember(ctx context.Context, env Env, write bool, project string, actor op.ActorRef) (memberCtx, func(), error) {
	r, done, err := env.Roster(ctx, write)
	if err != nil {
		return memberCtx{}, done, err
	}
	p, err := resolveProject(ctx, env, r, project)
	if err != nil {
		return memberCtx{}, done, err
	}
	a, err := r.ResolveActor(ctx, string(actor))
	if err != nil {
		return memberCtx{}, done, err
	}
	all, err := readMemberships(ctx, r)
	if err != nil {
		return memberCtx{}, done, err
	}
	return memberCtx{r: r, project: p, actor: a, all: all, stints: stintsOf(all, p.ID.String(), a.ID.String())}, done, nil
}

func memberAdd(ctx context.Context, env Env, in MemberAddIn) (MemberWriteOut, error) {
	want, err := parseRoles(in.Roles)
	if err != nil {
		return MemberWriteOut{}, err
	}
	mc, done, err := openMember(ctx, env, true, in.Project, in.Actor)
	defer done()
	if err != nil {
		return MemberWriteOut{}, err
	}
	// TP-15: a left or archived person is out of every assign list,
	// projects included; rejoin first.
	if st := teamprofile.StatusOf(mc.actor.Fields); !teamprofile.Pickable(st) {
		return MemberWriteOut{}, errs.New(errs.StatusChange, actorShown(mc.actor)+" is "+st+"; someone who is "+st+" cannot be added to a project").
			WithHint("lore actor rejoin " + string(in.Actor))
	}
	cur := opens(mc.stints)
	var keep []string
	for _, m := range cur {
		keep = append(keep, m.Roles...)
	}
	roles, err := readRoles(ctx, mc.r)
	if err != nil {
		return MemberWriteOut{}, err
	}
	if err := checkRoles(roles, want, keep); err != nil {
		return MemberWriteOut{}, err
	}
	out := MemberWriteOut{Where: mc.r.Where()}
	if len(cur) > 0 {
		return addToOpen(ctx, mc, cur, want, in.Note, out)
	}
	on, err := parseOn(in.On, mc.r.Today())
	if err != nil {
		return MemberWriteOut{}, err
	}
	rolesV, err := rolesValue(want)
	if err != nil {
		return MemberWriteOut{}, err
	}
	onV, err := dateValue(on)
	if err != nil {
		return MemberWriteOut{}, err
	}
	// TP-12: the new stint is open ([on, ∞)), so it may not share a day
	// with any ended stint of the pair: one it starts inside, one whose
	// last day it starts on (ended_on is the last day, as a company
	// period's is: 5.2), or one that starts later (added with a future
	// --on and ended before it began). Same code a period uses for that
	// boundary (E_PERIOD_OVERLAP); the stint that begins on --on is not
	// counted, it is the one 5.4 reopens below.
	span := teamprofile.Span{Start: on}
	for _, m := range mc.stints {
		if m.StartedOn.Equal(on) || !span.Overlaps(teamprofile.Span{Start: m.StartedOn, End: m.EndedOn}) {
			continue
		}
		if err := overlapErr(mc, m, on); err != nil {
			return MemberWriteOut{}, err
		}
	}
	// 5.4: re-adding on the day an ended stint began has that stint's
	// natural id, so that row is reopened instead of a twin made.
	for _, m := range mc.stints {
		if m.StartedOn.Equal(on) {
			patch := map[string]value.Value{schema.FieldMemberRoles: rolesV, schema.FieldMemberEndedOn: value.Null{}, schema.FieldMemberEndReason: value.Null{}}
			if err := setNote(patch, in.Note); err != nil {
				return MemberWriteOut{}, err
			}
			w, uerr := mc.r.Update(ctx, schema.TableProjectMembers, m.ID, patch)
			if uerr != nil {
				return MemberWriteOut{}, uerr
			}
			return finishWrite(ctx, mc, w, verbMemberReopened, out)
		}
	}
	projRef, err := value.RefOf(mc.project.ID.Any())
	if err != nil {
		return MemberWriteOut{}, errs.Wrap(errs.InvalidInput, "project id", err)
	}
	actorRef, err := value.RefOf(mc.actor.ID)
	if err != nil {
		return MemberWriteOut{}, errs.Wrap(errs.InvalidInput, "actor id", err)
	}
	fields := map[string]value.Value{schema.FieldMemberProject: projRef, schema.FieldMemberActor: actorRef, schema.FieldMemberRoles: rolesV, schema.FieldMemberStartedOn: onV}
	if err := setNote(fields, in.Note); err != nil {
		return MemberWriteOut{}, err
	}
	w, err := mc.r.Create(ctx, schema.TableProjectMembers, fields)
	if err != nil {
		return MemberWriteOut{}, err
	}
	return finishWrite(ctx, mc, w, verbMemberAdded, out)
}

// addToOpen unions want into the open stint(s): "already a member; roles
// now dev, reviewer". Nothing is written when the union adds nothing and
// no note changes, so a second run is a no-op.
func addToOpen(ctx context.Context, mc memberCtx, cur []membership, want []string, note string, out MemberWriteOut) (MemberWriteOut, error) {
	var merged []string
	for _, m := range cur {
		merged = unionRoles(merged, m.Roles)
	}
	merged = unionRoles(merged, want)
	first := cur[0]
	shown := first
	shown.Roles = merged
	note = strings.TrimSpace(note)
	for _, m := range cur {
		if len(m.Roles) == len(merged) && (note == "" || note == m.Note) {
			continue
		}
		rv, err := rolesValue(merged)
		if err != nil {
			return MemberWriteOut{}, err
		}
		patch := map[string]value.Value{schema.FieldMemberRoles: rv}
		if err := setNote(patch, note); err != nil {
			return MemberWriteOut{}, err
		}
		if _, err := mc.r.Update(ctx, schema.TableProjectMembers, m.ID, patch); err != nil {
			return MemberWriteOut{}, err
		}
	}
	mc.r.Push(ctx)
	if note != "" {
		shown.Note = note
	}
	out.Member = memberOut(shown, mc.project.Shown(), actorShown(mc.actor))
	out.Verb = verbMemberAlready
	out.Warnings = append(mc.r.Warnings(), out.Warnings...)
	return out, nil
}

// setNote puts a non-empty note into fields.
func setNote(fields map[string]value.Value, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil
	}
	t, err := value.TextOf(note)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "--note", err)
	}
	fields[schema.FieldMemberNote] = t
	return nil
}

// finishWrite pushes and renders the written row.
func finishWrite(ctx context.Context, mc memberCtx, w row.Row, verb string, out MemberWriteOut) (MemberWriteOut, error) {
	m, err := membershipOf(w)
	if err != nil {
		return MemberWriteOut{}, err
	}
	mc.r.Push(ctx)
	out.Member = memberOut(m, mc.project.Shown(), actorShown(mc.actor))
	out.Verb = verb
	out.Warnings = append(mc.r.Warnings(), out.Warnings...)
	return out, nil
}

func memberEdit(ctx context.Context, env Env, in MemberEditIn) (MemberWriteOut, error) {
	want, err := parseRoles(in.Roles)
	if err != nil {
		return MemberWriteOut{}, err
	}
	mc, done, err := openMember(ctx, env, true, in.Project, in.Actor)
	defer done()
	if err != nil {
		return MemberWriteOut{}, err
	}
	cur := opens(mc.stints)
	if len(cur) == 0 {
		return MemberWriteOut{}, notMember(mc)
	}
	var keep []string
	for _, m := range cur {
		keep = append(keep, m.Roles...)
	}
	roles, err := readRoles(ctx, mc.r)
	if err != nil {
		return MemberWriteOut{}, err
	}
	if err := checkRoles(roles, want, keep); err != nil {
		return MemberWriteOut{}, err
	}
	rv, err := rolesValue(want)
	if err != nil {
		return MemberWriteOut{}, err
	}
	var last row.Row
	for _, m := range cur {
		if last, err = mc.r.Update(ctx, schema.TableProjectMembers, m.ID, map[string]value.Value{schema.FieldMemberRoles: rv}); err != nil {
			return MemberWriteOut{}, err
		}
	}
	return finishWrite(ctx, mc, last, verbMemberUpdated, MemberWriteOut{Where: mc.r.Where()})
}

// notMember is E_NOT_FOUND for a person with no open membership.
func notMember(mc memberCtx) error {
	return errs.New(errs.NotFound, actorShown(mc.actor)+" is not a member of "+mc.project.Shown()).
		WithHint("lore project member list " + mc.project.Shown() + " --history")
}

func memberRemove(ctx context.Context, env Env, in MemberRemoveIn) (MemberWriteOut, error) {
	mc, done, err := openMember(ctx, env, true, in.Project, in.Actor)
	defer done()
	if err != nil {
		return MemberWriteOut{}, err
	}
	cur := opens(mc.stints)
	if len(cur) == 0 {
		return MemberWriteOut{}, notMember(mc)
	}
	on, err := parseOn(in.On, mc.r.Today())
	if err != nil {
		return MemberWriteOut{}, err
	}
	for _, m := range cur {
		if on.Before(m.StartedOn) {
			return MemberWriteOut{}, errs.New(errs.PeriodOverlap, "--on "+on.Format(value.DateLayout)+" is before the membership began ("+m.StartedOn.Format(value.DateLayout)+")").
				WithHint("pick --on on or after " + m.StartedOn.Format(value.DateLayout))
		}
	}
	onV, err := dateValue(on)
	if err != nil {
		return MemberWriteOut{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = schema.MemberEndReasonRemoved
	}
	reasonV, err := value.TextOf(reason)
	if err != nil {
		return MemberWriteOut{}, errs.Wrap(errs.InvalidInput, "--reason", err)
	}
	// TP-12: the row is ended, never deleted, so --history keeps the stint.
	var last row.Row
	for _, m := range cur {
		if last, err = mc.r.Update(ctx, schema.TableProjectMembers, m.ID,
			map[string]value.Value{schema.FieldMemberEndedOn: onV, schema.FieldMemberEndReason: reasonV}); err != nil {
			return MemberWriteOut{}, err
		}
	}
	out := MemberWriteOut{Where: mc.r.Where()}
	// TP-13: the person stays in the org's teams (teams are not
	// changed); say so.
	ws, err := teamWarnings(ctx, mc.r, mc.project, nil, map[string]bool{mc.actor.ID.String(): true})
	if err != nil {
		return MemberWriteOut{}, err
	}
	out.Warnings = ws
	return finishWrite(ctx, mc, last, verbMemberRemoved, out)
}

// MemberListIn is the input of project.member.list.
type MemberListIn struct {
	Project string      `json:"project,omitempty" arg:"0" desc:"the project: a name or prj_ id (default: every project)"`
	Actor   op.ActorRef `json:"actor,omitempty" flag:"actor" desc:"only this person's memberships"`
	Role    string      `json:"role,omitempty" flag:"role" desc:"only memberships holding this role"`
	History bool        `json:"history,omitempty" flag:"history" desc:"include ended stints"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

// MemberListOut is the list.
type MemberListOut struct {
	Members []MemberOut `json:"members"`
	// Warnings are the TP-13 lines (W_TEAM_NOT_MEMBER), with a project only.
	Warnings []string `json:"warnings,omitempty"`
	// Project is the project shown, "" for every project.
	Project string `json:"project,omitempty"`
}

// Text prints one line per stint, then the warnings.
func (o MemberListOut) Text() string {
	var b strings.Builder
	if len(o.Members) == 0 {
		b.WriteString("no members\n")
	}
	for _, m := range o.Members {
		b.WriteString(m.line(o.Project == "") + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString(w + "\n")
	}
	return b.String()
}

func memberList(ctx context.Context, env Env, in MemberListIn) (MemberListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return MemberListOut{}, err
	}
	out := MemberListOut{Members: []MemberOut{}}
	var project Project
	if strings.TrimSpace(in.Project) != "" {
		if project, err = resolveProject(ctx, env, r, in.Project); err != nil {
			return MemberListOut{}, err
		}
		out.Project = project.Shown()
	}
	actorFilter := ""
	if in.Actor != "" {
		a, aerr := r.ResolveActor(ctx, string(in.Actor))
		if aerr != nil {
			return MemberListOut{}, aerr
		}
		actorFilter = a.ID.String()
	}
	all, err := readMemberships(ctx, r)
	if err != nil {
		return MemberListOut{}, err
	}
	actors, err := actorIndex(ctx, r)
	if err != nil {
		return MemberListOut{}, err
	}
	shown := readView(all, in.History)
	names := map[string]string{}
	for _, m := range shown {
		if out.Project != "" && m.Project != project.ID.String() {
			continue
		}
		if actorFilter != "" && m.Actor != actorFilter {
			continue
		}
		if in.Role != "" && !slices.Contains(m.Roles, in.Role) {
			continue
		}
		pn := out.Project
		if pn == "" {
			pn = projectShown(ctx, env, r, m.Project, names)
		}
		an := m.Actor
		if a, ok := actors[m.Actor]; ok {
			an = actorShown(a)
		}
		out.Members = append(out.Members, memberOut(m, pn, an))
	}
	slices.SortStableFunc(out.Members, func(a, b MemberOut) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		if c := strings.Compare(a.Actor, b.Actor); c != 0 {
			return c
		}
		return strings.Compare(a.StartedOn, b.StartedOn)
	})
	if out.Project != "" {
		if out.Warnings, err = teamWarnings(ctx, r, project, all, nil); err != nil {
			return MemberListOut{}, err
		}
	}
	return out, nil
}

// readView is the stints a list shows: every open stint, two open ones of
// the same (project, actor) folded into one (union of roles, earliest
// start, 2.6), plus the ended ones with history.
func readView(all []membership, history bool) []membership {
	type key struct{ p, a string }
	var out []membership
	at := map[key]int{}
	for _, m := range all {
		if !m.open() {
			if history {
				out = append(out, m)
			}
			continue
		}
		k := key{m.Project, m.Actor}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			out = append(out, m)
			continue
		}
		cur := &out[i]
		cur.Roles = unionRoles(cur.Roles, m.Roles)
		if m.StartedOn.Before(cur.StartedOn) {
			cur.ID, cur.StartedOn = m.ID, m.StartedOn
		}
	}
	return out
}

// actorIndex is the roster's actors by id.
func actorIndex(ctx context.Context, r profilestore.Roster) (map[string]row.Row, error) {
	rows, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return nil, err
	}
	out := make(map[string]row.Row, len(rows))
	for _, a := range rows {
		out[a.ID.String()] = a
	}
	return out, nil
}

// teamWarnings are the TP-13 lines for project: one per member of an org
// @team who has no open membership of it, sorted ("@frontend: @carol is
// in the org team but not a member of acme-hub (…)"). A team is "of
// the project" when its project_id is the project, or the roster store's
// own project (an org roster's teams are org-wide groups, usable in every
// project of the org; schema TableTeams). Inactive teams and people who
// cannot be picked (left, archived; TP-15) are skipped: they could not be
// added anyway. only, when not nil, limits the lines to those actor ids
// (member remove warns about the removed person only). all nil reads the
// memberships.
func teamWarnings(ctx context.Context, r profilestore.Roster, project Project, all []membership, only map[string]bool) ([]string, error) {
	teams, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	if len(teams) == 0 {
		return nil, nil
	}
	scope := map[string]bool{project.ID.String(): true}
	projects, err := r.List(ctx, schema.TableProjects)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if !p.Deleted {
			scope[p.ID.String()] = true
		}
	}
	if all == nil {
		if all, err = readMemberships(ctx, r); err != nil {
			return nil, err
		}
	}
	member := map[string]bool{}
	for _, m := range all {
		if m.open() && m.Project == project.ID.String() {
			member[m.Actor] = true
		}
	}
	actors, err := actorIndex(ctx, r)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range teams {
		if t.Deleted || fieldText(t, fieldTeamActive) == boolFalse || !scope[fieldText(t, fieldTeamProject)] {
			continue
		}
		for _, aid := range value.ItemStrings(t.Fields[schema.FieldTeamMembers]) {
			if member[aid] || (only != nil && !only[aid]) {
				continue
			}
			a, ok := actors[aid]
			if !ok || !teamprofile.Pickable(teamprofile.StatusOf(a.Fields)) {
				continue
			}
			who := actorShown(a)
			out = append(out, WarnTeamNotMember+" "+handleMark+fieldText(t, fieldTeamHandle)+": "+who+" is in the org team but not a member of "+project.Shown()+
				" (lore project member add "+project.Shown()+" "+who+" --role "+teamprofile.RoleDev+")")
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// overlapErr is E_PERIOD_OVERLAP for a new open stint from on that
// shares a day with the ended stint m, naming m's dates and the first
// --on that works. Three shapes, one message each: on is m's last day,
// on falls inside m, or m starts after on (a stint planned for later).
func overlapErr(mc memberCtx, m membership, on time.Time) error {
	start, end := m.StartedOn.Format(value.DateLayout), m.EndedOn.Format(value.DateLayout)
	from, next := on.Format(value.DateLayout), m.EndedOn.AddDate(0, 0, 1).Format(value.DateLayout)
	stint := start + " → " + end
	switch {
	case on.Equal(m.EndedOn):
		return errs.New(errs.PeriodOverlap, "a membership from "+from+" starts on the last day of the stint "+stint).
			WithHint("start it on " + next + " (the old stint's last day is " + end + "): --on " + next)
	case on.Before(m.StartedOn):
		return errs.New(errs.PeriodOverlap, "a membership from "+from+" would run through the later stint "+stint).
			WithHint("the stint " + stint + " comes later: pick --on " + next + " or later, or --on " + start + " to reopen it")
	default:
		return errs.New(errs.PeriodOverlap, "a membership from "+from+" overlaps the stint "+stint).
			WithHint("lore project member list " + mc.project.Shown() + " --history shows the stint " + stint + "; pick --on " + next + " or later")
	}
}
