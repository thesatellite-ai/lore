package team

// teams.go — the org's teams (LORE_TEAM_SPEC F18, v0.1.17 `lore team
// add|list`, owner report 2026-11-11: `lore team list` and `lore team add`
// answered "unknown command" although the teams table exists, and a task
// can be assigned to @team). A team is an @handle on the org roster that
// names a group of people; `task add --assign @web` and every other
// @mention resolve it beside the people.
//
// Every op reads and writes the org roster store through
// profilestore.Store, like the member and invite ops, so they run direct
// on the roster App: outside any repo too (the setup's org store), with a
// coding agent acting as its person.
//
//	team.add <@h> [--name] [--members a,b]
//	    ──▶ handle free of every active person? (a person's handle is refused)
//	    ──▶ members resolved on the roster (left/archived refused)
//	    ──▶ a team @h exists?  yes ──▶ set name/members, active again
//	                           no  ──▶ new teams row in the org folder's project
//	    ──▶ Push
//	team.list [--all]          ──▶ active teams (--all: removed ones too)
//	team.show <@h>             ──▶ the team and each member
//	team.remove <@h>           ──▶ active false, left_at (the row stays, so a
//	                               task that named @h still reads; team add
//	                               brings it back) ──▶ Push
//	team.rename <@h> --to <@new> ──▶ @new free of every person and team?
//	                               ──▶ the row's handle changes (tasks name
//	                               the team by its tem_ id, so they follow)
//	                               ──▶ pending invites naming @h name @new ──▶ Push
//	team.member.add|remove <@h> <person>... ──▶ the member list changed ──▶ Push

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/logic/textnorm"
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

// Team operation names (CLI `lore team add|list|show|remove`, `lore team
// member add|remove`; REST and docs come from the same specs).
// The names live in profilestore, whose IsDirect runs them on the roster
// App, so the two can never disagree.
const (
	OpTeamAdd          = profilestore.OpTeamAdd
	OpTeamList         = profilestore.OpTeamList
	OpTeamShow         = profilestore.OpTeamShow
	OpTeamRemove       = profilestore.OpTeamRemove
	OpTeamRename       = profilestore.OpTeamRename
	OpTeamMemberAdd    = profilestore.OpTeamMemberAdd
	OpTeamMemberRemove = profilestore.OpTeamMemberRemove
)

// Teams row fields (schema.TableTeams; v0.1.17's columns).
const (
	teamFieldHandle  = "handle"
	teamFieldName    = "display_name"
	teamFieldProject = "project_id"
	teamFieldActive  = "active"
	teamFieldLeftAt  = "left_at"
	actorFieldName   = "display_name"
	actorFieldHandle = "handle"
	actorFieldActive = "active"
	actorFieldStatus = schema.FieldActorStatus
	boolFalse        = "false"
	// handleMark prefixes a handle in input and output.
	handleMark = "@"
	// handleChars are the characters a team handle may hold after its first
	// (a lowercase letter or digit): the roster's handle rule (orgconf).
	handleChars = "abcdefghijklmnopqrstuvwxyz0123456789_-"
)

// Team write verbs (TeamWriteOut.Verb).
const (
	verbTeamAdded         = "added"
	verbTeamUpdated       = "updated"
	verbTeamRemoved       = "removed"
	verbTeamRenamed       = "renamed"
	verbTeamMembersAdded  = "members_added"
	verbTeamMembersRemove = "members_removed"
)

