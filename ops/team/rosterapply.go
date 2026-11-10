package team

// rosterapply.go — the team-side writes of the join and admin steps
// (ONBOARDING_DESIGN 4.5 Apply 1–5, 4.6 Apply). The wire Applier
// (setup.Applier for onboard.ApplyJoin / onboard.ApplyTeam) calls
// ApplyJoin and ApplyAdmin; the setups[] entry (4.5 step 6) stays the
// Applier's.
//
//	ApplyJoin  ──▶ orgs[] entry? no ──▶ org.import (registers the actor on the roster)
//	           ──▶ roster (write) ──▶ invites row by id (E_INVITE_NOT_FOUND)
//	           ──▶ DecideInvite (revoked / used / expired / email; same actor = done)
//	           ──▶ email + github identity contacts ──▶ open period of the row's type
//	           ──▶ left/archived? status active (rejoin, TEAM_PROFILES 5.2)
//	           ──▶ memberships per row project with the row's roles
//	           ──▶ invite accepted + accepted_by ──▶ Push
//	ApplyAdmin ──▶ orgs[] entry? no ──▶ store.init ──▶ org.create
//	           ──▶ roster (write) ──▶ identity contacts ──▶ open employee period ──▶ Push
//
// Every row is written through profilestore.Roster (the org roster
// store's engine). Each write first looks for what an earlier run left,
// so a re-run after a partial failure finishes the job and a re-run after
// success writes nothing (the invite is marked accepted last).

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
	"github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
)

// OrgEntries answers the orgs[] entry of an org (Env implements it).
type OrgEntries interface {
	// TeamOrigin is E_NOT_FOUND when ~/.lore/config.yaml has no entry.
	TeamOrigin(ctx context.Context, org string) (Origin, error)
}

// JoinDeps is what ApplyJoin calls. Profiles must open the roster of the
// invite's org (the wire Applier builds it after the import, for that org).
type JoinDeps struct {
	Orgs     orgcmd.Host
	Entries  OrgEntries
	Profiles profilestore.Store
}

// JoinRequest is the join step's answers (4.5): the decoded invite line
// and the identity (4.2) and GitHub (4.3) answers. Roles, projects and
// the period type are NOT here: they come from the invites row, so an
// edited line grants nothing.
type JoinRequest struct {
	Invite onboard.Invite
	// Emails are the person's identity addresses; the invite's address
	// must be one of them and becomes the primary email contact.
	Emails     []string
	Name       string
	Handle     string
	GitHubUser string
}

// RosterResult is what ApplyJoin / ApplyAdmin did.
type RosterResult struct {
	// Actor is the writing actor (act_ id).
	Actor string
	// Imported / Created: the org entry was added by this run.
	Imported bool
	Created  bool
	// Rejoined: the actor was left or archived and is active again.
	Rejoined bool
	// Already: the invite was accepted by this actor before; nothing written.
	Already bool
	// Wrote lists the row ids written or changed, in order.
	Wrote []string
}

