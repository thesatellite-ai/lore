package team

// staff.go — the org's own people, added straight to the roster (owner
// report 2026-11-12: "those are part of the company already, so why
// invite"). An invite is a pending row plus a line the person must paste;
// a member is on the roster at once, and joins from their own machine by
// their email (`lore setup join <the org's store url>`, rosterapply.go
// ApplyJoinMember).
//
//	member.add <email> [--name --handle --github --role --type --team --project
//	                    --project-role] | --from <file.csv|json> | --from-invites
//	   ──▶ admin setup ──▶ roster (write, staged: every row of the run in
//	       ONE store commit, one push)
//	   ──▶ per person: actor (id derived from human:<email>, as org.import
//	       derives it, so their join finds the same actor) ──▶ email and
//	       GitHub identity contacts ──▶ open period of --type with the org
//	       role ──▶ project memberships with --project-role
//	   ──▶ each team once with every new member ──▶ a pending invite of the
//	       same email is marked accepted (its teams and projects kept)
//	member.list / member.show <who>   ──▶ people on the roster: email,
//	       handle, org role, teams, status
//	member.edit <who> [--name --handle --role]
//	member.remove <who> [--reason]    ──▶ actor offboard (period ended,
//	       memberships ended, status left)
//
// Writes go through profilestore.Roster like every profile op, so an
// agent runs them as its person (created_by is the person, the agent's
// session on the audit trail, as every roster write).

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Member operation names (`lore member add|list|show|edit|remove`).
const (
	OpStaffAdd    = profilestore.OpStaffAdd
	OpStaffList   = profilestore.OpStaffList
	OpStaffShow   = profilestore.OpStaffShow
	OpStaffEdit   = profilestore.OpStaffEdit
	OpStaffRemove = profilestore.OpStaffRemove
)

// StaffAddIn is the input of member.add.
type StaffAddIn struct {
	Email        string   `json:"email,omitempty" arg:"0" desc:"the person's work email (leave out with --from or --from-invites)"`
	Name         string   `json:"name,omitempty" flag:"name" desc:"display name (default: the email's name part)"`
	Handle       string   `json:"handle,omitempty" flag:"handle" desc:"@handle on the roster"`
	GitHub       string   `json:"github,omitempty" flag:"github" desc:"their GitHub login"`
	Role         string   `json:"role,omitempty" flag:"role" desc:"org-level role, no project needed (member, pm, admin, …): the stint's org role"`
	Type         string   `json:"type,omitempty" flag:"type" desc:"employee, contractor, intern or client_contact (default employee)"`
	Teams        []string `json:"teams,omitempty" flag:"team" desc:"a team to put them on: @handle (repeat)"`
	Projects     []string `json:"projects,omitempty" flag:"project" desc:"a project to add them to (repeat); needs --project-role"`
	ProjectRoles []string `json:"project_roles,omitempty" flag:"project-role" desc:"project role(s) on each --project (repeat, or comma-separate)"`
	From         string   `json:"from,omitempty" flag:"from" desc:"a .csv or .json file of people (email,name,handle,github,role,type,teams,projects,project_roles); all of them in one store commit"`
	FromInvites  bool     `json:"from_invites,omitempty" flag:"from-invites" desc:"every pending invite becomes a member (its teams and projects kept)"`
	DryRun       bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"show who would be added; write nothing"`
}

// StaffRow is one person as member ops show them.
type StaffRow struct {
	ID       string   `json:"id"`
	Email    string   `json:"email,omitempty"`
	Name     string   `json:"display_name"`
	Handle   string   `json:"handle,omitempty"`
	GitHub   string   `json:"github,omitempty"`
	Role     string   `json:"role,omitempty"`
	Type     string   `json:"type,omitempty"`
	Status   string   `json:"status,omitempty"`
	Teams    []string `json:"teams,omitempty"`
	Projects []string `json:"projects,omitempty"`
	// Invite is the pending invite this add turned into a member.
	Invite string `json:"invite,omitempty"`
}

// StaffAddOut is member.add's answer.
type StaffAddOut struct {
	Added   []StaffRow `json:"added"`
	Already []StaffRow `json:"already,omitempty"`
	Where   string     `json:"where"`
	DryRun  bool       `json:"dry_run,omitempty"`
	// Rows is how many roster rows the add wrote (one store commit).
	Rows     int      `json:"rows"`
	Warnings []string `json:"warnings,omitempty"`
	// Notes say what member add chose for someone (a handle lowercased or
	// derived), one line each.
	Notes []string `json:"notes,omitempty"`
	// JoinWith is the command each person runs on their own machine:
	// `lore setup join <the org's store url>`. The store url, never the org
	// name: a new member's machine has no org entry yet, so only the url
	// resolves there (B2-1). Credentials are stripped; empty when the org
	// entry names no store.
	JoinWith string `json:"join_with,omitempty"`
}

// joinPlaceholder stands in for the store url when the org entry has none.
const joinPlaceholder = "lore setup join <the org's store url>"