// TeamOps returns the team operations bound to env.
func TeamOps(env profilestore.Store) []op.Entry {
	readErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked}, work.ReadErrors...)
	writeErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked, errs.StatusChange, errs.Unsupported}, work.WriteErrors...)
	return []op.Entry{
		op.Define(op.Spec{Name: OpTeamAdd, Summary: "Create or update a team; @handle then resolves to the team (task add --assign @web)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "a team of two", CLI: `lore team add --handle @web --name "Web" --members @mira,@tomas`, Want: "@web"}}},
			func(ctx context.Context, in TeamAddIn) (TeamWriteOut, error) { return teamAdd(ctx, env, in) }),
		op.Define(op.Spec{Name: OpTeamList, Summary: "List the org's teams (--all adds removed ones)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrs,
			Examples: []op.Example{{Title: "the teams as JSON", CLI: "lore team list --json", Want: `"teams"`}}},
			func(ctx context.Context, in TeamListIn) (TeamListOut, error) { return teamList(ctx, env, in) }),
		op.Define(op.Spec{Name: OpTeamShow, Summary: "Show a team and its members",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrs,
			Examples: []op.Example{{Title: "one team", CLI: "lore team show --handle @web", Want: "members"}}},
			func(ctx context.Context, in TeamRefIn) (TeamShowOut, error) { return teamShow(ctx, env, in) }),
		op.Define(op.Spec{Name: OpTeamRemove, Summary: "Remove a team (its row is kept, so tasks that named it still read; team add brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "the team is disbanded", CLI: "lore team remove --handle @web", Want: "removed"}}},
			func(ctx context.Context, in TeamRefIn) (TeamWriteOut, error) { return teamRemove(ctx, env, in) }),
		op.Define(op.Spec{Name: OpTeamRename, Summary: "Change a team's @handle; tasks assigned to it and pending invites naming it follow",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "the web team becomes the frontend guild", CLI: "lore team rename --handle @web --to @frontend", Want: "@frontend"}}},
			func(ctx context.Context, in TeamRenameIn) (TeamWriteOut, error) { return teamRename(ctx, env, in) }),
		op.Define(op.Spec{Name: OpTeamMemberAdd, Summary: "Add people to a team",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "one more member", CLI: "lore team member add --handle @web --members @devi", Want: "@devi"}}},
			func(ctx context.Context, in TeamMembersIn) (TeamWriteOut, error) {
				return teamMembers(ctx, env, in, true)
			}),
		op.Define(op.Spec{Name: OpTeamMemberRemove, Summary: "Take people off a team",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "someone moves teams", CLI: "lore team member remove --handle @web --members @devi", Want: "@devi"}}},
			func(ctx context.Context, in TeamMembersIn) (TeamWriteOut, error) {
				return teamMembers(ctx, env, in, false)
			}),
	}
}

// TeamAddIn is the input of team.add.
type TeamAddIn struct {
	Handle  string   `json:"handle" arg:"0" desc:"the team's @handle"`
	Name    string   `json:"name,omitempty" flag:"name" desc:"the display name"`
	Members []string `json:"members,omitempty" flag:"members" desc:"the people: @handle, act_ id or email, comma-separated or repeated (replaces the members of an existing team)"`
}