// ApplyJoin is the member join write (4.5 Apply 1–5).
func ApplyJoin(ctx context.Context, d JoinDeps, req JoinRequest) (RosterResult, error) {
	inv := req.Invite
	email := ""
	for _, e := range req.Emails {
		if onboard.SameEmail(e, inv.Email) {
			email = strings.TrimSpace(e)
		}
	}
	if email == "" {
		return RosterResult{}, errs.New(errs.InviteEmail, "invite "+inv.ID+" is for "+inv.Email+"; use that address or ask for a new invite").
			WithHint("add " + inv.Email + " to the identity step's emails")
	}
	inviteID, err := ids.ParseAny(inv.ID)
	if err != nil || inviteID.Prefix() != ids.PrefixInvite {
		return RosterResult{}, errs.New(errs.InviteInvalid, "this is not a lore invite; copy the whole line starting with "+onboard.InvitePrefix)
	}
	var res RosterResult
	if res.Imported, err = ensureImported(ctx, d, req, email); err != nil {
		return RosterResult{}, err
	}
	r, done, err := d.Profiles.Roster(ctx, true)
	defer done()
	if err != nil {
		return RosterResult{}, err
	}
	invRow, err := r.Get(ctx, schema.TableInvites, inviteID)
	if errs.Has(err, errs.NotFound) || (err == nil && invRow.Deleted) {
		return RosterResult{}, errs.New(errs.InviteNotFound, "invite "+inv.ID+" is not in "+inv.Org+"'s store; ask "+inv.By+" to run lore team invite "+inv.Email+" again")
	}
	if err != nil {
		return RosterResult{}, err
	}
	x, err := inviteOf(invRow)
	if err != nil {
		return RosterResult{}, err
	}
	// Rejoin (4.5): the roster App's caller is already the canonical actor
	// — wire canonicalActor rebinds a key that is an identity contact of
	// an existing actor (TEAM_PROFILES 6.3, engine RebindActor) — so an
	// offboarded person invited again writes on their old actor row.
	actor, err := r.Get(ctx, schema.TableActors, r.Caller().Any())
	if err != nil {
		return RosterResult{}, errs.Prefix("the joining person is not on the roster in "+r.Where()+" after org.import", err).
			WithHint("lore actor register, then lore setup join again")
	}
	res.Actor = actor.ID.String()
	today := r.Today()
	verdict, err := onboard.DecideInvite(x.record(), inv.By, today, onboard.Acceptor{Actor: res.Actor, Email: email})
	if err != nil {
		return RosterResult{}, err
	}
	if verdict == onboard.VerdictAlready {
		res.Already = true
		return res, nil
	}
	w := &writer{r: r, actor: actor, today: today}
	// TP-12 before any write: a membership the invite would start today
	// may not begin on (or inside) an ended stint of the same project.
	if err = w.checkStints(ctx, x.Projects); err != nil {
		return RosterResult{}, err
	}
	if err = w.identity(ctx, req.Emails, email, req.GitHubUser); err != nil {
		return RosterResult{}, err
	}
	// The period before the status: the period synthesised from
	// active/left_at must be read from the row as it was (a left actor's
	// ended stint), then materialised.
	if err = w.openPeriod(ctx, x.PeriodType); err != nil {
		return RosterResult{}, err
	}
	if res.Rejoined, err = w.reactivate(ctx); err != nil {
		return RosterResult{}, err
	}
	for _, p := range x.Projects {
		if err = w.membership(ctx, p, x.Roles); err != nil {
			return RosterResult{}, err
		}
	}
	if err = w.accept(ctx, x.ID); err != nil {
		return RosterResult{}, err
	}
	r.Push(ctx)
	res.Wrote = w.wrote
	return res, nil
}

// ensureImported runs org.import unless the orgs[] entry exists for the
// same store (a re-run). An entry of that name on another store is
// E_SETUP_ORG_MISMATCH: lore never re-points an org entry silently.
func ensureImported(ctx context.Context, d JoinDeps, req JoinRequest, email string) (bool, error) {
	inv := req.Invite
	origin, err := d.Entries.TeamOrigin(ctx, inv.Org)
	switch {
	case err == nil:
		if !sameStore(origin.Store, inv.Store) {
			return false, errs.New(errs.SetupOrgMismatch, "an org entry named "+inv.Org+" exists for store "+origin.Store+", not "+inv.Store).
				WithHint("lore org rename " + inv.Org + " <other name>, then lore setup join again")
		}
		return false, nil
	case !errs.Has(err, errs.NotFound):
		return false, err
	}
	rep, err := d.Orgs.Import(ctx, orgcmd.ImportIn{URL: inv.Store, Key: humanKeyPrefix + email, Name: req.Name, Handle: req.Handle, As: inv.Org}, true)
	if err != nil {
		return false, err
	}
	if rep.Ask != "" {
		return false, errs.New(errs.Internal, "org.import asked although the join step confirmed it: "+rep.Ask)
	}
	return true, nil
}

