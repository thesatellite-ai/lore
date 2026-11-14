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
//	           ──▶ the row's teams (team invite --team, team member add
//	               <email> while pending): added to each active one
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

	"github.com/thesatellite-ai/lore/engine"
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
	"github.com/thesatellite-ai/lore/store"
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
	// Teams are the team handles the invite named that the person is now
	// a member of; SkippedTeams those removed (or gone) since the invite,
	// which nobody joins.
	Teams        []string
	SkippedTeams []string
	// OrgRole is the org-level role of the joining person's open stint
	// (OrgRoleAdmin for the org's admin), "" when none is set. Why
	// (I-17): an admin's second machine joins by the roster too, and its
	// setup must be an admin setup, or every admin command there refused
	// with "ask the org admin".
	OrgRole string
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
	// The invite check comes before anyone is registered (owner item 19):
	// org.import runs it as Admit, so a missing, revoked, used, expired or
	// mismatched invite writes no actor row, no commit and no push; an
	// entry that was already here is checked before the write-mode open.
	admit := func(ctx context.Context, _ string) error {
		return checkInvite(ctx, d.Profiles, inv, inviteID, req.Emails, email)
	}
	// SkipRegister (W-12): the joining person's actor row is written below,
	// in the join's one store commit, not as a commit of its own first.
	if res.Imported, err = ensureImported(ctx, d, req, email, admit, true); err != nil {
		return RosterResult{}, err
	}
	if !res.Imported {
		if err = checkInvite(ctx, d.Profiles, inv, inviteID, req.Emails, email); err != nil {
			return RosterResult{}, err
		}
	}
	// W-12: the whole join is ONE store commit and one push: the roster
	// opens without registering the caller (RegisterInCommit), and the
	// registration is the first staged row below.
	sctx := engine.Staging(store.WithCommitTitle(ctx, "setup join: "+email))
	committed := false
	defer func() {
		if !committed {
			engine.Discard(sctx)
		}
	}()
	r, done, err := d.Profiles.Roster(WithRegisterInCommit(ctx), true)
	defer done()
	if err != nil {
		return RosterResult{}, err
	}
	registered := false
	if rr, ok := r.(CallerRegisterer); ok {
		if registered, err = rr.RegisterCaller(sctx); err != nil {
			return RosterResult{}, err
		}
	}
	x, err := readInvite(ctx, r, inv, inviteID)
	if err != nil {
		return RosterResult{}, err
	}
	// Rejoin (4.5): the roster App's caller is already the canonical actor
	// — wire canonicalActor rebinds a key that is an identity contact of
	// an existing actor (TEAM_PROFILES 6.3, engine RebindActor) — so an
	// offboarded person invited again writes on their old actor row.
	actor, err := r.Get(sctx, schema.TableActors, r.Caller().Any())
	if errs.Has(err, errs.NotFound) && registered {
		// registered just now, in the stage: reads see it only after the
		// commit, so the writes below act on the row as it was written
		// (a person, active, created today: no stint yet)
		actor, err = row.Row{Table: schema.TableActors, ID: r.Caller().Any(), CreatedAt: r.Today(), UpdatedAt: r.Today(),
			Fields: map[string]value.Value{staffFieldKind: mustText(schema.ActorKindHuman), actorFieldStatus: mustText(schema.ActorStatusActive),
				actorFieldActive: value.BoolOf(true)}}, nil
	}
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
		committed = true
		if _, err = engine.Commit(sctx); err != nil { // a registration made for a join already done
			return RosterResult{}, err
		}
		r.Push(ctx)
		return res, nil
	}
	w := &writer{r: r, actor: actor, today: today}
	// TP-12 before any write: a membership the invite would start today
	// may not begin on (or inside) an ended stint of the same project.
	if err = w.checkStints(ctx, x.Projects); err != nil {
		return RosterResult{}, err
	}
	// W-12: every roster row of the join (contacts, stint, status,
	// memberships, teams, the invite accepted) in ONE store commit, one
	// push, so a teammate never fetches half a join.
	err = func(ctx context.Context) error {
		if err := w.identity(ctx, req.Emails, email, req.GitHubUser); err != nil {
			return err
		}
		// The period before the status: the period synthesised from
		// active/left_at must be read from the row as it was (a left
		// actor's ended stint), then materialised.
		if err := w.openPeriod(ctx, x.PeriodType); err != nil {
			return err
		}
		var werr error
		if res.Rejoined, werr = w.reactivate(ctx); werr != nil {
			return werr
		}
		for _, p := range x.Projects {
			if err := w.membership(ctx, p, x.Roles); err != nil {
				return err
			}
		}
		if res.Teams, res.SkippedTeams, werr = w.joinTeams(ctx, x.Teams); werr != nil {
			return werr
		}
		return w.accept(ctx, x.ID)
	}(sctx)
	if err != nil {
		return RosterResult{}, err
	}
	committed = true
	if _, err = engine.Commit(sctx); err != nil {
		return RosterResult{}, errs.Prefix("setup join: the roster write (nothing written)", err)
	}
	r.Push(ctx)
	res.Wrote = w.wrote
	return res, nil
}