// TeamListIn is the input of team.list.
type TeamListIn struct {
	All bool `json:"all,omitempty" flag:"all" desc:"also list removed teams"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; bound by the direct runner, applied by the
	// roster's engine).
	metalist.Audit
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// TeamRefIn names one team.
type TeamRefIn struct {
	Handle string `json:"handle" arg:"0" desc:"the team's @handle (or its tem_ id)"`
}

// TeamRenameIn is the input of team.rename.
type TeamRenameIn struct {
	Handle string `json:"handle" arg:"0" desc:"the team's @handle now (or its tem_ id)"`
	To     string `json:"to" flag:"to" desc:"the new @handle"`
}

// TeamMembersIn is the input of team.member.add|remove.
type TeamMembersIn struct {
	Handle  string   `json:"handle" arg:"0" desc:"the team's @handle"`
	Members []string `json:"members" arg:"1" rest:"true" desc:"the people: @handle, act_ id or email (several, or comma-separated)"`
}

// TeamMember is one member as shown.
type TeamMember struct {
	ID     string `json:"id"`
	Handle string `json:"handle,omitempty"`
	Name   string `json:"display_name,omitempty"`
	// Status is the person's roster status when they are no longer active
	// (left, archived, deactivated); "" for an active person.
	Status string `json:"status,omitempty"`
}

// shown is @handle, else the name, else the id.
func (m TeamMember) shown() string {
	switch {
	case m.Handle != "":
		return handleMark + m.Handle
	case m.Name != "":
		return m.Name
	}
	return m.ID
}

// PendingMember is an invited person a team waits for.
type PendingMember struct {
	Email  string `json:"email"`
	Invite string `json:"invite"`
	Until  string `json:"expires_on"`
}

// Team is one team as shown.
type Team struct {
	ID      string       `json:"id"`
	Handle  string       `json:"handle"`
	Name    string       `json:"display_name,omitempty"`
	Active  bool         `json:"active"`
	LeftAt  string       `json:"left_at,omitempty"`
	Members []TeamMember `json:"members"`
	// Pending are people invited into the team who have not joined yet:
	// their pending invite names it (team invite --team, or team member
	// add <email>); they become members when they join.
	Pending []PendingMember `json:"pending,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// TeamWriteOut is a team write's answer.
type TeamWriteOut struct {
	Team Team `json:"team"`
	// Verb is added, updated, removed, renamed, members_added or
	// members_removed.
	Verb string `json:"verb"`
	// Changed are the members added or taken off (member add|remove);
	// Unchanged those already in (add) or not in (remove) the team.
	Changed   []string `json:"changed,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
	// Pending are invitees (by email) the team was recorded on: their
	// pending invite now names it and they join it on accept.
	Pending []string `json:"pending,omitempty"`
	// From is the handle before a rename (without its @); "" otherwise.
	From     string   `json:"from,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Where    string   `json:"where"`
}

// Text is one success line (v0.1.17's for add: "✓ <id> @h (n member(s))
// in <where>"), then the warnings.
func (o TeamWriteOut) Text() string {
	t := o.Team
	var line string
	switch o.Verb {
	case verbTeamRemoved:
		line = work.Success + " " + t.ID + " @" + t.Handle + " removed from the roster in " + o.Where +
			" (kept for history: tasks that named it still read; lore team add --handle @" + t.Handle + " brings it back)"
	case verbTeamRenamed:
		line = work.Success + " " + t.ID + " @" + o.From + " is now @" + t.Handle + " in " + o.Where +
			" (" + memberWord(len(t.Members)) + "; tasks assigned to it follow"
		if len(o.Pending) > 0 {
			line += "; pending invites renamed: " + strings.Join(o.Pending, ", ")
		}
		line += ")"
	case verbTeamMembersAdded, verbTeamMembersRemove:
		did := "added to"
		if o.Verb == verbTeamMembersRemove {
			did = "taken off"
		}
		line = work.Success + " @" + t.Handle + ": " + memberWord(len(t.Members)) + " now"
		if len(o.Changed) > 0 {
			line += "; " + strings.Join(o.Changed, ", ") + " " + did + " the team"
		}
		if len(o.Pending) > 0 {
			line += "; " + strings.Join(o.Pending, ", ") + " not on the roster yet: recorded on the pending invite, joins @" + t.Handle + " on accept"
		}
		if len(o.Unchanged) > 0 {
			was := "already a member"
			if o.Verb == verbTeamMembersRemove {
				was = "not a member"
			}
			line += "; " + strings.Join(o.Unchanged, ", ") + " " + was
		}
	default: // added, updated
		line = work.Success + " " + t.ID + " @" + t.Handle + " (" + memberWord(len(t.Members)) + ") " + o.Verb + " in " + o.Where
	}
	return line + "\n" + profilestore.WarningText(o.Warnings)
}

// memberWord is "n member(s)" as v0.1.17 printed it.
func memberWord(n int) string { return fmt.Sprintf("%d member(s)", n) }

// TeamListOut is team.list's answer.
type TeamListOut struct {
	Teams []Team `json:"teams"`
	Where string `json:"where"`
}

// Text is v0.1.17's list: "@<handle> <name> <n> member(s)", a removed
// team marked, or "no teams on <where>'s roster".
func (o TeamListOut) Text() string {
	if len(o.Teams) == 0 {
		return "no teams on " + o.Where + "'s roster\n"
	}
	var b strings.Builder
	for _, t := range o.Teams {
		line := fmt.Sprintf("@%-16s %-24s %s", t.Handle, t.Name, memberWord(len(t.Members)))
		if len(t.Pending) > 0 {
			line += fmt.Sprintf(", %d pending (invited)", len(t.Pending))
		}
		if !t.Active {
			line += removedMark
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// removedMark ends the list line of a removed team (--all).
const removedMark = "  [removed]"

// TeamShowOut is team.show's answer.
type TeamShowOut struct {
	Team  Team   `json:"team"`
	Where string `json:"where"`
}

// Text is the team, then one line per member.
func (o TeamShowOut) Text() string {
	t := o.Team
	var b strings.Builder
	head := t.ID + "  @" + t.Handle
	if t.Name != "" {
		head += "  " + t.Name
	}
	if !t.Active {
		head += removedMark
	}
	b.WriteString(head + "\n")
	b.WriteString("roster: " + o.Where + "\n")
	b.WriteString("members (" + fmt.Sprint(len(t.Members)) + "):\n")
	for _, m := range t.Members {
		line := "  " + m.ID + "  " + m.shown()
		if m.Name != "" && m.Handle != "" {
			line += "  " + m.Name
		}
		if m.Status != "" {
			line += "  [" + m.Status + "]"
		}
		b.WriteString(line + "\n")
	}
	if len(t.Pending) > 0 {
		b.WriteString("pending, joins on accepting the invite (" + fmt.Sprint(len(t.Pending)) + "):\n")
		for _, p := range t.Pending {
			b.WriteString("  " + p.Email + "  " + p.Invite + "  until " + p.Until + "\n")
		}
	}
	return b.String()
}

// teamHandle is the handle in, without its @, checked against the roster's
// handle rule (one lowercase word: letters, digits, '_' and '-').
func teamHandle(in string) (string, error) {
	h := strings.TrimPrefix(strings.TrimSpace(in), handleMark)
	if h == "" {
		return "", errs.New(errs.InvalidInput, "name the team as @handle").WithHint("lore team add --handle @web --members @mira")
	}
	if c := h[0]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') || strings.Trim(h, handleChars) != "" {
		return "", errs.New(errs.InvalidInput, "team handle @"+h+" is not one lowercase word (letters, digits, '_' and '-', starting with a letter or digit)").
			WithHint("lore team add --handle @" + strings.ToLower(strings.Map(handleRune, textnorm.ASCIIFold(h))))
	}
	return h, nil
}

// handleRune maps a character that a handle may not hold to '-'.
func handleRune(r rune) rune {
	if r < 128 && strings.ContainsRune(handleChars, r) || r >= 'A' && r <= 'Z' {
		return r
	}
	return '-'
}

// teamOf reads a teams row; members name their actors when the roster
// holds them.
func teamOf(r row.Row, actors map[string]row.Row) Team {
	t := Team{ID: r.ID.String(), Handle: fieldText(r, teamFieldHandle), Name: fieldText(r, teamFieldName),
		Active: fieldText(r, teamFieldActive) != boolFalse, LeftAt: fieldText(r, teamFieldLeftAt), Members: []TeamMember{}}
	w := auditrec.Of(r)
	t.CreatedBy, t.UpdatedBy = w.CreatedBy, w.UpdatedBy
	for _, id := range value.ItemStrings(r.Fields[schema.FieldTeamMembers]) {
		t.Members = append(t.Members, memberOf(id, actors[id]))
	}
	return t
}

// memberOf is actor id as a member (a only names it; an actor the roster
// does not hold shows its id).
func memberOf(id string, a row.Row) TeamMember {
	m := TeamMember{ID: id}
	if a.ID.IsZero() {
		return m
	}
	m.Handle, m.Name = fieldText(a, actorFieldHandle), fieldText(a, actorFieldName)
	if st := fieldText(a, actorFieldStatus); st != "" && st != schema.ActorStatusActive {
		m.Status = st
	} else if fieldText(a, actorFieldActive) == boolFalse {
		m.Status = statusDeactivated
	}
	return m
}

// statusDeactivated marks a member a v0.1.17 `actor deactivate` left
// inactive with no status.
const statusDeactivated = "deactivated"

// rosterActors is every actors row of r by id.
func rosterActors(ctx context.Context, r profilestore.Roster) (map[string]row.Row, error) {
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

// findTeam is the teams row with handle h, or with id h when h is a tem_
// id (live rows; an active one wins over a removed one), ok false when
// none.
func findTeam(ctx context.Context, r profilestore.Roster, h string) (row.Row, bool, error) {
	rows, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return row.Row{}, false, err
	}
	byID := isTeamID(h)
	var hit row.Row
	found := false
	for _, t := range rows {
		if (byID && t.ID.String() != h) || (!byID && fieldText(t, teamFieldHandle) != h) {
			continue
		}
		if !found || fieldText(t, teamFieldActive) != boolFalse {
			hit, found = t, true
		}
	}
	return hit, found, nil
}

// isTeamID reports whether ref is a tem_ id rather than a handle.
func isTeamID(ref string) bool { return strings.HasPrefix(ref, string(ids.PrefixTeam)+"_") }

// teamMissErr is the answer for a handle (or tem_ id) no team has.
func teamMissErr(h, where string) error {
	if isTeamID(h) {
		return errs.New(errs.NotFound, "no team "+h+" on the roster in "+where).
			WithHint("lore team list shows the teams (lore team list --all also the removed ones)")
	}
	return errs.New(errs.NotFound, "no team @"+h+" on the roster in "+where).
		WithHint("lore team list shows the teams; lore team add --handle @" + h + " --members <people> makes it")
}

// resolveMembers turns references into actor ids, in order, each once. A
// person who left or was archived is refused (TP-15: out of every assign
// list); an agent is taken (an agent of the org may belong to a team).
func resolveMembers(ctx context.Context, r profilestore.Roster, refs []string) ([]string, map[string]row.Row, error) {
	var ids []string
	byID := map[string]row.Row{}
	for _, ref := range splitList(refs) {
		a, err := r.ResolveActor(ctx, ref)
		if err != nil {
			if errs.Has(err, errs.NotFound) {
				return nil, nil, errs.New(errs.NotFound, "no one is "+ref+" on the roster in "+r.Where()).
					WithHint("check the handle with lore actor list; a new person runs lore actor register (or joins from an invite) first")
			}
			return nil, nil, err
		}
		if st := fieldText(a, actorFieldStatus); st == schema.ActorStatusLeft || st == schema.ActorStatusArchived {
			return nil, nil, errs.New(errs.StatusChange, ref+" is "+st+"; someone who is "+st+" cannot join a team").
				WithHint("lore actor rejoin --actor " + ref + " --type employee|contractor|intern|client_contact, then add them")
		}
		id := a.ID.String()
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
		byID[id] = a
	}
	return ids, byID, nil
}

// membersValue is actor ids as the teams member list.
func membersValue(list []string) (value.List, error) {
	vs := make([]value.Value, 0, len(list))
	for _, s := range list {
		id, err := ids.ParseAny(s)
		if err != nil {
			return value.List{}, errs.Wrap(errs.Internal, "member id "+s+" is not valid", err)
		}
		ref, err := refValue(id)
		if err != nil {
			return value.List{}, err
		}
		vs = append(vs, ref)
	}
	l, err := value.ListOf(value.KindRef, vs...)
	if err != nil {
		return value.List{}, errs.Wrap(errs.Internal, "the member list", err)
	}
	return l, nil
}

// handleHolder is the active person holding handle h on r, "" when none: a
// team handle may not be a person's (an @mention would be two things).
func handleHolder(actors map[string]row.Row, h string) string {
	for _, a := range actors {
		if fieldText(a, actorFieldHandle) == h && fieldText(a, actorFieldActive) != boolFalse {
			if n := fieldText(a, actorFieldName); n != "" {
				return n
			}
			return a.ID.String()
		}
	}
	return ""
}

func teamAdd(ctx context.Context, env profilestore.Store, in TeamAddIn) (TeamWriteOut, error) {
	h, err := teamHandle(in.Handle)
	if err != nil {
		return TeamWriteOut{}, err
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamWriteOut{}, err
	}
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if who := handleHolder(actors, h); who != "" {
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "handle @"+h+" is taken by "+who+" on the roster in "+r.Where()+"; a team and a person cannot share one").
			WithHint("choose another team handle (lore team add --handle @" + h + "-team …)")
	}
	members, found, err := resolveMembers(ctx, r, in.Members)
	if err != nil {
		return TeamWriteOut{}, err
	}
	for id, a := range found {
		actors[id] = a
	}
	fields := map[string]value.Value{teamFieldActive: value.BoolOf(true)}
	if name := strings.TrimSpace(in.Name); name != "" {
		t, terr := textValue("--name", name)
		if terr != nil {
			return TeamWriteOut{}, terr
		}
		fields[teamFieldName] = t
	}
	cur, exists, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if exists && len(in.Members) == 0 {
		// no --members on an existing team keeps its members (v0.1.17 cleared
		// them; a rename must not empty a team)
		members = value.ItemStrings(cur.Fields[schema.FieldTeamMembers])
	}
	ml, err := membersValue(members)
	if err != nil {
		return TeamWriteOut{}, err
	}
	fields[schema.FieldTeamMembers] = ml
	verb := verbTeamAdded
	var w row.Row
	if exists {
		verb = verbTeamUpdated
		fields[teamFieldLeftAt] = value.Null{}
		w, err = r.Update(ctx, schema.TableTeams, cur.ID, fields)
	} else {
		fp, ok := r.(profilestore.FolderProjecter)
		if !ok {
			return TeamWriteOut{}, errs.New(errs.Unsupported, "this lore build cannot write teams to the roster in "+r.Where()).WithHint("upgrade lore")
		}
		pid, perr := fp.FolderProject(ctx)
		if perr != nil {
			return TeamWriteOut{}, perr
		}
		ref, rerr := refValue(pid.Any())
		if rerr != nil {
			return TeamWriteOut{}, rerr
		}
		hv, herr := textValue("the handle", h)
		if herr != nil {
			return TeamWriteOut{}, herr
		}
		fields[teamFieldHandle], fields[teamFieldProject] = hv, ref
		w, err = r.Create(ctx, schema.TableTeams, fields)
	}
	if err != nil {
		return TeamWriteOut{}, err
	}
	r.Push(ctx)
	return TeamWriteOut{Team: teamOf(w, actors), Verb: verb, Warnings: r.Warnings(), Where: r.Where()}, nil
}

func teamList(ctx context.Context, env profilestore.Store, in TeamListIn) (TeamListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return TeamListOut{}, err
	}
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamListOut{}, err
	}
	rows, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return TeamListOut{}, err
	}
	pending, err := pendingByTeam(ctx, r)
	if err != nil {
		return TeamListOut{}, err
	}
	out := TeamListOut{Teams: []Team{}, Where: r.Where()}
	for _, x := range rows {
		t := teamOf(x, actors)
		t.Pending = pending[t.Handle]
		if t.Active || in.All {
			out.Teams = append(out.Teams, t)
		}
	}
	slices.SortFunc(out.Teams, func(a, b Team) int { return strings.Compare(a.Handle, b.Handle) })
	return out, nil
}