// AdminRequest is the admin step's answers (4.6 questions plus the
// identity and GitHub answers).
type AdminRequest struct {
	Org       string
	StoreURL  string
	OrgFolder string
	Own       []string
	Repos     []string
	// Emails are the admin's identity addresses; the first is the key
	// (human:<email>) and the primary email contact.
	Emails     []string
	Name       string
	Handle     string
	GitHubUser string
}

// AdminDeps is what ApplyAdmin calls. Profiles must open the roster of
// the new org (the wire Applier builds it after org.create).
type AdminDeps struct {
	Stores   stores.Manager
	Orgs     orgcmd.Host
	Entries  OrgEntries
	Profiles profilestore.Store
}

// ApplyAdmin is the start-a-team write (4.6 Apply): store.init, org.create,
// then the admin's identity contacts and open employee period.
func ApplyAdmin(ctx context.Context, d AdminDeps, req AdminRequest) (RosterResult, error) {
	if len(req.Emails) == 0 || strings.TrimSpace(req.Emails[0]) == "" {
		return RosterResult{}, errs.New(errs.InvalidInput, "the admin needs an identity email (the identity step)")
	}
	primary := strings.TrimSpace(req.Emails[0])
	var res RosterResult
	origin, err := d.Entries.TeamOrigin(ctx, req.Org)
	switch {
	case err == nil:
		if !sameStore(origin.Store, req.StoreURL) {
			return RosterResult{}, errs.New(errs.SetupOrgMismatch, "an org entry named "+req.Org+" exists for store "+origin.Store+"; pick another name, or lore org rename").
				WithHint("lore org rename " + req.Org + " <other name>")
		}
	case !errs.Has(err, errs.NotFound):
		return RosterResult{}, err
	default:
		if _, err = d.Stores.InitStore(ctx, stores.InitIn{Name: req.Org, URL: req.StoreURL, RepoOptional: true}); err != nil {
			return RosterResult{}, err
		}
		folder := req.OrgFolder
		if folder == "" {
			folder = orgcmd.DefaultOrgFolder
		}
		rep, cerr := d.Orgs.Create(ctx, orgcmd.CreateIn{Org: req.Org, Store: req.StoreURL, OrgFolder: folder, Own: req.Own, Repos: req.Repos,
			Key: humanKeyPrefix + primary, Name: req.Name, Handle: req.Handle}, true)
		if cerr != nil {
			return RosterResult{}, cerr
		}
		if rep.Ask != "" {
			return RosterResult{}, errs.New(errs.Internal, "org.create asked although the team step confirmed it: "+rep.Ask)
		}
		res.Created = true
	}
	r, done, err := d.Profiles.Roster(ctx, true)
	defer done()
	if err != nil {
		return RosterResult{}, err
	}
	actor, err := r.Get(ctx, schema.TableActors, r.Caller().Any())
	if err != nil {
		return RosterResult{}, errs.Prefix("the admin is not on the roster in "+r.Where()+" after org.create", err).
			WithHint("lore actor register")
	}
	res.Actor = actor.ID.String()
	w := &writer{r: r, actor: actor, today: r.Today()}
	if err = w.identity(ctx, req.Emails, primary, req.GitHubUser); err != nil {
		return RosterResult{}, err
	}
	if err = w.openPeriod(ctx, schema.PeriodTypeEmployee); err != nil {
		return RosterResult{}, err
	}
	r.Push(ctx)
	res.Wrote = w.wrote
	return res, nil
}

// sameStore compares two store urls, ignoring space, a trailing slash and
// a trailing .git (the same repo spelled two ways).
func sameStore(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(s), "/"), gitSuffix)
	}
	return norm(a) == norm(b)
}

// writer writes one person's roster rows and records what it wrote.
type writer struct {
	r     profilestore.Roster
	actor row.Row
	today time.Time
	wrote []string
}

