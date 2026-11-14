package team

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/member"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
)

// invite is team.invite (ONB 4.6, Q8): one pending invites row in the org
// roster store, then the line and the GitHub access state.
//
// Run twice: the second run finds the first pending invite for the same
// email and refuses (one invite per person, Q8), naming resend/revoke.
func (o ops) invite(ctx context.Context, in setup.TeamInviteIn) (setup.TeamInviteOut, error) {
	e, err := adminSetup(ctx, o.env)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	email, err := teamprofile.Normalize(schema.ContactKindEmail, in.Email)
	if err != nil {
		return setup.TeamInviteOut{}, errs.Wrap(errs.InvalidInput, "email "+work.Quote(in.Email)+" is not an address", err).
			WithHint("lore team invite tomas@agency.example")
	}
	periodType := strings.TrimSpace(in.Type)
	if periodType == "" {
		periodType = schema.PeriodTypeEmployee
	}
	if !slices.Contains(schema.PeriodTypes(), periodType) {
		return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "--type "+work.Quote(periodType)+" is not one of "+strings.Join(schema.PeriodTypes(), ", "))
	}
	ghUser := ""
	if strings.TrimSpace(in.GitHub) != "" {
		if ghUser, err = teamprofile.Normalize(schema.ContactKindHandle, in.GitHub); err != nil || ghUser == "" {
			return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "--github "+work.Quote(in.GitHub)+" is not a GitHub user name")
		}
	}
	roles, projectRefs := splitList(in.Roles), splitList(in.Projects)
	if len(projectRefs) > 0 && len(roles) == 0 {
		return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "--project needs at least one --role: a project membership has one or more roles").
			WithHint("--role dev")
	}
	if len(roles) > 0 && len(projectRefs) == 0 {
		return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "--role is applied on each --project, and no --project is given").
			WithHint("--project <project>")
	}
	origin, err := o.env.TeamOrigin(ctx, e.Org)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	repo, onGitHub := GitHubRepo(origin.Store)
	if in.Grant {
		if ghUser == "" || !onGitHub {
			return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "--grant needs --github <user> and an org store on github.com (store: "+origin.Store+")")
		}
		if !op.Confirmed(ctx) {
			// Outward (4.6): asked before anything is written, so the
			// confirmed re-run does not find its own invite pending.
			q := "Grant " + ghUser + " push access to " + repo.Owner + "/" + repo.Name + " on GitHub?"
			return setup.TeamInviteOut{}, errs.New(errs.NeedsConfirm, "--grant runs "+strings.Join(GrantArgv(repo, ghUser), " ")+"\n"+q).
				WithQuestion(q).WithHint("nothing written; confirm with --yes (or \"yes\": true over lore api)")
		}
	}

	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	today := r.Today()
	lastDay, err := parseExpires(in.Expires, today)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	if err := checkRoles(ctx, r, roles); err != nil {
		return setup.TeamInviteOut{}, err
	}
	teams, err := inviteTeams(ctx, r, in.Teams)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	projects := make([]string, 0, len(projectRefs))
	for _, ref := range projectRefs {
		id, perr := resolveProject(ctx, o.env, r, ref)
		if perr != nil {
			return setup.TeamInviteOut{}, perr
		}
		if !slices.Contains(projects, id) {
			projects = append(projects, id)
		}
	}
	all, err := readInvites(ctx, r)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	for _, x := range all {
		if x.Email == email && onboard.EffectiveInviteStatus(x.record(), today) == schema.InviteStatusPending {
			return setup.TeamInviteOut{}, errs.New(errs.InvalidInput, "invite "+x.ID.String()+" for "+email+" is still pending (until "+x.day()+"); one invite per person").
				WithHint("lore team invite resend --ref " + email + " prints it again; lore team invite revoke --ref " + email + " ends it")
		}
	}

	fields, err := inviteFields(email, ghUser, roles, projects, periodType, lastDay)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	if len(teams) > 0 {
		if fields[schema.FieldInviteTeams], err = textList("--team", teams); err != nil {
			return setup.TeamInviteOut{}, err
		}
	}
	created, err := r.Create(ctx, schema.TableInvites, fields)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	r.Push(ctx)
	inv, err := inviteOf(created)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	text, err := o.line(ctx, r, e, origin, inv)
	if err != nil {
		return setup.TeamInviteOut{}, err
	}
	out := setup.TeamInviteOut{ID: inv.ID.String(), Line: text, ExpiresOn: inv.day(), Email: email, Org: e.Org, GitHub: ghUser, Teams: inv.Teams}
	// C5: the invitee's join reads the invite from the store's remote; a
	// roster commit still in this clone fails it (E27) until lore sync.
	if h, ok := r.(profilestore.PushHolder); ok && h.Held() {
		out.SyncFirst = true
	}
	if ghUser != "" && onGitHub {
		out.Repo = repo.Owner + "/" + repo.Name
		out.GitHubAccess, out.GitHubDetail = o.githubStep(ctx, e, repo, ghUser, in.Grant)
		if out.GitHubAccess == GitHubPending {
			out.GitHubFix = strings.Join(GrantArgv(repo, ghUser), " ")
		}
	} else if ghUser != "" {
		out.GitHubAccess = GitHubPending
	}
	return out, nil
}