func teamShow(ctx context.Context, env profilestore.Store, in TeamRefIn) (TeamShowOut, error) {
	h, err := teamHandle(in.Handle)
	if err != nil {
		return TeamShowOut{}, err
	}
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return TeamShowOut{}, err
	}
	cur, ok, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamShowOut{}, err
	}
	if !ok {
		return TeamShowOut{}, teamMissErr(h, r.Where())
	}
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamShowOut{}, err
	}
	pending, err := pendingByTeam(ctx, r)
	if err != nil {
		return TeamShowOut{}, err
	}
	t := teamOf(cur, actors)
	t.Pending = pending[t.Handle]
	return TeamShowOut{Team: t, Where: r.Where()}, nil
}

func teamRemove(ctx context.Context, env profilestore.Store, in TeamRefIn) (TeamWriteOut, error) {
	h, err := teamHandle(in.Handle)
	if err != nil {
		return TeamWriteOut{}, err
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamWriteOut{}, err
	}
	cur, ok, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if !ok {
		return TeamWriteOut{}, teamMissErr(h, r.Where())
	}
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if fieldText(cur, teamFieldActive) == boolFalse {
		return TeamWriteOut{}, errs.New(errs.StatusChange, "team @"+h+" is already removed; nothing to remove").
			WithHint("lore team add --handle @" + h + " brings it back")
	}
	w, err := r.Update(ctx, schema.TableTeams, cur.ID, map[string]value.Value{teamFieldActive: value.BoolOf(false), teamFieldLeftAt: value.DateTimeFrom(r.Today())})
	if err != nil {
		return TeamWriteOut{}, err
	}
	r.Push(ctx)
	return TeamWriteOut{Team: teamOf(w, actors), Verb: verbTeamRemoved, Warnings: r.Warnings(), Where: r.Where()}, nil
}