func (w *writer) note(id ids.AnyID) { w.wrote = append(w.wrote, id.String()) }

func (w *writer) actorRef() (value.Ref, error) { return refValue(w.actor.ID) }

// reactivate makes a left or archived actor active again (the rejoin of
// TEAM_PROFILES 5.2; active/left_at kept in step, 2.1); like every write
// that makes someone active it clears the status note (3.4).
func (w *writer) reactivate(ctx context.Context) (bool, error) {
	if teamprofile.Pickable(teamprofile.StatusOf(w.actor.Fields)) {
		return false, nil
	}
	st, err := textValue("status", schema.ActorStatusActive)
	if err != nil {
		return false, err
	}
	patch := map[string]value.Value{schema.FieldActorStatus: st, schema.FieldActorActive: value.BoolOf(true),
		schema.FieldActorLeftAt: value.Null{}, schema.FieldActorLeaveFrom: value.Null{}, schema.FieldActorLeaveUntil: value.Null{},
		schema.FieldActorStatusNote: value.Null{}}
	a, err := w.r.Update(ctx, schema.TableActors, w.actor.ID, patch)
	if err != nil {
		return false, err
	}
	w.actor = a
	w.note(a.ID)
	return true, nil
}

// contact is one actor_contacts row, read.
type contact struct {
	ID       ids.AnyID
	Actor    string
	Type     string
	Norm     string
	Primary  bool
	Identity bool
}

func (w *writer) contacts(ctx context.Context) ([]contact, error) {
	rows, err := w.r.List(ctx, schema.TableActorContacts)
	if err != nil {
		return nil, err
	}
	out := make([]contact, 0, len(rows))
	for _, x := range rows {
		if x.Deleted {
			continue
		}
		out = append(out, contact{ID: x.ID, Actor: fieldText(x, schema.FieldContactActor), Type: fieldText(x, schema.FieldContactType),
			Norm: fieldText(x, schema.FieldContactValueNorm), Primary: fieldText(x, schema.FieldContactPrimary) == boolTrue,
			Identity: fieldText(x, schema.FieldContactIdentity) == boolTrue})
	}
	return out, nil
}

// identity writes the identity contacts (4.5 step 2, 4.6): one email row
// per address (identity, primaryEmail primary, the other primaries
// cleared, TP-3) and one github row. A contact the actor already has is
// brought to identity (and primary) in place; one held as identity by
// another actor is E_CONTACT_TAKEN (TEAM_PROFILES 6.4), never a twin.
func (w *writer) identity(ctx context.Context, emails []string, primaryEmail, githubUser string) error {
	all, err := w.contacts(ctx)
	if err != nil {
		return err
	}
	primaryNorm, err := teamprofile.Normalize(schema.ContactKindEmail, primaryEmail)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "email "+primaryEmail+" is not an address", err)
	}
	var seen []string
	for _, e := range emails {
		norm, nerr := teamprofile.Normalize(schema.ContactKindEmail, e)
		if nerr != nil {
			return errs.Wrap(errs.InvalidInput, "email "+e+" is not an address", nerr)
		}
		if slices.Contains(seen, norm) {
			continue
		}
		seen = append(seen, norm)
		if err := w.ensureContact(ctx, all, teamprofile.ContactTypeEmail, strings.TrimSpace(e), norm, norm == primaryNorm); err != nil {
			return err
		}
	}
	// TP-3: at most one primary email; the invite's address wins.
	for _, c := range all {
		if c.Actor == w.actor.ID.String() && c.Type == teamprofile.ContactTypeEmail && c.Primary && c.Norm != primaryNorm {
			if _, err := w.r.Update(ctx, schema.TableActorContacts, c.ID, map[string]value.Value{schema.FieldContactPrimary: value.BoolOf(false)}); err != nil {
				return err
			}
			w.note(c.ID)
		}
	}
	if strings.TrimSpace(githubUser) == "" {
		return nil
	}
	norm, err := teamprofile.Normalize(schema.ContactKindHandle, githubUser)
	if err != nil || norm == "" {
		return errs.New(errs.InvalidInput, "GitHub user "+githubUser+" is not a user name")
	}
	hasPrimary := slices.ContainsFunc(all, func(c contact) bool {
		return c.Actor == w.actor.ID.String() && c.Type == teamprofile.ContactTypeGitHub && c.Primary
	})
	return w.ensureContact(ctx, all, teamprofile.ContactTypeGitHub, strings.TrimSpace(githubUser), norm, !hasPrimary)
}