// joinCommand is the line a member runs on a fresh machine to join org:
// its store url (credentials stripped), else the placeholder.
func joinCommand(ctx context.Context, env Env, org string) string {
	origin, err := env.TeamOrigin(ctx, org)
	if err != nil || strings.TrimSpace(origin.Store) == "" {
		return joinPlaceholder
	}
	clean, _, err := repourl.StripCredentials(origin.Store)
	if err != nil || clean == "" {
		return joinPlaceholder
	}
	return "lore setup join " + clean
}

// Text is one line per person, then how to join.
func (o StaffAddOut) Text() string {
	var b strings.Builder
	verb := work.Success + " added "
	if o.DryRun {
		verb = "would add "
	}
	for _, p := range o.Added {
		line := verb + p.Email + " (" + p.ID + ") " + p.Name
		if p.Role != "" {
			line += ", role " + p.Role
		}
		if len(p.Teams) > 0 {
			line += ", teams @" + strings.Join(p.Teams, ", @")
		}
		if len(p.Projects) > 0 {
			line += ", projects " + strings.Join(p.Projects, ", ")
		}
		if p.Invite != "" {
			line += " (was invite " + p.Invite + ", now accepted)"
		}
		b.WriteString(line + "\n")
	}
	for _, n := range o.Notes {
		b.WriteString("  note: " + n + "\n")
	}
	for _, p := range o.Already {
		b.WriteString("  already a member: " + p.Email + " (" + p.ID + ")\n")
	}
	switch {
	case len(o.Added) == 0:
		b.WriteString("nothing to add on " + o.Where + "'s roster\n")
	case o.DryRun:
		b.WriteString("nothing written (--dry-run)\n")
	default:
		join := cmp.Or(o.JoinWith, joinPlaceholder)
		b.WriteString(fmt.Sprintf("  %d roster rows in %s, one store commit; each person joins on their machine with: %s (matched by their email, no invite)\n", o.Rows, o.Where, join))
	}
	return b.String() + profilestore.WarningText(o.Warnings)
}

// StaffListIn is the input of member.list.
type StaffListIn struct {
	All bool `json:"all,omitempty" flag:"all" desc:"people who left or are archived too"`
}

// StaffListOut is member.list's answer.
type StaffListOut struct {
	Members []StaffRow `json:"members"`
	Where   string     `json:"where"`
}