// readInvite is the invites row the line names; E_INVITE_NOT_FOUND when
// the store has none (or it is deleted).
func readInvite(ctx context.Context, r profilestore.Roster, inv onboard.Invite, id ids.AnyID) (inviteRow, error) {
	invRow, err := r.Get(ctx, schema.TableInvites, id)
	if errs.Has(err, errs.NotFound) || (err == nil && invRow.Deleted) {
		return inviteRow{}, errs.New(errs.InviteNotFound, "invite "+inv.ID+" is not in "+inv.Org+"'s store; ask "+inv.By+" to run lore team invite --email "+inv.Email+" again")
	}
	if err != nil {
		return inviteRow{}, err
	}
	return inviteOf(invRow)
}

// checkInvite is the invite join's check before anyone is registered,
// on a read-only open of the roster: the row exists and DecideInvite
// accepts it (not revoked, not used by someone else, not expired, for
// this email). The acceptor is the caller's actor when it is on the
// roster, else the actor one of emails names (a rejoin, a re-run: the
// same actor is Already), else nobody, so an accepted invite is used. The full decision runs
// again on the write-mode open, as the registered actor.
func checkInvite(ctx context.Context, profiles profilestore.Store, inv onboard.Invite, id ids.AnyID, emails []string, email string) error {
	r, done, err := profiles.Roster(ctx, false)
	defer done()
	if err != nil {
		return err
	}
	x, err := readInvite(ctx, r, inv, id)
	if err != nil {
		return err
	}
	who := onboard.Acceptor{Email: email}
	if a, gerr := r.Get(ctx, schema.TableActors, r.Caller().Any()); gerr == nil && !a.Deleted {
		who.Actor = a.ID.String() // a re-run: the caller is on the roster already
	}
	for _, e := range memberEmails(emails) {
		for _, ref := range []string{"email:" + e, humanKeyPrefix + e} {
			if a, rerr := r.ResolveActor(ctx, ref); rerr == nil && who.Actor == "" {
				who.Actor = a.ID.String()
			}
		}
	}
	_, err = onboard.DecideInvite(x.record(), inv.By, r.Today(), who)
	return err
}