// ensureContact writes or completes one identity contact.
func (w *writer) ensureContact(ctx context.Context, all []contact, typ, val, norm string, primary bool) error {
	me := w.actor.ID.String()
	for _, c := range all {
		if c.Type != typ || c.Norm != norm {
			continue
		}
		if c.Actor != me {
			if c.Identity {
				return errs.New(errs.ContactTaken, typ+" "+norm+" is an identity contact of "+c.Actor+", not of "+me).
					WithHint("lore actor show " + typ + ":" + norm + " shows who has it; ask the org admin to fix the roster")
			}
			continue
		}
		patch := map[string]value.Value{}
		if !c.Identity {
			patch[schema.FieldContactIdentity] = value.BoolOf(true)
		}
		if primary && !c.Primary {
			patch[schema.FieldContactPrimary] = value.BoolOf(true)
		}
		if len(patch) > 0 {
			if _, err := w.r.Update(ctx, schema.TableActorContacts, c.ID, patch); err != nil {
				return err
			}
			w.note(c.ID)
		}
		return nil
	}
	ref, err := w.actorRef()
	if err != nil {
		return err
	}
	f := map[string]value.Value{schema.FieldContactActor: ref, schema.FieldContactIdentity: value.BoolOf(true), schema.FieldContactPrimary: value.BoolOf(primary)}
	if f[schema.FieldContactType], err = textValue("type", typ); err != nil {
		return err
	}
	if f[schema.FieldContactValue], err = textValue("value", val); err != nil {
		return err
	}
	if f[schema.FieldContactValueNorm], err = textValue("value", norm); err != nil {
		return err
	}
	c, err := w.r.Create(ctx, schema.TableActorContacts, f)
	if err != nil {
		return err
	}
	w.note(c.ID)
	return nil
}

// openPeriod writes an open period of typ starting today (4.5 step 3),
// unless the actor already has one. The period synthesised from
// active/left_at (TP-22) counts as open only when it began before today:
// one that begins today is the actor org.import just registered, so the
// invite's type is written instead of the synthesised "employee". An
// ended synthesised period (a rejoin) is materialised first, so the old
// stint is never lost (TEAM_PROFILES 8).
func (w *writer) openPeriod(ctx context.Context, typ string) error {
	if !slices.Contains(schema.PeriodTypes(), typ) {
		return errs.New(errs.InvalidInput, "period type "+typ+" is not one of "+strings.Join(schema.PeriodTypes(), ", "))
	}
	rows, err := w.r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return err
	}
	ps, err := teamprofile.PeriodsOf(w.actor, rows)
	if err != nil {
		return err
	}
	today := teamprofile.Day(w.today)
	var kept []teamprofile.Period
	for _, p := range ps {
		if p.Open() && (!p.Synthesised() || p.StartedOn.Before(today)) {
			return nil
		}
		if p.Synthesised() && p.Open() {
			continue // replaced by the invite's period below
		}
		if p.Synthesised() {
			f, ferr := p.Fields()
			if ferr != nil {
				return ferr
			}
			x, cerr := w.r.Create(ctx, schema.TableActorPeriods, f)
			if cerr != nil {
				return cerr
			}
			p.ID = x.ID
			w.note(x.ID)
		}
		kept = append(kept, p)
	}
	next := teamprofile.Period{ActorID: w.actor.ID, Type: typ, StartedOn: today}
	if err := teamprofile.CheckPeriods(append(kept, next)); err != nil {
		return err
	}
	f, err := next.Fields()
	if err != nil {
		return err
	}
	x, err := w.r.Create(ctx, schema.TableActorPeriods, f)
	if err != nil {
		return err
	}
	w.note(x.ID)
	return nil
}