// Text is a table of the members.
func (o StaffListOut) Text() string {
	if len(o.Members) == 0 {
		return "no members on " + o.Where + "'s roster (lore member add <email>)\n"
	}
	var b strings.Builder
	for _, m := range o.Members {
		line := fmt.Sprintf("%s  %-28s %-20s %-12s", m.ID, m.Email, m.Name, orDash(m.Role))
		if m.Handle != "" {
			line += " @" + m.Handle
		}
		if len(m.Teams) > 0 {
			line += "  teams @" + strings.Join(m.Teams, ", @")
		}
		if m.Status != "" && m.Status != schema.ActorStatusActive {
			line += "  [" + m.Status + "]"
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// StaffRefIn names one member.
type StaffRefIn struct {
	Who string `json:"who" arg:"0" desc:"the person: email, @handle or act_ id"`
}

// StaffShowOut is member.show's answer.
type StaffShowOut struct {
	Member StaffRow `json:"member"`
	Where  string   `json:"where"`
}

// Text is the person, one fact per line.
func (o StaffShowOut) Text() string {
	m := o.Member
	var b strings.Builder
	b.WriteString(m.ID + "  " + m.Name + "\n")
	for _, kv := range [][2]string{{"email", m.Email}, {"handle", atOrEmpty(m.Handle)}, {"github", m.GitHub}, {"role", m.Role}, {"type", m.Type}, {"status", m.Status},
		{"teams", joinAt(m.Teams)}, {"projects", strings.Join(m.Projects, ", ")}} {
		if kv[1] != "" {
			b.WriteString(fmt.Sprintf("  %-9s %s\n", kv[0]+":", kv[1]))
		}
	}
	b.WriteString("  roster:   " + o.Where + "\n")
	return b.String()
}

// StaffEditIn is the input of member.edit.
type StaffEditIn struct {
	Who    string `json:"who" arg:"0" desc:"the person: email, @handle or act_ id"`
	Name   string `json:"name,omitempty" flag:"name" desc:"new display name"`
	Handle string `json:"handle,omitempty" flag:"handle" desc:"new @handle"`
	Role   string `json:"role,omitempty" flag:"role" desc:"new org-level role (on their open stint)"`
}

// StaffRemoveIn is the input of member.remove.
type StaffRemoveIn struct {
	Who    string `json:"who" arg:"0" desc:"the person: email, @handle or act_ id"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why they left"`
}

// StaffWriteOut is member.edit's and member.remove's answer.
type StaffWriteOut struct {
	Member StaffRow `json:"member"`
	Verb   string   `json:"verb"`
	Where  string   `json:"where"`
	Note   string   `json:"note,omitempty"`
}

// Text is one success line (and the note, when there is one).
func (o StaffWriteOut) Text() string {
	t := work.Success + " " + o.Member.Email + " (" + o.Member.ID + ") " + o.Verb + " on " + o.Where + "'s roster\n"
	if o.Note != "" {
		t += "  note: " + o.Note + "\n"
	}
	return t
}

// Remover is the optional Env upgrade member.remove runs (ops/period's
// actor offboard: the stint and memberships end, the status is left).
type Remover interface {
	OffboardPerson(ctx context.Context, who, reason string) error
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func atOrEmpty(h string) string {
	if h == "" {
		return ""
	}
	return "@" + h
}

func joinAt(hs []string) string {
	if len(hs) == 0 {
		return ""
	}
	return "@" + strings.Join(hs, ", @")
}

// staffOps are the member operations (registered in Ops).
func (o ops) staffOps(base, writeErrs []errs.Code) []op.Entry {
	asking := func(s op.Spec) op.Spec {
		s.Confirm.MayAsk = true
		return s
	}
	return []op.Entry{
		op.Define(asking(op.Spec{Name: OpStaffAdd, Summary: "Add your own staff straight to the org roster (no invite): role, teams and projects apply at once; they join with lore setup join <the org's store url>",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.ContactTaken, errs.StatusChange}, writeErrs...),
			Examples: []op.Example{
				{Title: "add a developer to a team", CLI: "lore member add mira@agency.example --name 'Mira Dev' --github mira-dev --role member --team @web --yes", Want: "added"},
				{Title: "add many from a file, one commit", CLI: "lore member add --from staff.csv --yes", Want: "added"},
			}}),
			func(ctx context.Context, in StaffAddIn) (StaffAddOut, error) { return o.staffAdd(ctx, in) }),
		op.Define(op.Spec{Name: OpStaffList, Summary: "List the org's people: email, name, org role, handle, teams",
			Permission: op.PermRead, Access: op.AccessRead, Errors: base,
			Examples: []op.Example{{Title: "everyone", CLI: "lore member list --json", Want: `"members"`}}},
			func(ctx context.Context, in StaffListIn) (StaffListOut, error) { return o.staffList(ctx, in) }),
		op.Define(op.Spec{Name: OpStaffShow, Summary: "Show one person on the org roster",
			Permission: op.PermRead, Access: op.AccessRead, Errors: base,
			Examples: []op.Example{{Title: "by email", CLI: "lore member show mira@agency.example", Want: "email"}}},
			func(ctx context.Context, in StaffRefIn) (StaffShowOut, error) { return o.staffShow(ctx, in) }),
		op.Define(op.Spec{Name: OpStaffEdit, Summary: "Change a person's name, @handle or org-level role",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "a new org role", CLI: "lore member edit mira@agency.example --role pm", Want: "updated"}}},
			func(ctx context.Context, in StaffEditIn) (StaffWriteOut, error) { return o.staffEdit(ctx, in) }),
		op.Define(asking(op.Spec{Name: OpStaffRemove, Summary: "Take a person off the org (actor offboard: their stint and memberships end; the row stays for history)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.StatusChange}, writeErrs...),
			Examples: []op.Example{{Title: "someone leaves", CLI: "lore member remove mira@agency.example --reason 'moved on' --yes", Want: "removed"}}}),
			func(ctx context.Context, in StaffRemoveIn) (StaffWriteOut, error) { return o.staffRemove(ctx, in) }),
	}
}

// staffSpec is one person to add, as read from flags, a file or an invite.
type staffSpec struct {
	Email        string
	Name         string
	Handle       string
	GitHub       string
	Role         string
	Type         string
	Teams        []string
	Projects     []string
	ProjectRoles []string
	invite       *inviteRow
}