// ensureImported runs org.import unless the orgs[] entry exists for the
// same store (a re-run). An entry of that name on another store is
// E_SETUP_ORG_MISMATCH: lore never re-points an org entry silently.
func ensureImported(ctx context.Context, d JoinDeps, req JoinRequest, email string, admit func(context.Context, string) error, skipRegister bool) (bool, error) {
	inv := req.Invite
	origin, err := d.Entries.TeamOrigin(ctx, inv.Org)
	switch {
	case err == nil:
		if !sameStore(origin.Store, inv.Store) {
			return false, errs.New(errs.SetupOrgMismatch, "an org entry named "+inv.Org+" exists for store "+origin.Store+", not "+inv.Store).
				WithHint("lore org rename --old " + inv.Org + " --new <other name>, then lore setup join again")
		}
		return false, nil
	case !errs.Has(err, errs.NotFound):
		return false, err
	}
	rep, err := d.Orgs.Import(ctx, orgcmd.ImportIn{URL: inv.Store, Key: humanKeyPrefix + email, Name: req.Name, Handle: req.Handle, As: inv.Org, Admit: admit, SkipRegister: skipRegister}, true)
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
				WithHint("lore org rename --old " + req.Org + " --new <other name>")
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
	// whoever starts the team is its admin (owner report 2026-11-12: the
	// creator showed org role "-" in member list)
	w := &writer{r: r, actor: actor, today: r.Today(), orgRole: OrgRoleAdmin}
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
	// orgRole is the org-level role a new open stint gets (member add
	// --role), and that setOrgRole puts on the open one.
	orgRole string
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
					WithHint("lore actor show --id " + typ + ":" + norm + " shows who has it; ask the org admin to fix the roster")
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
			if w.orgRole != "" && !p.Synthesised() {
				return w.setOrgRole(ctx)
			}
			if !p.Synthesised() || w.orgRole == "" {
				return nil
			}
			// an open stint synthesised from active/left_at: written now,
			// so the org role has a row to live on
			f, ferr := p.Fields()
			if ferr != nil {
				return ferr
			}
			if f[schema.FieldPeriodOrgRole], ferr = textValue("--role", w.orgRole); ferr != nil {
				return ferr
			}
			x, cerr := w.r.Create(ctx, schema.TableActorPeriods, f)
			if cerr != nil {
				return cerr
			}
			w.note(x.ID)
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
	if reopened, err := w.reopenEndedToday(ctx, kept, typ, today); err != nil || reopened {
		return err
	}
	next := teamprofile.Period{ActorID: w.actor.ID, Type: typ, StartedOn: today}
	if err := teamprofile.CheckPeriods(append(kept, next)); err != nil {
		return err
	}
	f, err := next.Fields()
	if err != nil {
		return err
	}
	if w.orgRole != "" {
		if f[schema.FieldPeriodOrgRole], err = textValue("--role", w.orgRole); err != nil {
			return err
		}
	}
	x, err := w.r.Create(ctx, schema.TableActorPeriods, f)
	if err != nil {
		return err
	}
	w.note(x.ID)
	return nil
}

// reopenEndedToday undoes a removal made today (B2-14): a stored stint
// that ended today is reopened (no end, no end reason; the type and org
// role asked for now) instead of a new stint starting today, which would
// overlap it (an end day is inclusive) and left no command able to bring
// a person removed by mistake back before tomorrow. reopened is false
// when no stint ended today.
func (w *writer) reopenEndedToday(ctx context.Context, kept []teamprofile.Period, typ string, today time.Time) (reopened bool, err error) {
	for i := len(kept) - 1; i >= 0; i-- {
		p := kept[i]
		if p.Synthesised() || p.Open() || !teamprofile.Day(p.EndedOn).Equal(today) {
			continue
		}
		patch := map[string]value.Value{schema.FieldPeriodEndedOn: value.Null{}, schema.FieldPeriodEndReason: value.Null{}}
		if patch[schema.FieldPeriodType], err = textValue("type", typ); err != nil {
			return false, err
		}
		if w.orgRole != "" {
			if patch[schema.FieldPeriodOrgRole], err = textValue("--role", w.orgRole); err != nil {
				return false, err
			}
		}
		if _, err = w.r.Update(ctx, schema.TableActorPeriods, p.ID, patch); err != nil {
			return false, err
		}
		w.note(p.ID)
		return true, nil
	}
	return false, nil
}

// setOrgRole puts w.orgRole on the actor's stored open stint (member edit
// --role, member add for someone already on the roster); with none it
// opens an employee stint carrying it.
func (w *writer) setOrgRole(ctx context.Context) error {
	rows, err := w.r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return err
	}
	for _, p := range rows {
		if fieldText(p, schema.FieldPeriodActor) != w.actor.ID.String() || fieldText(p, schema.FieldPeriodEndedOn) != "" {
			continue
		}
		if fieldText(p, schema.FieldPeriodOrgRole) == w.orgRole {
			return nil
		}
		v, verr := textValue("--role", w.orgRole)
		if verr != nil {
			return verr
		}
		if _, err := w.r.Update(ctx, schema.TableActorPeriods, p.ID, map[string]value.Value{schema.FieldPeriodOrgRole: v}); err != nil {
			return err
		}
		w.note(p.ID)
		return nil
	}
	return w.openPeriod(ctx, schema.PeriodTypeEmployee)
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
					WithHint("lore project member list --project " + project + " --history")
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
		return errs.New(errs.InvalidInput, "the invite names project "+project+" but no role; a membership needs one").
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
// joinTeams adds the joining person to each team handle of the invite
// (a re-run finds them in it and writes nothing). A team removed or gone
// since the invite is skipped and returned in skipped: nobody joins it.
func (w *writer) joinTeams(ctx context.Context, handles []string) (joined, skipped []string, err error) {
	me := w.actor.ID.String()
	for _, h := range handles {
		cur, ok, ferr := findTeam(ctx, w.r, h)
		if ferr != nil {
			return nil, nil, ferr
		}
		if !ok || fieldText(cur, teamFieldActive) == boolFalse {
			skipped = append(skipped, h)
			continue
		}
		joined = append(joined, h)
		list := value.ItemStrings(cur.Fields[schema.FieldTeamMembers])
		if slices.Contains(list, me) {
			continue
		}
		ml, merr := membersValue(append(list, me))
		if merr != nil {
			return nil, nil, merr
		}
		if _, uerr := w.r.Update(ctx, schema.TableTeams, cur.ID, map[string]value.Value{schema.FieldTeamMembers: ml}); uerr != nil {
			return nil, nil, errs.Prefix("join team @"+h+" from the invite", uerr)
		}
		w.note(cur.ID)
	}
	return joined, skipped, nil
}

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