// githubStep is the access check after the row is written: with --grant
// (already confirmed) it grants; otherwise it only probes. Any failure is
// "pending": the invite stands, the person cannot read the store yet.
// detail is set when gh could not answer at all (missing, not logged in,
// an API error): then pending means "unknown", and the text must not say
// the person has no access (A11 bug 7).
func (o ops) githubStep(ctx context.Context, e onboard.SetupEntry, repo Repo, user string, grant bool) (state, detail string) {
	g, err := o.env.TeamGitHub(ctx)
	if err != nil {
		return GitHubPending, ghUnknownDetail(err.Error())
	}
	st, out := githubAccess(ctx, g, e.GitHub.GHAccount, repo, user)
	if st == accessYes {
		return GitHubGranted, ""
	}
	if grant && grantAccess(ctx, g, e.GitHub.GHAccount, repo, user) == nil {
		return GitHubGranted, ""
	}
	if st == accessUnknown {
		return GitHubPending, ghUnknownDetail(out)
	}
	return GitHubPending, ""
}

// inviteTeams resolves --team values to the handles of active teams on
// the roster (no @), each once; an unknown or removed team is refused
// before anything is written, naming the fix.
func inviteTeams(ctx context.Context, r profilestore.Roster, refs []string) ([]string, error) {
	var out []string
	for _, ref := range splitList(refs) {
		h, err := teamHandle(ref)
		if err != nil {
			return nil, err
		}
		cur, ok, err := findTeam(ctx, r, h)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, teamMissErr(h, r.Where())
		}
		if fieldText(cur, teamFieldActive) == boolFalse {
			return nil, errs.New(errs.StatusChange, "--team @"+h+": the team is removed; nobody joins it").
				WithHint("lore team add --handle @" + h + " brings it back first")
		}
		if h = fieldText(cur, teamFieldHandle); !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out, nil
}