// staffAdd is member.add.
func (o ops) staffAdd(ctx context.Context, in StaffAddIn) (StaffAddOut, error) {
	e, err := adminSetup(ctx, o.env)
	if err != nil {
		return StaffAddOut{}, errs.Prefix("member add", err)
	}
	given := 0
	for _, on := range []bool{strings.TrimSpace(in.Email) != "", in.From != "", in.FromInvites} {
		if on {
			given++
		}
	}
	if given != 1 {
		return StaffAddOut{}, errs.New(errs.InvalidInput, "member add: give one of <email>, --from <file> or --from-invites").
			WithHint("lore member add mira@agency.example --team @web")
	}
	r, done, err := o.env.Roster(ctx, !in.DryRun)
	defer done()
	if err != nil {
		return StaffAddOut{}, err
	}
	invites, err := readInvites(ctx, r)
	if err != nil {
		return StaffAddOut{}, err
	}
	pending := map[string]inviteRow{}
	for _, x := range invites {
		if onboard.EffectiveInviteStatus(x.record(), r.Today()) == schema.InviteStatusPending {
			pending[x.Email] = x
		}
	}
	var specs []staffSpec
	switch {
	case in.From != "":
		if specs, err = readStaffFile(in.From); err != nil {
			return StaffAddOut{}, err
		}
	case in.FromInvites:
		for _, k := range slices.Sorted(maps.Keys(pending)) {
			x := pending[k]
			specs = append(specs, staffSpec{Email: x.Email, GitHub: x.GitHub, Type: x.PeriodType, Teams: x.Teams, Projects: x.Projects, ProjectRoles: x.Roles})
		}
	default:
		specs = []staffSpec{{Email: in.Email, Name: in.Name, Handle: in.Handle, GitHub: in.GitHub, Role: in.Role, Type: in.Type,
			Teams: splitList(in.Teams), Projects: splitList(in.Projects), ProjectRoles: splitList(in.ProjectRoles)}}
	}
	out := StaffAddOut{Added: []StaffRow{}, Where: r.Where(), DryRun: in.DryRun, JoinWith: joinCommand(ctx, o.env, e.Org)}
	plans, err := o.planStaff(ctx, r, specs, pending, &out)
	if err != nil {
		return StaffAddOut{}, err
	}
	if in.DryRun || len(plans) == 0 {
		return out, nil
	}
	if !op.Confirmed(ctx) && !engine.Confirmed(ctx) {
		q := fmt.Sprintf("add %d people to %s's roster as members (%s)?", len(plans), r.Where(), staffEmails(plans))
		return StaffAddOut{}, errs.New(errs.NeedsConfirm, "member add: "+q).WithQuestion(q).WithHint("ask the person, then rerun with --yes")
	}
	// every row of the run in ONE store commit (engine stage), one push
	sctx := engine.Staging(store.WithCommitTitle(ctx, "member add: "+staffEmails(plans)))
	rows := 0
	teamAdds := map[string][]string{}
	for _, p := range plans {
		n, err := o.writeStaff(sctx, r, p)
		if err != nil {
			engine.Discard(sctx)
			return StaffAddOut{}, errs.Prefix("member add "+p.spec.Email+" (nothing written)", err)
		}
		rows += n
		for _, h := range p.spec.Teams {
			teamAdds[h] = append(teamAdds[h], p.id.String())
		}
	}
	n, err := addToTeams(sctx, r, teamAdds)
	if err != nil {
		engine.Discard(sctx)
		return StaffAddOut{}, errs.Prefix("member add (nothing written)", err)
	}
	rows += n
	if _, err := engine.Commit(sctx); err != nil {
		return StaffAddOut{}, errs.Prefix("member add: the roster write (nothing written)", err)
	}
	r.Push(ctx)
	out.Rows = rows
	out.Warnings = r.Warnings()
	return out, nil
}

// staffPlan is one person member add writes.
type staffPlan struct {
	spec     staffSpec
	id       ids.ID[ids.Actor]
	existing *row.Row
	projects []string // resolved prj_ ids
}

// staffEmails lists the plans' emails (short).
func staffEmails(plans []staffPlan) string {
	var es []string
	for _, p := range plans {
		es = append(es, p.spec.Email)
	}
	if len(es) > 5 {
		return strings.Join(es[:5], ", ") + fmt.Sprintf(" and %d more", len(es)-5)
	}
	return strings.Join(es, ", ")
}