// MemberJoinRequest is a member's join without an invite (owner report
// 2026-11-12): the org (its entry name and store url) and the identity
// answers. lore finds the person on the roster by their email.
type MemberJoinRequest struct {
	Org        string
	Store      string
	Emails     []string
	Name       string
	Handle     string
	GitHubUser string
}

// ApplyJoinMember joins a person an admin added with `lore member add`:
// the org entry (org.import, as an invite join), then the roster actor
// whose email identity contact is one of the person's emails and who is
// a member (an open stint). Nothing else is granted here: role, teams and
// projects were written by member add.
//
// The member check comes before any write (owner item 19): org.import
// runs with Admit, so the entry is checked against the roster before the
// caller would be registered, and a person not on the roster gets the
// reason and the admin's command with no actor row, no commit and no
// push (the entry the import added is taken out again).
func ApplyJoinMember(ctx context.Context, d JoinDeps, req MemberJoinRequest) (RosterResult, error) {
	emails := memberEmails(req.Emails)
	if len(emails) == 0 {
		return RosterResult{}, errs.New(errs.InvalidInput, "join "+req.Org+": no email to find you on the roster by").
			WithHint("answer the identity step with your work email (git config user.email)")
	}
	inv := onboard.Invite{Org: req.Org, Store: req.Store, Email: emails[0]}
	var res RosterResult
	var err error
	admit := func(ctx context.Context, entry string) error { return CheckMember(ctx, d.Profiles, entry, emails) }
	if res.Imported, err = ensureImported(ctx, d, JoinRequest{Invite: inv, Emails: emails, Name: req.Name, Handle: req.Handle, GitHubUser: req.GitHubUser}, emails[0], admit, true); err != nil {
		return RosterResult{}, err
	}
	// The read-only check again for an entry that was already here (a
	// re-run, or the setup apply that imported it by url): still before
	// the write-mode open, which registers nobody but is the write path.
	if err = CheckMember(ctx, d.Profiles, req.Org, emails); err != nil {
		return RosterResult{}, err
	}
	r, done, err := d.Profiles.Roster(ctx, true)
	defer done()
	if err != nil {
		return RosterResult{}, err
	}
	actor, matched := findMember(ctx, r, emails)
	if matched == "" {
		return RosterResult{}, notMember(req.Org, r.Where(), emails)
	}
	res.Actor = actor.ID.String()
	w := &writer{r: r, actor: actor, today: r.Today()}
	if err = joinInOneCommit(ctx, "setup join: "+matched, func(ctx context.Context) error {
		return w.identity(ctx, emails, matched, req.GitHubUser) // W-12: the contacts in one commit
	}); err != nil {
		return RosterResult{}, err
	}
	if len(w.wrote) > 0 {
		r.Push(ctx)
	} else {
		res.Already = true
	}
	res.Wrote = w.wrote
	if res.OrgRole, err = openOrgRole(ctx, r, actor.ID.String()); err != nil {
		return RosterResult{}, err
	}
	return res, nil
}

// openOrgRole is the org role of actor's open stint on r ("" when it has
// none or no role).
func openOrgRole(ctx context.Context, r profilestore.Roster, actor string) (string, error) {
	rows, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return "", err
	}
	for _, p := range rows {
		if fieldText(p, schema.FieldPeriodActor) == actor && fieldText(p, schema.FieldPeriodEndedOn) == "" {
			return fieldText(p, schema.FieldPeriodOrgRole), nil
		}
	}
	return "", nil
}