// teamRename changes a team's handle (G-15: a team could only be
// recreated under a new handle, leaving its tasks on the removed one).
// Tasks hold the team's tem_ id, so they follow; pending invites hold the
// handle, so each one naming the old handle is rewritten.
func teamRename(ctx context.Context, env profilestore.Store, in TeamRenameIn) (TeamWriteOut, error) {
	h, err := teamRef(in.Handle)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if strings.TrimSpace(in.To) == "" {
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "name the new handle").WithHint("lore team rename --handle @" + h + " --to @<new-handle>")
	}
	to, err := teamHandle(in.To)
	if err != nil {
		return TeamWriteOut{}, err
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamWriteOut{}, err
	}
	cur, ok, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if !ok {
		return TeamWriteOut{}, teamMissErr(h, r.Where())
	}
	from := fieldText(cur, teamFieldHandle)
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if to == from {
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "team "+cur.ID.String()+" is already @"+to).WithHint("lore team list shows the teams")
	}
	if who := handleHolder(actors, to); who != "" {
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "handle @"+to+" is taken by "+who+" on the roster in "+r.Where()+"; a team and a person cannot share one").
			WithHint("choose another team handle (lore team rename --handle @" + from + " --to @" + to + "-team)")
	}
	if other, taken, ferr := findTeam(ctx, r, to); ferr != nil {
		return TeamWriteOut{}, ferr
	} else if taken {
		state := ""
		if fieldText(other, teamFieldActive) == boolFalse {
			state = " (removed)"
		}
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "handle @"+to+" is taken by team "+other.ID.String()+state+" on the roster in "+r.Where()).
			WithHint("lore team list --all shows every team; choose another handle")
	}
	hv, err := textValue("--to", to)
	if err != nil {
		return TeamWriteOut{}, err
	}
	w, err := r.Update(ctx, schema.TableTeams, cur.ID, map[string]value.Value{teamFieldHandle: hv})
	if err != nil {
		return TeamWriteOut{}, err
	}
	out := TeamWriteOut{Verb: verbTeamRenamed, From: from, Where: r.Where()}
	invs, err := readInvites(ctx, r)
	if err != nil {
		return TeamWriteOut{}, err
	}
	for _, x := range invs {
		if !slices.Contains(x.Teams, from) || onboard.EffectiveInviteStatus(x.record(), r.Today()) != schema.InviteStatusPending {
			continue
		}
		next := slices.Clone(x.Teams)
		for i := range next {
			if next[i] == from {
				next[i] = to
			}
		}
		tl, terr := textList("teams", slices.Compact(next))
		if terr != nil {
			return TeamWriteOut{}, terr
		}
		if _, uerr := r.Update(ctx, schema.TableInvites, x.ID, map[string]value.Value{schema.FieldInviteTeams: tl}); uerr != nil {
			return TeamWriteOut{}, errs.Prefix("rename @"+from+" on invite "+x.ID.String(), uerr)
		}
		out.Pending = append(out.Pending, x.Email+" (invite "+x.ID.String()+")")
	}
	r.Push(ctx)
	out.Team, out.Warnings = teamOf(w, actors), r.Warnings()
	return out, nil
}