// checkStints applies member.add's TP-12 boundary to the memberships
// an invite accept would start today (membership below): ended_on is a
// stint's last day, as a company period's is (TEAM_PROFILES 5.2, 5.4),
// so a project with an ended stint the new open one would share a day
// with (teamprofile.Span.Overlaps: it covers today, or starts later) is
// E_PERIOD_OVERLAP naming the day after it. A project with an open
// stint is skipped: the accept only adds roles there. It runs before
// any roster write, so a refused accept writes nothing.
func (w *writer) checkStints(ctx context.Context, projects []string) error {
	if len(projects) == 0 {
		return nil
	}
	rows, err := w.r.List(ctx, schema.TableProjectMembers)
	if err != nil {
		return err
	}
	today := teamprofile.Day(w.today)
	me := w.actor.ID.String()
	for _, project := range projects {
		var ended []row.Row
		open := false
		for _, x := range rows {
			if x.Deleted || fieldText(x, schema.FieldMemberProject) != project || fieldText(x, schema.FieldMemberActor) != me {
				continue
			}
			if fieldText(x, schema.FieldMemberEndedOn) == "" {
				open = true
				break
			}
			ended = append(ended, x)
		}
		if open {
			continue
		}
		for _, x := range ended {
			start, serr := time.Parse(value.DateLayout, fieldText(x, schema.FieldMemberStartedOn))
			end, eerr := time.Parse(value.DateLayout, fieldText(x, schema.FieldMemberEndedOn))
			if serr != nil || eerr != nil {
				return errs.New(errs.InvalidInput, "membership "+x.ID.String()+" has an unreadable started_on/ended_on").
					WithHint("lore project member list " + project + " --history")
			}
			if !(teamprofile.Span{Start: today}).Overlaps(teamprofile.Span{Start: start, End: end}) {
				continue
			}
			endS, next := end.Format(value.DateLayout), end.AddDate(0, 0, 1).Format(value.DateLayout)
			stint := start.Format(value.DateLayout) + " → " + endS
			if start.After(today) {
				return errs.New(errs.PeriodOverlap, "the invite starts a membership of "+project+" today ("+today.Format(value.DateLayout)+
					"), which would run through the later stint "+stint).
					WithHint("accept it again on " + next + ", the day after the stint " + stint)
			}
			when := "tomorrow (" + next + ")"
			if end.After(today) {
				when = "on " + next
			}
			return errs.New(errs.PeriodOverlap, "the invite starts a membership of "+project+" today ("+today.Format(value.DateLayout)+
				"), inside the stint "+stint).
				WithHint("accept it again " + when + ": the old stint's last day is " + endS)
		}
	}
	return nil
}