// CheckMember is the member check of a join without an invite, run on a
// read-only open of org's roster before anything is written: nil when
// one of emails belongs to a member (an open stint), else E_NOT_FOUND
// with the admin's command. setup apply passes it as org.import's Admit.
func CheckMember(ctx context.Context, profiles profilestore.Store, org string, emails []string) error {
	emails = memberEmails(emails)
	if len(emails) == 0 {
		return errs.New(errs.InvalidInput, "join "+org+": no email to find you on the roster by").
			WithHint("answer the identity step with your work email (git config user.email)")
	}
	r, done, err := profiles.Roster(ctx, false)
	defer done()
	if err != nil {
		return err
	}
	if _, matched := findMember(ctx, r, emails); matched == "" {
		return notMember(org, r.Where(), emails)
	}
	return nil
}

// memberEmails are the normalized, distinct addresses of emails.
func memberEmails(in []string) []string {
	var emails []string
	for _, e := range in {
		if n, err := teamprofile.Normalize(schema.ContactKindEmail, e); err == nil && n != "" && !slices.Contains(emails, n) {
			emails = append(emails, n)
		}
	}
	return emails
}

// findMember is the member actor one of emails names (its email identity
// contact, else the human:<email> key member add writes) and the address
// that matched; "" when none is a member.
func findMember(ctx context.Context, r profilestore.Roster, emails []string) (row.Row, string) {
	for _, e := range emails {
		for _, ref := range []string{"email:" + e, humanKeyPrefix + e} {
			if a, err := r.ResolveActor(ctx, ref); err == nil && isMember(ctx, r, a) {
				return a, e
			}
		}
	}
	return row.Row{}, ""
}

// notMember is the refusal of a join by someone not on org's roster: the
// cause and the next step (an admin adds them, or an invite line).
func notMember(org, where string, emails []string) error {
	return errs.New(errs.NotFound, strings.Join(emails, ", ")+" is not a member of "+org+" ("+where+"); nothing was written").
		WithHint("ask an admin to run: lore member add --email " + emails[0] + ", then lore setup join again (an outsider joins with an invite line instead)")
}

// OrgRoleAdmin is the org-level role of the person who created the org.
const OrgRoleAdmin = "admin"

// joinInOneCommit runs write with every roster write staged and commits
// them as ONE store commit titled title, as member add does (W-12: a join
// wrote each contact, the stint, the memberships and the invite as
// commits of their own, each pushed). A failed write discards the stage:
// nothing is written.
func joinInOneCommit(ctx context.Context, title string, write func(ctx context.Context) error) error {
	sctx := engine.Staging(store.WithCommitTitle(ctx, title))
	if err := write(sctx); err != nil {
		engine.Discard(sctx)
		return err
	}
	if _, err := engine.Commit(sctx); err != nil {
		return errs.Prefix(title+" (nothing written)", err)
	}
	return nil
}

// registerInCommitKey marks a roster open whose caller registration the
// caller writes itself, inside its own staged commit (W-12).
type registerInCommitKey struct{}

// WithRegisterInCommit is ctx for a write-mode Roster open that must not
// register the caller on its own commit: the join registers them with
// CallerRegisterer.RegisterCaller inside its one staged commit (W-12).
func WithRegisterInCommit(ctx context.Context) context.Context {
	return context.WithValue(ctx, registerInCommitKey{}, true)
}

// RegisterInCommit reports whether ctx asks the roster open to leave the
// caller's registration to the op (WithRegisterInCommit).
func RegisterInCommit(ctx context.Context) bool {
	v, ok := ctx.Value(registerInCommitKey{}).(bool)
	return ok && v
}

// CallerRegisterer is the optional upgrade of an opened Roster that
// registers its caller (as `lore actor register` does: identity-contact
// match, handle check) when they have no row yet; with a staged ctx the
// row joins the op's one commit (W-12). wire's org roster implements it.
type CallerRegisterer interface {
	// RegisterCaller reports whether it wrote the caller's row.
	RegisterCaller(ctx context.Context) (bool, error)
}

// mustText is a fixed, valid text value.
func mustText(s string) value.Value {
	v, _ := value.TextOf(s) // the constants passed are valid text
	return v
}