// planStaff checks every spec before anything is written (one bad line
// refuses the whole file, naming the line and the fix) and fills out.
func (o ops) planStaff(ctx context.Context, r profilestore.Roster, specs []staffSpec, pending map[string]inviteRow, out *StaffAddOut) ([]staffPlan, error) {
	var plans []staffPlan
	seen := map[string]bool{}
	taken, err := takenHandles(ctx, r)
	if err != nil {
		return nil, err
	}
	for i, s := range specs {
		where := "member add"
		if len(specs) > 1 {
			where = fmt.Sprintf("member add: person %d (%s)", i+1, s.Email)
		}
		email, err := teamprofile.Normalize(schema.ContactKindEmail, s.Email)
		if err != nil || email == "" {
			return nil, errs.New(errs.InvalidInput, where+": "+work.Quote(s.Email)+" is not an email address").WithHint("lore member add mira@agency.example")
		}
		if seen[email] {
			return nil, errs.New(errs.InvalidInput, where+": "+email+" is listed twice").WithHint("keep one line per person")
		}
		seen[email] = true
		s.Email = email
		if s.Type == "" {
			s.Type = schema.PeriodTypeEmployee
		}
		if !slices.Contains(schema.PeriodTypes(), s.Type) {
			return nil, errs.New(errs.InvalidInput, where+": type "+work.Quote(s.Type)+" is not one of "+strings.Join(schema.PeriodTypes(), ", "))
		}
		if s.Name == "" {
			s.Name, _, _ = strings.Cut(email, "@")
		}
		if s.GitHub = strings.TrimPrefix(strings.TrimSpace(s.GitHub), handleMark); s.GitHub != "" {
			// the login is kept as given (its contact's norm is lowercase)
			if gh, gerr := teamprofile.Normalize(schema.ContactKindHandle, s.GitHub); gerr != nil || gh == "" {
				return nil, errs.New(errs.InvalidInput, where+": --github "+work.Quote(s.GitHub)+" is not a GitHub user name")
			}
		}
		var notes []string
		derived := ""
		if strings.TrimSpace(s.Handle) == "" {
			// no handle given: from the GitHub login (kept as given on its
			// contact), else the email's name part, lowercased
			derived = s.GitHub
			if derived == "" {
				derived, _, _ = strings.Cut(email, "@")
			}
		}
		h, note, herr := personHandle(cmp.Or(strings.TrimSpace(s.Handle), derived))
		if herr != nil {
			return nil, errs.Prefix(where, herr)
		}
		if derived != "" && h != "" && taken[h] {
			notes = append(notes, email+": no handle (@"+h+" is taken; member edit "+email+" --handle <h> sets one)")
			h = ""
		}
		if h != "" {
			taken[h] = true
		}
		switch {
		case derived != "" && h != "":
			notes = append(notes, email+": handle @"+h+" (from "+map[bool]string{true: "the GitHub login", false: "the email"}[s.GitHub != ""]+", lowercase)")
		case note != "":
			notes = append(notes, email+": "+note)
		}
		s.Handle = h

		if len(s.Projects) > 0 && len(s.ProjectRoles) == 0 {
			return nil, errs.New(errs.InvalidInput, where+": --project needs at least one --project-role (a project membership has roles); the org-level role is --role").
				WithHint("--project-role dev")
		}
		if err := checkRoles(ctx, r, s.ProjectRoles); err != nil {
			return nil, errs.Prefix(where, err)
		}
		teams, err := inviteTeams(ctx, r, s.Teams)
		if err != nil {
			return nil, errs.Prefix(where, err)
		}
		s.Teams = teams
		p := staffPlan{spec: s}
		for _, ref := range s.Projects {
			pid, perr := resolveProject(ctx, o.env, r, ref)
			if perr != nil {
				return nil, errs.Prefix(where, perr)
			}
			if !slices.Contains(p.projects, pid) {
				p.projects = append(p.projects, pid)
			}
		}
		if p.id, err = identity.ActorID(humanKeyPrefix + email); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, where+": no actor id for "+email, err)
		}
		cur, found, err := staffActor(ctx, r, email, p.id)
		if err != nil {
			return nil, err
		}
		if found {
			p.id = mustActorID(cur.ID)
			if isMember(ctx, r, cur) {
				out.Already = append(out.Already, StaffRow{ID: cur.ID.String(), Email: email, Name: fieldText(cur, actorFieldName)})
				continue
			}
			p.existing = &cur
			// someone who left comes back (B2-14: the same day too, the
			// stint that ended today reopens): keep their roster name
			if strings.TrimSpace(specs[i].Name) == "" {
				s.Name = cmp.Or(fieldText(cur, actorFieldName), s.Name)
				p.spec.Name = s.Name
			}
			notes = append(notes, email+": was "+teamprofile.StatusOf(cur.Fields)+"; back on the roster")
		}
		if x, ok := pending[email]; ok {
			p.spec.invite = &x
		}
		row := StaffRow{ID: p.id.String(), Email: email, Name: s.Name, Handle: s.Handle, GitHub: s.GitHub, Role: s.Role, Type: s.Type, Teams: s.Teams, Projects: p.projects}
		if p.spec.invite != nil {
			row.Invite = p.spec.invite.ID.String()
		}
		out.Added = append(out.Added, row)
		out.Notes = append(out.Notes, notes...)
		plans = append(plans, p)
	}
	return plans, nil
}

// mustActorID is id as an actor id (ids read from actor rows always are).
func mustActorID(id ids.AnyID) ids.ID[ids.Actor] {
	a, _ := ids.Parse[ids.Actor](id.String())
	return a
}

// staffActor finds the roster actor of email: by its email identity
// contact, else by the actor id the key derives.
func staffActor(ctx context.Context, r profilestore.Roster, email string, id ids.ID[ids.Actor]) (row.Row, bool, error) {
	if a, err := r.ResolveActor(ctx, "email:"+email); err == nil {
		return a, true, nil
	}
	a, err := r.Get(ctx, schema.TableActors, id.Any())
	if errs.Has(err, errs.NotFound) {
		return row.Row{}, false, nil
	}
	if err != nil {
		return row.Row{}, false, err
	}
	return a, true, nil
}

// isMember reports whether a is already a member: an active person with
// an email identity contact and a stored stint (what member add and an
// accepted invite write; a bare `actor register` writes neither).
func isMember(ctx context.Context, r profilestore.Roster, a row.Row) bool {
	if !teamprofile.Pickable(teamprofile.StatusOf(a.Fields)) {
		return false
	}
	rows, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return false
	}
	for _, p := range rows {
		if fieldText(p, schema.FieldPeriodActor) == a.ID.String() && fieldText(p, schema.FieldPeriodEndedOn) == "" {
			return true
		}
	}
	return false
}