// membership opens a project membership with roles (4.5 step 4): an open
// stint gets the roles added (union, as member.add does), else a new
// stint starts today.
func (w *writer) membership(ctx context.Context, project string, roles []string) error {
	if len(roles) == 0 {
		return errs.New(errs.InvalidInput, "the invite names project "+project+" but no role; a membership needs one (TP-10)").
			WithHint("ask the org admin for a new invite with --role")
	}
	pid, err := ids.ParseAny(project)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "the invite's project "+project+" is not a prj_ id", err)
	}
	rows, err := w.r.List(ctx, schema.TableProjectMembers)
	if err != nil {
		return err
	}
	me := w.actor.ID.String()
	for _, x := range rows {
		if x.Deleted || fieldText(x, schema.FieldMemberProject) != project || fieldText(x, schema.FieldMemberActor) != me ||
			fieldText(x, schema.FieldMemberEndedOn) != "" {
			continue
		}
		have := value.ItemStrings(x.Fields[schema.FieldMemberRoles])
		union := slices.Clone(have)
		for _, n := range roles {
			if !slices.Contains(union, n) {
				union = append(union, n)
			}
		}
		if len(union) == len(have) {
			return nil
		}
		l, lerr := textList("roles", union)
		if lerr != nil {
			return lerr
		}
		if _, err := w.r.Update(ctx, schema.TableProjectMembers, x.ID, map[string]value.Value{schema.FieldMemberRoles: l}); err != nil {
			return err
		}
		w.note(x.ID)
		return nil
	}
	pref, err := refValue(pid)
	if err != nil {
		return err
	}
	aref, err := w.actorRef()
	if err != nil {
		return err
	}
	l, err := textList("roles", roles)
	if err != nil {
		return err
	}
	start, err := dateValue(w.today)
	if err != nil {
		return err
	}
	x, err := w.r.Create(ctx, schema.TableProjectMembers, map[string]value.Value{schema.FieldMemberProject: pref,
		schema.FieldMemberActor: aref, schema.FieldMemberRoles: l, schema.FieldMemberStartedOn: start})
	if err != nil {
		return err
	}
	w.note(x.ID)
	return nil
}

// accept marks the invite used by this actor (4.5 step 5), last, so a
// failed run leaves it pending for the re-run.
func (w *writer) accept(ctx context.Context, id ids.AnyID) error {
	st, err := textValue("status", schema.InviteStatusAccepted)
	if err != nil {
		return err
	}
	ref, err := w.actorRef()
	if err != nil {
		return err
	}
	x, err := w.r.Update(ctx, schema.TableInvites, id, map[string]value.Value{schema.FieldInviteStatus: st, schema.FieldInviteAcceptedBy: ref})
	if err != nil {
		return err
	}
	w.note(x.ID)
	return nil
}

// AddIdentityEmail makes email an identity contact, and the primary email,
// of the caller's roster actor (the actor of the identity key config.yaml
// holds now). The setup page's identity step calls it before it moves a
// team setup's orgs[].identity.key to that email: the roster then rebinds
// the new key to the same actor (TEAM_PROFILES 6.3), so the person keeps
// one roster entry with the old address as an earlier contact. Without it
// the new key became a second actor, and saving the team step again was
// E_CONTACT_TAKEN (the GitHub contact belonged to the first).
func AddIdentityEmail(ctx context.Context, profiles profilestore.Store, email string) (RosterResult, error) {
	r, done, err := profiles.Roster(ctx, true)
	defer done()
	if err != nil {
		return RosterResult{}, err
	}
	actor, err := r.Get(ctx, schema.TableActors, r.Caller().Any())
	if err != nil {
		return RosterResult{}, errs.Prefix("you are not on the roster in "+r.Where(), err).WithHint("lore actor register")
	}
	w := &writer{r: r, actor: actor, today: r.Today()}
	if err = w.identity(ctx, []string{email}, email, ""); err != nil {
		return RosterResult{}, err
	}
	r.Push(ctx)
	return RosterResult{Actor: actor.ID.String(), Wrote: w.wrote}, nil
}

// IdentityEmails lists the caller's identity email contacts on the roster
// (primary first). Saving a team setup's join or team step again sends
// them with the identity answers, so an address the person had before
// (the invite's, after an email change) still counts as theirs.
func IdentityEmails(ctx context.Context, profiles profilestore.Store) ([]string, error) {
	r, done, err := profiles.Roster(ctx, false)
	defer done()
	if err != nil {
		return nil, err
	}
	w := &writer{r: r}
	all, err := w.contacts(ctx)
	if err != nil {
		return nil, err
	}
	me := r.Caller().String()
	var primary, rest []string
	for _, c := range all {
		if c.Actor != me || c.Type != teamprofile.ContactTypeEmail || !c.Identity {
			continue
		}
		if c.Primary {
			primary = append(primary, c.Norm)
		} else {
			rest = append(rest, c.Norm)
		}
	}
	return append(primary, rest...), nil
}