// inviteFields is a new pending invites row.
func inviteFields(email, ghUser string, roles, projects []string, periodType string, lastDay time.Time) (map[string]value.Value, error) {
	f := map[string]value.Value{}
	var err error
	if f[schema.FieldInviteEmail], err = textValue("email", email); err != nil {
		return nil, err
	}
	if f[schema.FieldInvitePeriodType], err = textValue("--type", periodType); err != nil {
		return nil, err
	}
	if f[schema.FieldInviteStatus], err = textValue("status", schema.InviteStatusPending); err != nil {
		return nil, err
	}
	if f[schema.FieldInviteExpiresOn], err = dateValue(lastDay); err != nil {
		return nil, err
	}
	if ghUser != "" {
		if f[schema.FieldInviteGitHub], err = textValue("--github", ghUser); err != nil {
			return nil, err
		}
	}
	if len(roles) > 0 {
		if f[schema.FieldInviteRoles], err = textList("--role", roles); err != nil {
			return nil, err
		}
	}
	if len(projects) > 0 {
		if f[schema.FieldInviteProjects], err = textList("--project", projects); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// inviteRow is one invites row, read.
type inviteRow struct {
	ID         ids.AnyID
	Email      string
	GitHub     string
	Roles      []string
	Projects   []string
	Teams      []string
	PeriodType string
	Status     string
	ExpiresOn  time.Time
	AcceptedBy string
	CreatedBy  ids.ID[ids.Actor]
	// w is the row's writers (auditrec), for the list record.
	w auditrec.Writers
}

func (x inviteRow) day() string { return x.ExpiresOn.Format(value.DateLayout) }

func (x inviteRow) record() onboard.InviteRecord {
	return onboard.InviteRecord{ID: x.ID.String(), Email: x.Email, Status: x.Status, ExpiresOn: x.ExpiresOn, AcceptedBy: x.AcceptedBy}
}

// inviteOf reads an invites row; a missing or bad expires_on is
// E_INVALID_INPUT naming the row (an expiry is never guessed).
func inviteOf(r row.Row) (inviteRow, error) {
	x := inviteRow{ID: r.ID, Email: fieldText(r, schema.FieldInviteEmail), GitHub: fieldText(r, schema.FieldInviteGitHub),
		Roles: value.ItemStrings(r.Fields[schema.FieldInviteRoles]), Projects: value.ItemStrings(r.Fields[schema.FieldInviteProjects]),
		Teams:      value.ItemStrings(r.Fields[schema.FieldInviteTeams]),
		PeriodType: fieldText(r, schema.FieldInvitePeriodType), Status: fieldText(r, schema.FieldInviteStatus),
		AcceptedBy: fieldText(r, schema.FieldInviteAcceptedBy), CreatedBy: r.CreatedBy, w: auditrec.Of(r)}
	t, err := time.Parse(value.DateLayout, fieldText(r, schema.FieldInviteExpiresOn))
	if err != nil {
		return inviteRow{}, errs.Wrap(errs.InvalidInput, "invite "+r.ID.String()+": expires_on is not YYYY-MM-DD", err).
			WithHint("lore show --id " + r.ID.String())
	}
	x.ExpiresOn = t
	return x, nil
}

// readInvites lists the live invites rows, by id.
func readInvites(ctx context.Context, r profilestore.Roster) ([]inviteRow, error) {
	rows, err := r.List(ctx, schema.TableInvites)
	if err != nil {
		return nil, err
	}
	out := make([]inviteRow, 0, len(rows))
	for _, x := range rows {
		if x.Deleted {
			continue
		}
		inv, ierr := inviteOf(x)
		if ierr != nil {
			return nil, ierr
		}
		out = append(out, inv)
	}
	return out, nil
}

// line is the invite line of inv (4.5). Every part comes from the row and
// the org entry, so resend prints exactly what invite printed.
func (o ops) line(ctx context.Context, r profilestore.Roster, e onboard.SetupEntry, origin Origin, inv inviteRow) (string, error) {
	return onboard.EncodeInvite(onboard.Invite{ID: inv.ID.String(), Org: e.Org, Store: origin.Store, OrgFolder: origin.OrgFolder,
		Email: inv.Email, By: inviterEmail(ctx, r, e, inv), Expires: inv.day()})
}

// inviterEmail is "by" of the line: the email of the actor who wrote the
// row (its stable key), else the setup's first identity email.
func inviterEmail(ctx context.Context, r profilestore.Roster, e onboard.SetupEntry, inv inviteRow) string {
	if !inv.CreatedBy.IsZero() {
		if a, err := r.Get(ctx, schema.TableActors, inv.CreatedBy.Any()); err == nil {
			if email := actorEmail(a); email != "" {
				return email
			}
		}
	}
	if len(e.Identity.Emails) > 0 {
		return e.Identity.Emails[0]
	}
	return ""
}

// checkRoles refuses a role that is not live in the effective list: the
// built-ins overlaid by roles rows by name, a retired one out (TS-2, TP-8).
func checkRoles(ctx context.Context, r profilestore.Roster, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	live := map[string]bool{}
	for _, b := range teamprofile.BuiltinRoles() {
		live[b.Name] = true
	}
	rows, err := r.List(ctx, schema.TableRoles)
	if err != nil {
		return err
	}
	for _, x := range rows {
		if x.Deleted {
			continue
		}
		live[fieldText(x, schema.FieldRoleName)] = fieldText(x, schema.FieldRoleRetired) != boolTrue
	}
	for _, n := range roles {
		if !live[n] {
			return errs.New(errs.RoleUnknown, "role "+work.Quote(n)+" is not a live role").
				WithHint("lore role list shows them; lore role add --name " + n + " adds one")
		}
	}
	return nil
}

// boolTrue is the text form of a true bool field.
const boolTrue = "true"

// resolveProject resolves --project to a prj_ id: a prj_ id as given
// (project_members.project_id is AllowDangling, TEAM_PROFILES 2.6), else
// the member.ProjectFinder upgrade, else the roster store's own projects
// rows by name.
func resolveProject(ctx context.Context, env Env, r profilestore.Roster, ref string) (string, error) {
	if id, err := ids.Parse[ids.Project](ref); err == nil {
		return id.String(), nil
	}
	if f, ok := upgrade.As[member.ProjectFinder](env); ok {
		p, err := f.FindProject(ctx, ref)
		if err != nil {
			return "", err
		}
		return p.ID.String(), nil
	}
	rows, err := r.List(ctx, schema.TableProjects)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, x := range rows {
		if !x.Deleted && fieldText(x, fieldProjectName) == ref {
			hits = append(hits, x.ID.String())
		}
	}
	switch len(hits) {
	case 0:
		return "", errs.New(errs.NotFound, "no project "+work.Quote(ref)+" on "+r.Where()).WithHint("--project <prj_ id>")
	case 1:
		return hits[0], nil
	}
	return "", errs.New(errs.ProjectNameCollision, "project name "+work.Quote(ref)+" is used by "+strings.Join(hits, ", ")).
		WithHint("name it by its prj_ id")
}

// list is team.invite.list: every invite, newest id last, the status as
// read today (EffectiveInviteStatus); --status filters on that.
func (o ops) list(ctx context.Context, in setup.TeamInviteListIn) (setup.TeamInviteListOut, error) {
	want := strings.TrimSpace(in.Status)
	if want != "" && !slices.Contains(schema.InviteStatuses(), want) {
		return setup.TeamInviteListOut{}, errs.New(errs.InvalidInput, "--status "+work.Quote(want)+" is not one of "+strings.Join(schema.InviteStatuses(), ", "))
	}
	r, done, err := o.env.Roster(ctx, false)
	defer done()
	if err != nil {
		return setup.TeamInviteListOut{}, err
	}
	all, err := readInvites(ctx, r)
	if err != nil {
		return setup.TeamInviteListOut{}, err
	}
	out := setup.TeamInviteListOut{Invites: []setup.InviteRow{}}
	for _, x := range all {
		status := onboard.EffectiveInviteStatus(x.record(), r.Today())
		if want != "" && status != want {
			continue
		}
		out.Invites = append(out.Invites, setup.InviteRow{ID: x.ID.String(), Email: x.Email, GitHub: x.GitHub, Roles: x.Roles,
			Projects: x.Projects, Teams: x.Teams, PeriodType: x.PeriodType, Status: status, ExpiresOn: x.day(), AcceptedBy: x.AcceptedBy,
			CreatedBy: x.w.CreatedBy, UpdatedBy: x.w.UpdatedBy})
	}
	return out, nil
}

// findInvite resolves <inv_id|email>: an id names one row; an email names
// its pending invite, else its newest one (ids sort by time).
func findInvite(ctx context.Context, r profilestore.Roster, ref string) (inviteRow, error) {
	ref = strings.TrimSpace(ref)
	all, err := readInvites(ctx, r)
	if err != nil {
		return inviteRow{}, err
	}
	if strings.HasPrefix(ref, string(ids.PrefixInvite)+"_") {
		for _, x := range all {
			if x.ID.String() == ref {
				return x, nil
			}
		}
		return inviteRow{}, errs.New(errs.InviteNotFound, "invite "+ref+" is not in "+r.Where()).WithHint("lore team invite list")
	}
	email, nerr := teamprofile.Normalize(schema.ContactKindEmail, ref)
	if nerr != nil {
		return inviteRow{}, errs.Wrap(errs.InvalidInput, work.Quote(ref)+" is neither an inv_ id nor an email", nerr)
	}
	var found []inviteRow
	for _, x := range all {
		if x.Email == email {
			found = append(found, x)
		}
	}
	if len(found) == 0 {
		return inviteRow{}, errs.New(errs.InviteNotFound, "no invite for "+email+" in "+r.Where()).
			WithHint("lore team invite --email " + email)
	}
	today := r.Today()
	if i := slices.IndexFunc(found, func(x inviteRow) bool {
		return onboard.EffectiveInviteStatus(x.record(), today) == schema.InviteStatusPending
	}); i >= 0 {
		return found[i], nil
	}
	return found[len(found)-1], nil
}

// revoke is team.invite.revoke (7.1: asks first). Revoking a revoked
// invite is a no-op answer; an accepted one is refused (the person is on
// the team: lore actor offboard).
func (o ops) revoke(ctx context.Context, in setup.TeamInviteRefIn) (setup.TeamInviteRevokeOut, error) {
	if _, err := adminSetup(ctx, o.env); err != nil {
		return setup.TeamInviteRevokeOut{}, err
	}
	r, done, err := o.env.Roster(ctx, true)
	defer done()
	if err != nil {
		return setup.TeamInviteRevokeOut{}, err
	}
	x, err := findInvite(ctx, r, in.Ref)
	if err != nil {
		return setup.TeamInviteRevokeOut{}, err
	}
	switch x.Status {
	case schema.InviteStatusRevoked:
		return setup.TeamInviteRevokeOut{ID: x.ID.String(), Status: x.Status}, nil
	case schema.InviteStatusAccepted:
		return setup.TeamInviteRevokeOut{}, errs.New(errs.InvalidInput, "invite "+x.ID.String()+" was accepted by "+x.AcceptedBy+"; there is nothing to revoke").
			WithHint("lore actor offboard --actor " + x.AcceptedBy + " ends their time on the team")
	}
	if !op.Confirmed(ctx) {
		q := "Revoke invite " + x.ID.String() + " for " + x.Email + "?"
		return setup.TeamInviteRevokeOut{}, errs.New(errs.NeedsConfirm, q).WithQuestion(q).
			WithHint("nothing written; confirm with --yes (or \"yes\": true over lore api)")
	}
	status, err := textValue("status", schema.InviteStatusRevoked)
	if err != nil {
		return setup.TeamInviteRevokeOut{}, err
	}
	if _, err := r.Update(ctx, schema.TableInvites, x.ID, map[string]value.Value{schema.FieldInviteStatus: status}); err != nil {
		return setup.TeamInviteRevokeOut{}, err
	}
	r.Push(ctx)
	return setup.TeamInviteRevokeOut{ID: x.ID.String(), Status: schema.InviteStatusRevoked}, nil
}

// resend is team.invite.resend: the same line again, nothing written. A
// used, revoked or expired invite answers its E_INVITE_* code, since its
// line could no longer be accepted.
func (o ops) resend(ctx context.Context, in setup.TeamInviteRefIn) (setup.TeamInviteResendOut, error) {
	e, err := adminSetup(ctx, o.env)
	if err != nil {
		return setup.TeamInviteResendOut{}, err
	}
	r, done, err := o.env.Roster(ctx, false)
	defer done()
	if err != nil {
		return setup.TeamInviteResendOut{}, err
	}
	x, err := findInvite(ctx, r, in.Ref)
	if err != nil {
		return setup.TeamInviteResendOut{}, err
	}
	by := inviterEmail(ctx, r, e, x)
	switch onboard.EffectiveInviteStatus(x.record(), r.Today()) {
	case schema.InviteStatusPending:
	case schema.InviteStatusAccepted:
		return setup.TeamInviteResendOut{}, errs.New(errs.InviteUsed, "invite "+x.ID.String()+" was already used by "+x.AcceptedBy).
			WithHint("lore team invite --email " + x.Email + " writes a new one if another person needs it")
	case schema.InviteStatusRevoked:
		return setup.TeamInviteResendOut{}, errs.New(errs.InviteRevoked, "invite "+x.ID.String()+" was revoked").
			WithHint("lore team invite --email " + x.Email)
	default:
		return setup.TeamInviteResendOut{}, errs.New(errs.InviteExpired, "invite "+x.ID.String()+" expired on "+x.day()+"; ask "+by+" for a new one").
			WithHint("lore team invite --email " + x.Email)
	}
	origin, err := o.env.TeamOrigin(ctx, e.Org)
	if err != nil {
		return setup.TeamInviteResendOut{}, err
	}
	text, err := o.line(ctx, r, e, origin, x)
	if err != nil {
		return setup.TeamInviteResendOut{}, err
	}
	return setup.TeamInviteResendOut{ID: x.ID.String(), Text: text}, nil
}