// writeStaff stages one person's rows and returns how many.
func (o ops) writeStaff(ctx context.Context, r profilestore.Roster, p staffPlan) (int, error) {
	s := p.spec
	var actor row.Row
	if p.existing != nil {
		actor = *p.existing
	} else {
		f := map[string]value.Value{}
		var err error
		for k, v := range map[string]string{staffFieldKind: schema.ActorKindHuman, actorFieldName: s.Name, fieldStableKey: humanKeyPrefix + s.Email, actorFieldHandle: s.Handle} {
			if v == "" {
				continue
			}
			if f[k], err = textValue(k, v); err != nil {
				return 0, err
			}
		}
		f[actorFieldActive] = value.BoolOf(true)
		if f[actorFieldStatus], err = textValue("status", schema.ActorStatusActive); err != nil {
			return 0, err
		}
		if actor, err = r.CreateWithID(ctx, schema.TableActors, p.id.Any(), f); err != nil {
			return 0, err
		}
	}
	w := &writer{r: r, actor: actor, today: r.Today(), orgRole: s.Role}
	if p.existing != nil {
		if _, err := w.reactivate(ctx); err != nil {
			return 0, err
		}
	} else {
		w.note(actor.ID)
	}
	if err := w.identity(ctx, []string{s.Email}, s.Email, s.GitHub); err != nil {
		return 0, err
	}
	if err := w.openPeriod(ctx, s.Type); err != nil {
		return 0, err
	}
	for _, pid := range p.projects {
		if err := w.membership(ctx, pid, s.ProjectRoles); err != nil {
			return 0, err
		}
	}
	if s.invite != nil {
		if err := w.accept(ctx, s.invite.ID); err != nil {
			return 0, err
		}
	}
	return len(w.wrote), nil
}

// addToTeams adds each team's new members, one write per team.
func addToTeams(ctx context.Context, r profilestore.Roster, adds map[string][]string) (int, error) {
	n := 0
	for _, h := range slices.Sorted(maps.Keys(adds)) {
		cur, ok, err := findTeam(ctx, r, h)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, teamMissErr(h, r.Where())
		}
		list := value.ItemStrings(cur.Fields[schema.FieldTeamMembers])
		changed := false
		for _, id := range adds[h] {
			if !slices.Contains(list, id) {
				list, changed = append(list, id), true
			}
		}
		if !changed {
			continue
		}
		ml, err := membersValue(list)
		if err != nil {
			return n, err
		}
		if _, err := r.Update(ctx, schema.TableTeams, cur.ID, map[string]value.Value{schema.FieldTeamMembers: ml}); err != nil {
			return n, errs.Prefix("team @"+h, err)
		}
		n++
	}
	return n, nil
}

// readStaffFile reads --from: a .json list of objects or a .csv with a
// header (email,name,handle,github,role,type,teams,projects,project_roles;
// list cells comma- or space-separated inside quotes or with ';').
func readStaffFile(path string) ([]staffSpec, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "member add --from: cannot read "+path, err).WithHint("give a .csv or .json file")
	}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var rows []struct {
			Email        string   `json:"email"`
			Name         string   `json:"name"`
			Handle       string   `json:"handle"`
			GitHub       string   `json:"github"`
			Role         string   `json:"role"`
			Type         string   `json:"type"`
			Teams        []string `json:"teams"`
			Projects     []string `json:"projects"`
			ProjectRoles []string `json:"project_roles"`
		}
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "member add --from "+path+": not a JSON list of people", err).
				WithHint(`[{"email":"mira@agency.example","name":"Mira","teams":["@web"]}]`)
		}
		out := make([]staffSpec, 0, len(rows))
		for _, x := range rows {
			out = append(out, staffSpec{Email: x.Email, Name: x.Name, Handle: x.Handle, GitHub: x.GitHub, Role: x.Role, Type: x.Type,
				Teams: splitList(x.Teams), Projects: splitList(x.Projects), ProjectRoles: splitList(x.ProjectRoles)})
		}
		return out, nil
	}
	cr := csv.NewReader(strings.NewReader(string(b)))
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "member add --from "+path+": no CSV header", err).
			WithHint("first line: email,name,handle,github,role,type,teams,projects,project_roles")
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["email"]; !ok {
		return nil, errs.New(errs.InvalidInput, "member add --from "+path+": the header has no email column").
			WithHint("first line: email,name,handle,github,role,type,teams,projects,project_roles")
	}
	cell := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	list := func(s string) []string {
		return splitList(strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ' ' || r == ',' }))
	}
	var out []staffSpec
	for line := 2; ; line++ {
		rec, rerr := cr.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, errs.Wrap(errs.InvalidInput, fmt.Sprintf("member add --from %s: line %d", path, line), rerr)
		}
		if cell(rec, "email") == "" {
			continue
		}
		out = append(out, staffSpec{Email: cell(rec, "email"), Name: cell(rec, "name"), Handle: cell(rec, "handle"), GitHub: cell(rec, "github"),
			Role: cell(rec, "role"), Type: cell(rec, "type"), Teams: list(cell(rec, "teams")), Projects: list(cell(rec, "projects")), ProjectRoles: list(cell(rec, "project_roles"))})
	}
	if len(out) == 0 {
		return nil, errs.New(errs.InvalidInput, "member add --from "+path+": no people in it").WithHint("one line per person after the header")
	}
	return out, nil
}