// teamRef is a team named by @handle or tem_ id: the id as given, else
// the checked handle.
func teamRef(in string) (string, error) {
	if ref := strings.TrimSpace(in); isTeamID(ref) {
		return ref, nil
	}
	return teamHandle(in)
}

func teamMembers(ctx context.Context, env profilestore.Store, in TeamMembersIn, add bool) (TeamWriteOut, error) {
	h, err := teamHandle(in.Handle)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if len(splitList(in.Members)) == 0 {
		return TeamWriteOut{}, errs.New(errs.InvalidInput, "name at least one person").WithHint("lore team member add --handle @" + h + " --members @mira")
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamWriteOut{}, err
	}
	cur, ok, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if !ok {
		return TeamWriteOut{}, teamMissErr(h, r.Where())
	}
	if fieldText(cur, teamFieldActive) == boolFalse {
		return TeamWriteOut{}, errs.New(errs.StatusChange, "team @"+h+" is removed; its members do not change").
			WithHint("lore team add --handle @" + h + " brings it back first")
	}
	actors, err := rosterActors(ctx, r)
	if err != nil {
		return TeamWriteOut{}, err
	}
	refs := in.Members
	var pendingInv []inviteRow
	if add {
		// someone invited but not joined yet is not on the roster: the
		// team goes on their pending invite and they join it on accept
		if refs, pendingInv, err = splitInvitees(ctx, r, refs); err != nil {
			return TeamWriteOut{}, err
		}
	} else {
		// taking someone off: a left person resolves too (no status check)
		refs = nil
	}
	named, found, err := resolveMembers(ctx, r, refs)
	if err != nil {
		return TeamWriteOut{}, err
	}
	if !add {
		for _, ref := range splitList(in.Members) {
			a, rerr := r.ResolveActor(ctx, ref)
			if rerr != nil {
				return TeamWriteOut{}, rerr
			}
			if !slices.Contains(named, a.ID.String()) {
				named = append(named, a.ID.String())
			}
			found[a.ID.String()] = a
		}
	}
	for id, a := range found {
		actors[id] = a
	}
	list := value.ItemStrings(cur.Fields[schema.FieldTeamMembers])
	out := TeamWriteOut{Verb: verbTeamMembersAdded, Where: r.Where()}
	if !add {
		out.Verb = verbTeamMembersRemove
	}
	for _, id := range named {
		shown := memberOf(id, actors[id]).shown()
		in := slices.Contains(list, id)
		switch {
		case add && !in:
			list = append(list, id)
			out.Changed = append(out.Changed, shown)
		case !add && in:
			list = slices.DeleteFunc(list, func(x string) bool { return x == id })
			out.Changed = append(out.Changed, shown)
		default:
			out.Unchanged = append(out.Unchanged, shown)
		}
	}
	for _, x := range pendingInv {
		if !slices.Contains(x.Teams, h) {
			tl, terr := textList("teams", append(slices.Clone(x.Teams), h))
			if terr != nil {
				return TeamWriteOut{}, terr
			}
			if _, uerr := r.Update(ctx, schema.TableInvites, x.ID, map[string]value.Value{schema.FieldInviteTeams: tl}); uerr != nil {
				return TeamWriteOut{}, errs.Prefix("record @"+h+" on invite "+x.ID.String(), uerr)
			}
		}
		out.Pending = append(out.Pending, x.Email+" (invite "+x.ID.String()+")")
	}
	w := cur
	if len(out.Changed) > 0 || len(pendingInv) > 0 {
		ml, merr := membersValue(list)
		if merr != nil {
			return TeamWriteOut{}, merr
		}
		if len(out.Changed) > 0 {
			if w, err = r.Update(ctx, schema.TableTeams, cur.ID, map[string]value.Value{schema.FieldTeamMembers: ml}); err != nil {
				return TeamWriteOut{}, err
			}
		}
		r.Push(ctx)
	}
	out.Team = teamOf(w, actors)
	out.Warnings = r.Warnings()
	return out, nil
}

// splitInvitees takes out of refs each email that names no one on the
// roster but has a pending invite, and returns those invites; the other
// refs are resolved as members. An email with no roster person and no
// pending invite stays in refs (the member lookup names the fix).
func splitInvitees(ctx context.Context, r profilestore.Roster, refs []string) ([]string, []inviteRow, error) {
	var keep []string
	var invs []inviteRow
	var all []inviteRow
	read := false
	for _, ref := range splitList(refs) {
		if !strings.Contains(ref, "@") || strings.HasPrefix(ref, handleMark) {
			keep = append(keep, ref)
			continue
		}
		if _, err := r.ResolveActor(ctx, ref); err == nil || !errs.Has(err, errs.NotFound) {
			keep = append(keep, ref)
			continue
		}
		email, nerr := teamprofile.Normalize(schema.ContactKindEmail, ref)
		if nerr != nil {
			keep = append(keep, ref)
			continue
		}
		if !read {
			var err error
			if all, err = readInvites(ctx, r); err != nil {
				return nil, nil, err
			}
			read = true
		}
		found := false
		for _, x := range all {
			if x.Email == email && onboard.EffectiveInviteStatus(x.record(), r.Today()) == schema.InviteStatusPending {
				invs = append(invs, x)
				found = true
				break
			}
		}
		if !found {
			keep = append(keep, ref)
		}
	}
	return keep, invs, nil
}

// pendingByTeam maps each team handle to the people whose pending invite
// names it.
func pendingByTeam(ctx context.Context, r profilestore.Roster) (map[string][]PendingMember, error) {
	all, err := readInvites(ctx, r)
	if err != nil {
		return nil, err
	}
	out := map[string][]PendingMember{}
	for _, x := range all {
		if len(x.Teams) == 0 || onboard.EffectiveInviteStatus(x.record(), r.Today()) != schema.InviteStatusPending {
			continue
		}
		for _, h := range x.Teams {
			out[h] = append(out[h], PendingMember{Email: x.Email, Invite: x.ID.String(), Until: x.day()})
		}
	}
	return out, nil
}