// staffRows is every person on the roster as a StaffRow.
func staffRows(ctx context.Context, r profilestore.Roster, all bool) ([]StaffRow, error) {
	actors, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return nil, err
	}
	contacts, err := r.List(ctx, schema.TableActorContacts)
	if err != nil {
		return nil, err
	}
	periods, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return nil, err
	}
	teams, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	members, err := r.List(ctx, schema.TableProjectMembers)
	if err != nil {
		return nil, err
	}
	var out []StaffRow
	for _, a := range actors {
		if fieldText(a, staffFieldKind) != schema.ActorKindHuman || fieldText(a, schema.FieldActorMergedInto) != "" {
			continue
		}
		id := a.ID.String()
		st := teamprofile.StatusOf(a.Fields)
		if !all && !teamprofile.Pickable(st) {
			continue
		}
		m := StaffRow{ID: id, Name: fieldText(a, actorFieldName), Handle: fieldText(a, actorFieldHandle), Status: st}
		for _, c := range contacts {
			if fieldText(c, schema.FieldContactActor) != id {
				continue
			}
			switch fieldText(c, schema.FieldContactType) {
			case teamprofile.ContactTypeEmail:
				if m.Email == "" || fieldText(c, schema.FieldContactPrimary) == boolTrue {
					m.Email = fieldText(c, schema.FieldContactValueNorm)
				}
			case teamprofile.ContactTypeGitHub:
				m.GitHub = cmp.Or(fieldText(c, schema.FieldContactValue), fieldText(c, schema.FieldContactValueNorm)) // the login as given
			}
		}
		if m.Email == "" {
			m.Email = strings.TrimPrefix(fieldText(a, fieldStableKey), humanKeyPrefix)
		}
		for _, p := range periods {
			if fieldText(p, schema.FieldPeriodActor) == id && fieldText(p, schema.FieldPeriodEndedOn) == "" {
				m.Role, m.Type = fieldText(p, schema.FieldPeriodOrgRole), fieldText(p, schema.FieldPeriodType)
			}
		}
		for _, t := range teams {
			if fieldText(t, teamFieldActive) != boolFalse && slices.Contains(value.ItemStrings(t.Fields[schema.FieldTeamMembers]), id) {
				m.Teams = append(m.Teams, fieldText(t, teamFieldHandle))
			}
		}
		for _, pm := range members {
			if fieldText(pm, schema.FieldMemberActor) == id && fieldText(pm, schema.FieldMemberEndedOn) == "" {
				m.Projects = append(m.Projects, fieldText(pm, schema.FieldMemberProject))
			}
		}
		slices.Sort(m.Teams)
		out = append(out, m)
	}
	slices.SortFunc(out, func(x, y StaffRow) int { return strings.Compare(x.Email, y.Email) })
	return out, nil
}

func (o ops) staffList(ctx context.Context, in StaffListIn) (StaffListOut, error) {
	r, done, err := o.env.Roster(ctx, false)
	defer done()
	if err != nil {
		return StaffListOut{}, err
	}
	rows, err := staffRows(ctx, r, in.All)
	if err != nil {
		return StaffListOut{}, err
	}
	if rows == nil {
		rows = []StaffRow{}
	}
	return StaffListOut{Members: rows, Where: r.Where()}, nil
}

// staffFind resolves who on r (email, @handle, act_ id) to its StaffRow.
func staffFind(ctx context.Context, r profilestore.Roster, who string) (row.Row, StaffRow, error) {
	a, err := r.ResolveActor(ctx, who)
	if err != nil {
		return row.Row{}, StaffRow{}, err
	}
	rows, err := staffRows(ctx, r, true)
	if err != nil {
		return row.Row{}, StaffRow{}, err
	}
	for _, m := range rows {
		if m.ID == a.ID.String() {
			return a, m, nil
		}
	}
	return row.Row{}, StaffRow{}, errs.New(errs.NotFound, who+" is not a person on "+r.Where()+"'s roster").WithHint("lore member list")
}

func (o ops) staffShow(ctx context.Context, in StaffRefIn) (StaffShowOut, error) {
	r, done, err := o.env.Roster(ctx, false)
	defer done()
	if err != nil {
		return StaffShowOut{}, err
	}
	_, m, err := staffFind(ctx, r, in.Who)
	if err != nil {
		return StaffShowOut{}, err
	}
	return StaffShowOut{Member: m, Where: r.Where()}, nil
}

func (o ops) staffEdit(ctx context.Context, in StaffEditIn) (StaffWriteOut, error) {
	if in.Name == "" && in.Handle == "" && in.Role == "" {
		return StaffWriteOut{}, errs.New(errs.InvalidInput, "member edit: nothing to change").WithHint("give --name, --handle or --role")
	}
	if _, err := adminSetup(ctx, o.env); err != nil {
		return StaffWriteOut{}, errs.Prefix("member edit", err)
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return StaffWriteOut{}, err
	}
	a, _, err := staffFind(ctx, r, in.Who)
	if err != nil {
		return StaffWriteOut{}, err
	}
	patch := map[string]value.Value{}
	if in.Name != "" {
		if patch[actorFieldName], err = textValue("--name", in.Name); err != nil {
			return StaffWriteOut{}, err
		}
	}
	h, note, herr := personHandle(in.Handle)
	if herr != nil {
		return StaffWriteOut{}, errs.Prefix("member edit --handle", herr)
	}
	if h != "" {
		if patch[actorFieldHandle], err = textValue("--handle", h); err != nil {
			return StaffWriteOut{}, err
		}
	}
	// the actor row and the stint's role in ONE store commit (B1-21)
	sctx := engine.Staging(store.WithCommitTitle(ctx, "member edit: "+in.Who))
	if len(patch) > 0 {
		if _, err := r.Update(sctx, schema.TableActors, a.ID, patch); err != nil {
			engine.Discard(sctx)
			return StaffWriteOut{}, err
		}
	}
	if in.Role != "" {
		w := &writer{r: r, actor: a, today: r.Today(), orgRole: in.Role}
		if err := w.setOrgRole(sctx); err != nil {
			engine.Discard(sctx)
			return StaffWriteOut{}, err
		}
	}
	if _, err := engine.Commit(sctx); err != nil {
		return StaffWriteOut{}, errs.Prefix("member edit (nothing written)", err)
	}
	r.Push(ctx)
	_, m, err := staffFind(ctx, r, a.ID.String())
	if err != nil {
		return StaffWriteOut{}, err
	}
	return StaffWriteOut{Member: m, Verb: "updated", Where: r.Where(), Note: note}, nil
}

func (o ops) staffRemove(ctx context.Context, in StaffRemoveIn) (StaffWriteOut, error) {
	rm, ok := o.env.(Remover)
	if !ok {
		return StaffWriteOut{}, errs.New(errs.Unsupported, "member remove: this lore build cannot offboard").WithHint("lore actor offboard " + in.Who)
	}
	r, done, err := o.env.Roster(ctx, false)
	if err != nil {
		done()
		return StaffWriteOut{}, err
	}
	a, m, err := staffFind(ctx, r, in.Who)
	done()
	if err != nil {
		return StaffWriteOut{}, err
	}
	reason := in.Reason
	if reason == "" {
		reason = "member remove"
	}
	if err := rm.OffboardPerson(ctx, a.ID.String(), reason); err != nil {
		return StaffWriteOut{}, err
	}
	m.Status = schema.ActorStatusLeft
	return StaffWriteOut{Member: m, Verb: "removed (left; the row stays for history)", Where: r.Where()}, nil
}

// personHandle is raw as a person's roster handle: without its @,
// lowercased, other characters as '-' (a handle is one lowercase word, as
// a team's; owner report 2026-11-12: `@OnurFromAgency` was refused as
// a "team handle"). note says what changed ("" when raw was already one).
func personHandle(raw string) (h, note string, err error) {
	given := strings.TrimPrefix(strings.TrimSpace(raw), handleMark)
	if given == "" {
		return "", "", nil
	}
	h = strings.Trim(strings.ToLower(strings.Map(handleRune, given)), "-_")
	if h == "" {
		return "", "", errs.New(errs.InvalidInput, "person handle @"+given+" has no letter or digit").WithHint("--handle mira")
	}
	if _, terr := teamHandle(h); terr != nil {
		return "", "", errs.New(errs.InvalidInput, "person handle @"+given+" is not one lowercase word (letters, digits, '_' and '-')").WithHint("--handle " + h)
	}
	if h != given {
		note = "handle @" + given + " is stored as @" + h + " (handles are one lowercase word)"
	}
	return h, note, nil
}

// takenHandles are the handles of active people and of teams on r.
func takenHandles(ctx context.Context, r profilestore.Roster) (map[string]bool, error) {
	out := map[string]bool{}
	actors, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return nil, err
	}
	for _, a := range actors {
		if h := fieldText(a, actorFieldHandle); h != "" && teamprofile.Pickable(teamprofile.StatusOf(a.Fields)) {
			out[h] = true
		}
	}
	teams, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		if h := fieldText(t, teamFieldHandle); h != "" {
			out[h] = true
		}
	}
	return out, nil
}

// staffFieldKind is actors.kind.
const staffFieldKind = "kind"
