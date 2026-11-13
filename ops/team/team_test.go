package team

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
)

const (
	storeURL = "git@github.com:agency/lore-team.git"
	adminKey = "human:mira@agency.example"
)

var seq = 0x200

func testID[T ids.Tag](t *testing.T, p ids.Prefix) ids.ID[T] {
	t.Helper()
	seq++
	id, err := ids.Parse[T](fmt.Sprintf("%s_%012x7000a%015x", p, 0x01890a5d8a2b, seq))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// fakeSetups is a setup.Store answering Active and Config only.
type fakeSetups struct {
	setup.Store
	active string
	cfg    onboard.Config
}

func (s fakeSetups) Active(context.Context, string) (setup.ActiveSetup, error) {
	if s.active == "" {
		return setup.ActiveSetup{Why: setup.WhyNone}, nil
	}
	return setup.ActiveSetup{Name: s.active, Why: setup.WhyFlag}, nil
}
func (s fakeSetups) Config(context.Context) (onboard.Config, error) { return s.cfg, nil }

// fakeGH records every gh run and answers from a table by the first two
// args ("api PUT" for the grant, "api repos/..." for the probe).
type fakeGH struct {
	runs    [][]string
	probe   GHResult
	grant   GHResult
	account []string
}

func (g *fakeGH) RunGH(_ context.Context, account string, args []string) (GHResult, error) {
	g.runs = append(g.runs, slices.Clone(args))
	g.account = append(g.account, account)
	if slices.Contains(args, ghMethodPut) {
		return g.grant, nil
	}
	return g.probe, nil
}

// env is the test Env.
type env struct {
	*profilestoretest.Fake
	setups  fakeSetups
	origins map[string]Origin
	gh      *fakeGH
	// joinKey, joinHandle: the last import that left the registration to
	// the join (SkipRegister), as the wire roster's RegisterCaller reads
	// them from the org entry.
	joinKey, joinHandle string
}

// Roster opens the fake roster; a write open whose op registers the
// caller in its own commit (WithRegisterInCommit) gets RegisterCaller,
// as the wire org roster has (W-12).
func (e *env) Roster(ctx context.Context, write bool) (profilestore.Roster, func(), error) {
	r, done, err := e.Fake.Roster(ctx, write)
	if err != nil || !RegisterInCommit(ctx) {
		return r, done, err
	}
	return registeringRoster{Roster: r, e: e}, done, nil
}

// registeringRoster is the fake roster with CallerRegisterer.
type registeringRoster struct {
	profilestore.Roster
	e *env
}

func (r registeringRoster) RegisterCaller(context.Context) (bool, error) {
	for _, a := range r.e.All(schema.TableActors) {
		if a.ID == r.e.CallerID.Any() {
			return false, nil
		}
	}
	r.e.ensureActor(r.e.joinKey, r.e.joinHandle)
	return true, nil
}

func (e *env) SetupStore(context.Context) (setup.Store, error) { return e.setups, nil }
func (e *env) TeamOrigin(_ context.Context, org string) (Origin, error) {
	o, ok := e.origins[org]
	if !ok {
		return Origin{}, errs.New(errs.NotFound, "no org entry "+org)
	}
	return o, nil
}
func (e *env) TeamGitHub(context.Context) (GitHubRunner, error) { return e.gh, nil }

// fakeOrgs is an orgcmd.Host: Import and Create record the call and add
// the caller's actor row, as the real verbs register on the roster.
type fakeOrgs struct {
	orgcmd.Host
	e       *env
	imports []orgcmd.ImportIn
	creates []orgcmd.CreateIn
}

func (o *fakeOrgs) Import(_ context.Context, in orgcmd.ImportIn, _ bool) (orgcmd.Report, error) {
	o.imports = append(o.imports, in)
	o.e.origins[in.As] = Origin{Store: in.URL, OrgFolder: orgcmd.DefaultOrgFolder}
	if in.Admit != nil { // as the wire Import: check, take the entry out on a refusal
		if err := in.Admit(context.Background(), in.As); err != nil {
			delete(o.e.origins, in.As)
			return orgcmd.Report{}, err
		}
	}
	if in.SkipRegister {
		o.e.joinKey, o.e.joinHandle = in.Key, in.Handle
		return orgcmd.Report{}, nil
	}
	o.e.ensureActor(in.Key, in.Handle)
	return orgcmd.Report{}, nil
}
func (o *fakeOrgs) Create(_ context.Context, in orgcmd.CreateIn, _ bool) (orgcmd.Report, error) {
	o.creates = append(o.creates, in)
	o.e.origins[in.Org] = Origin{Store: in.Store, OrgFolder: in.OrgFolder}
	o.e.ensureActor(in.Key, in.Handle)
	return orgcmd.Report{}, nil
}

type fakeStores struct {
	stores.Manager
	inits []stores.InitIn
}

func (s *fakeStores) InitStore(_ context.Context, in stores.InitIn) (stores.InitOut, error) {
	s.inits = append(s.inits, in)
	return stores.InitOut{Name: in.Name, URL: in.URL}, nil
}

// ensureActor adds the caller's actors row (created now) unless present.
func (e *env) ensureActor(key, handle string) {
	for _, a := range e.All(schema.TableActors) {
		if a.ID == e.CallerID.Any() {
			return
		}
	}
	tv := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	e.Put(row.Row{Table: schema.TableActors, ID: e.CallerID.Any(), CreatedAt: e.Now, Fields: map[string]value.Value{
		"kind": tv(schema.ActorKindHuman), "display_name": tv(handle), "handle": tv(handle), "stable_key": tv(key), "active": value.BoolOf(true)}})
}

type fixture struct {
	t     *testing.T
	e     *env
	ctx   context.Context
	admin ids.ID[ids.Actor]
	hub   ids.ID[ids.Project]
	nw    ids.ID[ids.Project]
	orgs  *fakeOrgs
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	x := &fixture{t: t, ctx: op.WithSetup(context.Background(), "agency")}
	x.admin = testID[ids.Actor](t, ids.PrefixActor)
	x.hub = testID[ids.Project](t, ids.PrefixProject)
	x.nw = testID[ids.Project](t, ids.PrefixProject)
	f := profilestoretest.New(x.admin, time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC))
	x.e = &env{Fake: f, gh: &fakeGH{probe: GHResult{Exit: 1, Output: "gh: Not Found (HTTP 404)"}},
		origins: map[string]Origin{"agency": {Store: storeURL, OrgFolder: "org"}},
		setups: fakeSetups{active: "agency", cfg: onboard.Config{Setups: []onboard.SetupEntry{
			{Name: "agency", Kind: onboard.KindAdmin, Org: "agency", Identity: onboard.SetupIdentity{Emails: []string{"mira@agency.example"}},
				GitHub: onboard.SetupGitHub{GHAccount: "mira-gh"}},
			{Name: "client", Kind: onboard.KindMember, Org: "acme"},
		}}}}
	x.orgs = &fakeOrgs{e: x.e}
	x.e.ensureActor(adminKey, "mira")
	f.Put(row.Row{Table: schema.TableProjects, ID: x.hub.Any(), Fields: map[string]value.Value{"name": txt(t, "acme-hub")}})
	f.Put(row.Row{Table: schema.TableProjects, ID: x.nw.Any(), Fields: map[string]value.Value{"name": txt(t, "northwind")}})
	return x
}

func (x *fixture) ops() ops { return ops{env: x.e} }

func (x *fixture) invite(in setup.TeamInviteIn) setup.TeamInviteOut {
	x.t.Helper()
	out, err := x.ops().invite(x.ctx, in)
	if err != nil {
		x.t.Fatalf("invite %+v: %v", in, err)
	}
	return out
}

func wantCode(t *testing.T, name string, err error, code errs.Code) {
	t.Helper()
	if !errs.Has(err, code) {
		t.Errorf("%s: got %v, want %s", name, err, code)
	}
}

// TestInviteRowAndLine: team.invite writes one pending row with the
// row-side facts and prints a line that points at it; resend prints the
// same line and writes nothing.
func TestInviteRowAndLine(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: " Tomas@Agency.example", GitHub: "@Tomas-Dev", Roles: []string{"dev,reviewer"}, Projects: []string{"acme-hub"}})
	rows := x.e.All(schema.TableInvites)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if got := fieldText(r, schema.FieldInviteEmail); got != "tomas@agency.example" {
		t.Errorf("email = %q", got)
	}
	if got := fieldText(r, schema.FieldInviteStatus); got != schema.InviteStatusPending {
		t.Errorf("status = %q", got)
	}
	if got := fieldText(r, schema.FieldInvitePeriodType); got != schema.PeriodTypeEmployee {
		t.Errorf("period_type = %q", got)
	}
	if got := fieldText(r, schema.FieldInviteExpiresOn); got != "2026-11-20" || out.ExpiresOn != "2026-11-20" {
		t.Errorf("expires_on = %q / %q, want 2026-11-20 (14 days)", got, out.ExpiresOn)
	}
	if got := value.ItemStrings(r.Fields[schema.FieldInviteRoles]); !slices.Equal(got, []string{"dev", "reviewer"}) {
		t.Errorf("roles = %v", got)
	}
	if got := value.ItemStrings(r.Fields[schema.FieldInviteProjects]); !slices.Equal(got, []string{x.hub.String()}) {
		t.Errorf("projects = %v", got)
	}
	if got := fieldText(r, schema.FieldInviteGitHub); got != "tomas-dev" {
		t.Errorf("github = %q", got)
	}
	inv, err := onboard.DecodeInvite(out.Line)
	if err != nil {
		t.Fatal(err)
	}
	want := onboard.Invite{V: 1, ID: r.ID.String(), Org: "agency", Store: storeURL, OrgFolder: "org", Email: "tomas@agency.example", By: "mira@agency.example", Expires: "2026-11-20"}
	if inv != want || out.ID != r.ID.String() {
		t.Errorf("line = %+v, want %+v", inv, want)
	}
	if out.GitHubAccess != GitHubPending {
		t.Errorf("github_access = %q, want pending (probe 404)", out.GitHubAccess)
	}
	if x.e.Pushes != 1 {
		t.Errorf("pushes = %d, want 1", x.e.Pushes)
	}
	again, err := x.ops().resend(x.ctx, setup.TeamInviteRefIn{Ref: "tomas@agency.example"})
	if err != nil || again.Text != out.Line || again.ID != out.ID {
		t.Errorf("resend = %+v %v, want the same text", again, err)
	}
	if x.e.Pushes != 1 || len(x.e.All(schema.TableInvites)) != 1 {
		t.Error("resend wrote something")
	}
	// One invite per person (Q8): a second invite is refused.
	_, err = x.ops().invite(x.ctx, setup.TeamInviteIn{Email: "tomas@agency.example"})
	wantCode(t, "second invite", err, errs.InvalidInput)
}

// TestInviteInputs: the refusals before any write.
func TestInviteInputs(t *testing.T) {
	x := newFixture(t)
	cases := []struct {
		name string
		in   setup.TeamInviteIn
		code errs.Code
	}{
		{"bad email", setup.TeamInviteIn{Email: "tomas"}, errs.InvalidInput},
		{"bad type", setup.TeamInviteIn{Email: "t@agency.example", Type: "boss"}, errs.InvalidInput},
		{"project without role", setup.TeamInviteIn{Email: "t@agency.example", Projects: []string{"acme-hub"}}, errs.InvalidInput},
		{"role without project", setup.TeamInviteIn{Email: "t@agency.example", Roles: []string{"dev"}}, errs.InvalidInput},
		{"unknown role", setup.TeamInviteIn{Email: "t@agency.example", Roles: []string{"wizard"}, Projects: []string{"acme-hub"}}, errs.RoleUnknown},
		{"unknown project", setup.TeamInviteIn{Email: "t@agency.example", Roles: []string{"dev"}, Projects: []string{"nope"}}, errs.NotFound},
		{"expires 0d", setup.TeamInviteIn{Email: "t@agency.example", Expires: "0d"}, errs.InvalidInput},
		{"expires 91d", setup.TeamInviteIn{Email: "t@agency.example", Expires: "91d"}, errs.InvalidInput},
		{"expires week", setup.TeamInviteIn{Email: "t@agency.example", Expires: "1w"}, errs.InvalidInput},
		{"grant without github", setup.TeamInviteIn{Email: "t@agency.example", Grant: true}, errs.InvalidInput},
		{"grant unconfirmed", setup.TeamInviteIn{Email: "t@agency.example", GitHub: "tdev", Grant: true}, errs.NeedsConfirm},
	}
	for _, c := range cases {
		_, err := x.ops().invite(x.ctx, c.in)
		wantCode(t, c.name, err, c.code)
	}
	if n := len(x.e.All(schema.TableInvites)); n != 0 || len(x.e.gh.runs) != 0 {
		t.Errorf("refusals wrote %d rows and ran gh %d times", n, len(x.e.gh.runs))
	}
	out := x.invite(setup.TeamInviteIn{Email: "t@agency.example", Expires: "3d", Type: schema.PeriodTypeContractor})
	if out.ExpiresOn != "2026-11-09" {
		t.Errorf("3d expires_on = %s", out.ExpiresOn)
	}
	if out.GitHubAccess != "" || len(x.e.gh.runs) != 0 {
		t.Error("no --github: no gh run and no access state")
	}
}

// TestInviteAdminOnly: no setup, or a member setup, is refused.
func TestInviteAdminOnly(t *testing.T) {
	x := newFixture(t)
	x.e.setups.active = ""
	_, err := x.ops().invite(x.ctx, setup.TeamInviteIn{Email: "t@agency.example"})
	wantCode(t, "no setup", err, errs.Refused)
	x.e.setups.active = "client"
	_, err = x.ops().invite(x.ctx, setup.TeamInviteIn{Email: "t@agency.example"})
	wantCode(t, "member setup", err, errs.Refused)
	_, err = x.ops().revoke(x.ctx, setup.TeamInviteRefIn{Ref: "t@agency.example"})
	wantCode(t, "member revoke", err, errs.Refused)
	x.e.setups.active = "gone"
	_, err = x.ops().invite(x.ctx, setup.TeamInviteIn{Email: "t@agency.example"})
	wantCode(t, "unknown setup", err, errs.SetupNotFound)
	if len(x.e.All(schema.TableInvites)) != 0 {
		t.Error("a refused invite wrote a row")
	}
}

// TestInviteGrant: --grant runs the gh PUT only after the confirm, as
// the setup's gh account; an existing collaborator is granted without it.
func TestInviteGrant(t *testing.T) {
	x := newFixture(t)
	ctx := op.WithConfirmed(x.ctx, true)
	out, err := x.ops().invite(ctx, setup.TeamInviteIn{Email: "t@agency.example", GitHub: "tdev", Grant: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.GitHubAccess != GitHubGranted {
		t.Errorf("access = %q", out.GitHubAccess)
	}
	want := []string{"api", "-X", "PUT", "repos/agency/lore-team/collaborators/tdev", "-f", "permission=push"}
	if len(x.e.gh.runs) != 2 || !slices.Equal(x.e.gh.runs[1], want) || x.e.gh.account[1] != "mira-gh" {
		t.Errorf("gh runs = %v as %v, want probe then %v as mira-gh", x.e.gh.runs, x.e.gh.account, want)
	}
	// Probe says collaborator: granted, no PUT even without --grant.
	x.e.gh.probe = GHResult{Exit: 0}
	x.e.gh.runs = nil
	out = x.invite(setup.TeamInviteIn{Email: "u@agency.example", GitHub: "udev"})
	if out.GitHubAccess != GitHubGranted || len(x.e.gh.runs) != 1 {
		t.Errorf("collaborator: access %q, runs %v", out.GitHubAccess, x.e.gh.runs)
	}
	// A failing grant leaves the invite written, access pending.
	x.e.gh.probe = GHResult{Exit: 1, Output: "HTTP 404"}
	x.e.gh.grant = GHResult{Exit: 1, Output: "HTTP 403"}
	out, err = x.ops().invite(ctx, setup.TeamInviteIn{Email: "v@agency.example", GitHub: "vdev", Grant: true})
	if err != nil || out.GitHubAccess != GitHubPending || out.Line == "" || out.GitHubDetail != "" {
		t.Errorf("failed grant: %+v %v", out, err)
	}
	if !strings.Contains(out.Text(), "vdev has no access") {
		t.Errorf("404 text = %q, want has no access", out.Text())
	}
	// gh without a login: access unknown (pending + detail), never "has no access".
	x.e.gh.probe = GHResult{Exit: 4, Output: "To get started with GitHub CLI, please run:  gh auth login"}
	out = x.invite(setup.TeamInviteIn{Email: "w@agency.example", GitHub: "wdev"})
	if out.GitHubAccess != GitHubPending || out.GitHubDetail != ghNotLoggedIn {
		t.Errorf("no gh login: access %q detail %q", out.GitHubAccess, out.GitHubDetail)
	}
	if txt := out.Text(); strings.Contains(txt, "has no access") || !strings.Contains(txt, "could not check whether wdev") {
		t.Errorf("no gh login text = %q", txt)
	}
}

// TestGitHubRepo: the url shapes of an org store on github.com.
func TestGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:agency/lore-team.git":       "agency/lore-team",
		"git@github.com-work:agency/lore-team.git":  "agency/lore-team",
		"ssh://git@github.com/agency/lore-team.git": "agency/lore-team",
		"https://github.com/agency/lore-team":       "agency/lore-team",
		"https://github.com/agency/lore-team/":      "agency/lore-team",
		"git@gitlab.com:agency/lore-team.git":       "",
		"file:///srv/tmp/agency-team":               "",
		"https://github.com/agency":                 "",
		"https://github.com.evil.example/agency/x":  "",
		"/srv/tmp/agency-team":                      "",
		"git@github.com:agency/lore-team/extra.git": "",
	} {
		r, ok := GitHubRepo(in)
		got := ""
		if ok {
			got = r.Owner + "/" + r.Name
		}
		if got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRevokeListResend: revoke asks first, then the row is revoked; list
// shows the status as of today; resend of a revoked invite is refused.
func TestRevokeListResend(t *testing.T) {
	x := newFixture(t)
	a := x.invite(setup.TeamInviteIn{Email: "a@agency.example"})
	x.invite(setup.TeamInviteIn{Email: "b@agency.example"})
	_, err := x.ops().revoke(x.ctx, setup.TeamInviteRefIn{Ref: "a@agency.example"})
	wantCode(t, "unconfirmed revoke", err, errs.NeedsConfirm)
	out, err := x.ops().revoke(op.WithConfirmed(x.ctx, true), setup.TeamInviteRefIn{Ref: a.ID})
	if err != nil || out.Status != schema.InviteStatusRevoked {
		t.Fatalf("revoke = %+v %v", out, err)
	}
	_, err = x.ops().resend(x.ctx, setup.TeamInviteRefIn{Ref: a.ID})
	wantCode(t, "resend revoked", err, errs.InviteRevoked)
	_, err = x.ops().resend(x.ctx, setup.TeamInviteRefIn{Ref: "inv_01890a5d8a2b7000a000000000000fff"})
	wantCode(t, "resend unknown", err, errs.InviteNotFound)
	// b expires: list shows it expired without a write.
	x.e.Now = x.e.Now.AddDate(0, 0, 15)
	l, err := x.ops().list(x.ctx, setup.TeamInviteListIn{Status: schema.InviteStatusExpired})
	if err != nil || len(l.Invites) != 1 || l.Invites[0].Email != "b@agency.example" {
		t.Errorf("expired list = %+v %v", l, err)
	}
	_, err = x.ops().resend(x.ctx, setup.TeamInviteRefIn{Ref: "b@agency.example"})
	wantCode(t, "resend expired", err, errs.InviteExpired)
	// An expired invite no longer blocks a new one for the same person.
	x.invite(setup.TeamInviteIn{Email: "b@agency.example"})
	_, err = x.ops().list(x.ctx, setup.TeamInviteListIn{Status: "open"})
	wantCode(t, "bad status filter", err, errs.InvalidInput)
}

// joiner switches the fake's caller to a new person (as org.import's
// register would make them the writing actor) and returns their id.
func (x *fixture) joiner() ids.ID[ids.Actor] {
	id := testID[ids.Actor](x.t, ids.PrefixActor)
	x.e.CallerID = id
	return id
}

func (x *fixture) joinDeps() JoinDeps { return JoinDeps{Orgs: x.orgs, Entries: x.e, Profiles: x.e} }

// joinOn decodes the line and runs ApplyJoin as tomas.
func (x *fixture) join(line string, emails ...string) (RosterResult, error) {
	x.t.Helper()
	inv, err := onboard.DecodeInvite(line)
	if err != nil {
		x.t.Fatal(err)
	}
	return ApplyJoin(context.Background(), x.joinDeps(), JoinRequest{Invite: inv, Emails: emails, Name: "Tomas", Handle: "tomas", GitHubUser: "tomas-dev"})
}

// rowsOf are the live rows of table belonging to actor (field actor_id).
func (x *fixture) rowsOf(table string, actor ids.ID[ids.Actor]) []row.Row {
	var out []row.Row
	for _, r := range x.e.All(row.Table(table)) {
		if !r.Deleted && fieldText(r, "actor_id") == actor.String() {
			out = append(out, r)
		}
	}
	return out
}

// TestJoinWritesRosterRows: the 4.5 Apply rows, then a re-run is a no-op.
func TestJoinWritesRosterRows(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", GitHub: "tomas-dev", Roles: []string{"dev"}, Projects: []string{"acme-hub", "northwind"}, Type: schema.PeriodTypeContractor})
	delete(x.e.origins, "agency") // a fresh machine: no orgs[] entry yet
	tomas := x.joiner()
	res, err := x.join(out.Line, "tomas.home@mail.example", "Tomas@agency.example")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || len(x.orgs.imports) != 1 || x.orgs.imports[0].Key != "human:Tomas@agency.example" || x.orgs.imports[0].As != "agency" || x.orgs.imports[0].URL != storeURL {
		t.Errorf("import = %+v (imported %v)", x.orgs.imports, res.Imported)
	}
	contacts := x.rowsOf(schema.TableActorContacts, tomas)
	if len(contacts) != 3 {
		t.Fatalf("contacts = %d, want 2 emails + github", len(contacts))
	}
	for _, c := range contacts {
		norm, typ := fieldText(c, schema.FieldContactValueNorm), fieldText(c, schema.FieldContactType)
		wantPrimary := norm == "tomas@agency.example" || typ == "github"
		if fieldText(c, schema.FieldContactIdentity) != "true" || (fieldText(c, schema.FieldContactPrimary) == "true") != wantPrimary {
			t.Errorf("contact %s %s: identity %s primary %s", typ, norm, fieldText(c, schema.FieldContactIdentity), fieldText(c, schema.FieldContactPrimary))
		}
	}
	periods := x.rowsOf(schema.TableActorPeriods, tomas)
	if len(periods) != 1 || fieldText(periods[0], schema.FieldPeriodType) != schema.PeriodTypeContractor || fieldText(periods[0], schema.FieldPeriodStartedOn) != "2026-11-06" || fieldText(periods[0], schema.FieldPeriodEndedOn) != "" {
		t.Errorf("periods = %+v", periods)
	}
	members := x.rowsOf(schema.TableProjectMembers, tomas)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	for _, m := range members {
		if got := value.ItemStrings(m.Fields[schema.FieldMemberRoles]); !slices.Equal(got, []string{"dev"}) {
			t.Errorf("member roles = %v", got)
		}
	}
	inv := x.e.All(schema.TableInvites)[0]
	if fieldText(inv, schema.FieldInviteStatus) != schema.InviteStatusAccepted || fieldText(inv, schema.FieldInviteAcceptedBy) != tomas.String() {
		t.Errorf("invite after join: %s by %s", fieldText(inv, schema.FieldInviteStatus), fieldText(inv, schema.FieldInviteAcceptedBy))
	}
	// Run twice: same actor, nothing new written, no second import.
	before := len(x.e.All(schema.TableActorContacts)) + len(x.e.All(schema.TableActorPeriods)) + len(x.e.All(schema.TableProjectMembers))
	res, err = x.join(out.Line, "Tomas@agency.example")
	if err != nil || !res.Already || len(res.Wrote) != 0 || len(x.orgs.imports) != 1 {
		t.Errorf("re-run = %+v %v (imports %d)", res, err, len(x.orgs.imports))
	}
	after := len(x.e.All(schema.TableActorContacts)) + len(x.e.All(schema.TableActorPeriods)) + len(x.e.All(schema.TableProjectMembers))
	if after != before {
		t.Errorf("re-run wrote rows: %d -> %d", before, after)
	}
}

// TestJoinSingleUse (break-verify target): a second accept of the same
// invite by another actor is E_INVITE_USED and writes nothing for them.
func TestJoinSingleUse(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	x.joiner()
	x.e.ensureActor("human:tomas@agency.example", "tomas")
	if _, err := x.join(out.Line, "tomas@agency.example"); err != nil {
		t.Fatal(err)
	}
	other := x.joiner()
	x.e.ensureActor("human:tomas@agency.example", "tomas2")
	_, err := x.join(out.Line, "tomas@agency.example")
	wantCode(t, "second accept", err, errs.InviteUsed)
	if n := len(x.rowsOf(schema.TableActorPeriods, other)) + len(x.rowsOf(schema.TableProjectMembers, other)); n != 0 {
		t.Errorf("the second actor got %d rows", n)
	}
}

// TestJoinRefusals (break-verify target: expiry): expired, revoked, wrong
// email and a missing row, each before any roster row is written.
func TestJoinRefusals(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	tomas := x.joiner()
	x.e.ensureActor("human:tomas@agency.example", "tomas")

	x.e.Now = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC) // expires_on 2026-11-20 was yesterday
	_, err := x.join(out.Line, "tomas@agency.example")
	wantCode(t, "expired yesterday", err, errs.InviteExpired)
	x.e.Now = time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC) // the last day still accepts
	if _, err := x.join(out.Line, "mira@agency.example"); !errs.Has(err, errs.InviteEmail) {
		t.Errorf("wrong email: %v", err)
	}

	inv, _ := onboard.DecodeInvite(out.Line)
	inv.ID = "inv_01890a5d8a2b7000a000000000000fff"
	line, err := onboard.EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	_, err = x.join(line, "tomas@agency.example")
	wantCode(t, "unknown id", err, errs.InviteNotFound)

	x.e.CallerID = x.admin
	if _, err := x.ops().revoke(op.WithConfirmed(x.ctx, true), setup.TeamInviteRefIn{Ref: out.ID}); err != nil {
		t.Fatal(err)
	}
	x.e.CallerID = tomas
	_, err = x.join(out.Line, "tomas@agency.example")
	wantCode(t, "revoked", err, errs.InviteRevoked)
	if n := len(x.rowsOf(schema.TableActorPeriods, tomas)) + len(x.rowsOf(schema.TableActorContacts, tomas)); n != 0 {
		t.Errorf("refused joins wrote %d rows", n)
	}
}

// TestEditedLineGrantsNothing: a line edited to carry extra roles,
// projects and a type grants exactly what the row says.
func TestEditedLineGrantsNothing(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}, Type: schema.PeriodTypeIntern})
	inv, _ := onboard.DecodeInvite(out.Line)
	b, _ := json.Marshal(inv)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["roles"] = json.RawMessage(`["lead","pm"]`)
	m["projects"] = json.RawMessage(`["` + x.nw.String() + `"]`)
	m["period_type"] = json.RawMessage(`"employee"`)
	m["status"] = json.RawMessage(`"accepted"`)
	m["expires"] = json.RawMessage(`"2099-01-01"`)
	eb, _ := json.Marshal(m)
	edited := onboard.InvitePrefix + base64.RawURLEncoding.EncodeToString(eb)
	tomas := x.joiner()
	x.e.ensureActor("human:tomas@agency.example", "tomas")
	if _, err := x.join(edited, "tomas@agency.example"); err != nil {
		t.Fatal(err)
	}
	members := x.rowsOf(schema.TableProjectMembers, tomas)
	if len(members) != 1 || fieldText(members[0], schema.FieldMemberProject) != x.hub.String() ||
		!slices.Equal(value.ItemStrings(members[0].Fields[schema.FieldMemberRoles]), []string{"dev"}) {
		t.Errorf("edited line: members = %+v", members)
	}
	periods := x.rowsOf(schema.TableActorPeriods, tomas)
	if len(periods) != 1 || fieldText(periods[0], schema.FieldPeriodType) != schema.PeriodTypeIntern {
		t.Errorf("edited line: periods = %+v", periods)
	}
	// The edited expiry is not believed either.
	x2 := newFixture(t)
	o2 := x2.invite(setup.TeamInviteIn{Email: "tomas@agency.example"})
	i2, _ := onboard.DecodeInvite(o2.Line)
	i2.Expires = "2099-01-01"
	l2, _ := onboard.EncodeInvite(i2)
	x2.joiner()
	x2.e.ensureActor("human:tomas@agency.example", "tomas")
	x2.e.Now = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	_, err := x2.join(l2, "tomas@agency.example")
	wantCode(t, "edited expiry", err, errs.InviteExpired)
}

// TestJoinRejoin: an offboarded person invited again keeps their actor:
// the old stint is materialised, a new period opens, status is active.
func TestJoinRejoin(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	tomas := x.joiner()
	tv := func(s string) value.Value { return txt(t, s) }
	x.e.Put(row.Row{Table: schema.TableActors, ID: tomas.Any(), CreatedAt: time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Fields: map[string]value.Value{
			"kind": tv(schema.ActorKindHuman), "display_name": tv("Tomas"), "handle": tv("tomas"), "stable_key": tv("human:tomas@agency.example"),
			schema.FieldActorActive: value.BoolOf(false), schema.FieldActorStatus: tv(schema.ActorStatusLeft),
			schema.FieldActorLeftAt: value.DateTimeFrom(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))}})
	res, err := x.join(out.Line, "tomas@agency.example")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rejoined || res.Actor != tomas.String() {
		t.Errorf("rejoin result = %+v", res)
	}
	a := x.e.All(schema.TableActors)
	i := slices.IndexFunc(a, func(r row.Row) bool { return r.ID == tomas.Any() })
	if fieldText(a[i], schema.FieldActorStatus) != schema.ActorStatusActive || fieldText(a[i], schema.FieldActorActive) != "true" || fieldText(a[i], schema.FieldActorLeftAt) != "" {
		t.Errorf("actor after rejoin: %v", a[i].Fields)
	}
	var spans []string
	for _, p := range x.rowsOf(schema.TableActorPeriods, tomas) {
		spans = append(spans, fieldText(p, schema.FieldPeriodStartedOn)+".."+fieldText(p, schema.FieldPeriodEndedOn))
	}
	slices.Sort(spans)
	if !slices.Equal(spans, []string{"2025-01-10..2026-03-01", "2026-11-06.."}) {
		t.Errorf("periods = %v", spans)
	}
}

// TestJoinContactTaken: an identity email held by another actor is
// E_CONTACT_TAKEN, never a second identity row.
func TestJoinContactTaken(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example"})
	ref, _ := value.RefOf(x.admin.Any())
	x.e.Put(row.Row{Table: schema.TableActorContacts, ID: testID[ids.ActorContact](t, ids.PrefixActorContact).Any(), Fields: map[string]value.Value{
		schema.FieldContactActor: ref, schema.FieldContactType: txt(t, "email"), schema.FieldContactValue: txt(t, "tomas@agency.example"),
		schema.FieldContactValueNorm: txt(t, "tomas@agency.example"), schema.FieldContactIdentity: value.BoolOf(true)}})
	x.joiner()
	x.e.ensureActor("human:tomas@agency.example", "tomas")
	_, err := x.join(out.Line, "tomas@agency.example")
	wantCode(t, "taken", err, errs.ContactTaken)
}

// TestJoinOrgEntryMismatch: an orgs[] entry of the invite's name on
// another store is refused, never re-pointed.
func TestJoinOrgEntryMismatch(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example"})
	x.e.origins["agency"] = Origin{Store: "git@github.com:other/team.git"}
	x.joiner()
	_, err := x.join(out.Line, "tomas@agency.example")
	wantCode(t, "mismatch", err, errs.SetupOrgMismatch)
	if len(x.orgs.imports) != 0 {
		t.Error("imported over a mismatching entry")
	}
}

// TestApplyAdmin: store.init, org.create, the admin's contacts and an
// open employee period; a re-run skips init/create and writes nothing.
func TestApplyAdmin(t *testing.T) {
	x := newFixture(t)
	erman := x.joiner()
	st := &fakeStores{}
	d := AdminDeps{Stores: st, Orgs: x.orgs, Entries: x.e, Profiles: x.e}
	req := AdminRequest{Org: "acme", StoreURL: "git@github.com:acme/lore-team.git", Own: []string{"github.com/acme/*"},
		Emails: []string{"erman@acme.example"}, Name: "Erman", Handle: "erman", GitHubUser: "erman-gh"}
	res, err := ApplyAdmin(context.Background(), d, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || len(st.inits) != 1 || st.inits[0].Name != "acme" || len(x.orgs.creates) != 1 {
		t.Fatalf("init/create = %+v / %+v", st.inits, x.orgs.creates)
	}
	c := x.orgs.creates[0]
	if c.Key != "human:erman@acme.example" || c.OrgFolder != orgcmd.DefaultOrgFolder || c.Store != req.StoreURL || !slices.Equal(c.Own, req.Own) {
		t.Errorf("create = %+v", c)
	}
	if n := len(x.rowsOf(schema.TableActorContacts, erman)); n != 2 {
		t.Errorf("contacts = %d, want email + github", n)
	}
	ps := x.rowsOf(schema.TableActorPeriods, erman)
	if len(ps) != 1 || fieldText(ps[0], schema.FieldPeriodType) != schema.PeriodTypeEmployee || fieldText(ps[0], schema.FieldPeriodEndedOn) != "" {
		t.Errorf("periods = %+v", ps)
	}
	res, err = ApplyAdmin(context.Background(), d, req)
	if err != nil || res.Created || len(res.Wrote) != 0 || len(st.inits) != 1 {
		t.Errorf("re-run = %+v %v (inits %d)", res, err, len(st.inits))
	}
	req.StoreURL = "git@github.com:acme/other.git"
	_, err = ApplyAdmin(context.Background(), d, req)
	wantCode(t, "name taken by another store", err, errs.SetupOrgMismatch)
	if !strings.Contains(fmt.Sprint(err), "pick another name") {
		t.Errorf("message %v lacks the 4.6 text", err)
	}
}

// TestJoinStintBoundary (break-verify target, TP-12): invite accept
// starts a membership today, so an ended stint of the same project that
// covers today (its last day, or a later end) or starts later (planned
// with a future --on, then ended) is E_PERIOD_OVERLAP naming the day
// after, and the refused accept writes nothing (no contact,
// period, membership, invite still pending). The day after the old
// stint's last day starts a new stint; an open stint only gains roles;
// a trashed stint does not count.
func TestJoinStintBoundary(t *testing.T) {
	cases := []struct {
		name     string
		start    string
		end      string // "" = open
		deleted  bool
		wantHint string // "" = accepted
		wantRows int    // memberships of acme-hub after an accepted join
	}{
		{name: "ended today (its last day)", start: "2026-01-12", end: "2026-11-06",
			wantHint: "accept it again tomorrow (2026-11-07): the old stint's last day is 2026-11-06"},
		{name: "one-day stint today", start: "2026-11-06", end: "2026-11-06",
			wantHint: "accept it again tomorrow (2026-11-07): the old stint's last day is 2026-11-06"},
		{name: "ends tomorrow", start: "2026-01-12", end: "2026-11-07",
			wantHint: "accept it again on 2026-11-08: the old stint's last day is 2026-11-07"},
		{name: "ended yesterday", start: "2026-01-12", end: "2026-11-05", wantRows: 2},
		{name: "open stint", start: "2026-01-12", wantRows: 1},
		{name: "trashed stint ended today", start: "2026-01-12", end: "2026-11-06", deleted: true, wantRows: 1},
		// A stint planned for later (member add --on <future>, then ended):
		// the open stint the accept starts today would run through it.
		{name: "ended stint starting tomorrow", start: "2026-11-07", end: "2026-12-31",
			wantHint: "accept it again on 2027-01-01, the day after the stint 2026-11-07 → 2026-12-31"},
		{name: "ended stint starting next month", start: "2026-12-01", end: "2027-01-31",
			wantHint: "accept it again on 2027-02-01, the day after the stint 2026-12-01 → 2027-01-31"},
		{name: "trashed stint starting later", start: "2026-12-01", end: "2027-01-31", deleted: true, wantRows: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := newFixture(t)
			out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
			tomas := x.joiner()
			x.e.ensureActor("human:tomas@agency.example", "tomas")
			pref, _ := value.RefOf(x.hub.Any())
			aref, _ := value.RefOf(tomas.Any())
			start, _ := value.DateOf(c.start)
			roles, err := textList("roles", []string{"qa"})
			if err != nil {
				t.Fatal(err)
			}
			f := map[string]value.Value{schema.FieldMemberProject: pref, schema.FieldMemberActor: aref,
				schema.FieldMemberRoles: roles, schema.FieldMemberStartedOn: start}
			if c.end != "" {
				end, _ := value.DateOf(c.end)
				f[schema.FieldMemberEndedOn] = end
			}
			x.e.Put(row.Row{Table: schema.TableProjectMembers, ID: testID[ids.ProjectMember](t, ids.PrefixProjectMember).Any(), Deleted: c.deleted, Fields: f})
			count := func() int {
				return len(x.e.All(schema.TableActorContacts)) + len(x.e.All(schema.TableActorPeriods)) + len(x.e.All(schema.TableProjectMembers))
			}
			before := count()
			_, err = x.join(out.Line, "tomas@agency.example")
			if c.wantHint != "" {
				wantCode(t, c.name, err, errs.PeriodOverlap)
				if e, ok := err.(*errs.Error); !ok || e.Hint != c.wantHint {
					t.Errorf("hint: %v", err)
				}
				if n := count(); n != before {
					t.Errorf("a refused accept wrote rows: %d -> %d", before, n)
				}
				if st := fieldText(x.e.All(schema.TableInvites)[0], schema.FieldInviteStatus); st != schema.InviteStatusPending {
					t.Errorf("invite after a refused accept: %s", st)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := len(x.rowsOf(schema.TableProjectMembers, tomas)); n != c.wantRows {
				t.Errorf("memberships = %d, want %d", n, c.wantRows)
			}
		})
	}
}

// TestJoinMemberRefusedFirst (owner item 19, break-verify target): a
// person not on the roster joining by the store url is refused before
// anything is written (no actor row, no push, the org entry taken out
// again); a person member add added joins on their own actor.
func TestJoinMemberRefusedFirst(t *testing.T) {
	x := newFixture(t)
	delete(x.e.origins, "agency") // a fresh machine: no orgs[] entry yet
	if _, err := x.ops().staffAdd(op.WithConfirmed(x.ctx, true), StaffAddIn{Email: "ann@agency.example", Name: "Ann"}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		n := 0
		for _, tb := range []string{schema.TableActors, schema.TableActorContacts, schema.TableActorPeriods, schema.TableProjectMembers} {
			n += len(x.e.All(row.Table(tb)))
		}
		return n
	}
	rows, pushes := count(), x.e.Pushes
	x.joiner()
	_, err := ApplyJoinMember(context.Background(), x.joinDeps(), MemberJoinRequest{Org: "agency", Store: storeURL, Emails: []string{"dan@agency.example"}, Name: "Dan", Handle: "dan"})
	wantCode(t, "non-member join by url", err, errs.NotFound)
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "is not a member of agency") || !strings.Contains(e.Hint, "lore member add dan@agency.example") {
		t.Errorf("refusal = %v, want the cause and the admin's command", err)
	}
	if got := count(); got != rows || x.e.Pushes != pushes {
		t.Errorf("a refused join wrote: rows %d -> %d, pushes %d -> %d", rows, got, pushes, x.e.Pushes)
	}
	if _, ok := x.e.origins["agency"]; ok || len(x.orgs.imports) != 1 {
		t.Errorf("the refused join left its org entry (imports %d)", len(x.orgs.imports))
	}
	// the member joins: their own actor, the entry kept, no new actor row
	actors := len(x.e.All(schema.TableActors))
	x.joiner()
	res, err := ApplyJoinMember(context.Background(), x.joinDeps(), MemberJoinRequest{Org: "agency", Store: storeURL, Emails: []string{"Ann@agency.example"}, Name: "Ann", Handle: "ann"})
	if err != nil {
		t.Fatal(err)
	}
	ann, aerr := func() (row.Row, error) {
		r, done, err := x.e.Roster(context.Background(), false)
		defer done()
		if err != nil {
			return row.Row{}, err
		}
		return r.ResolveActor(context.Background(), "human:ann@agency.example")
	}()
	if aerr != nil || res.Actor != ann.ID.String() || !res.Imported || len(x.e.All(schema.TableActors)) != actors {
		t.Errorf("member join = %+v (ann %v %v, actors %d -> %d)", res, ann.ID, aerr, actors, len(x.e.All(schema.TableActors)))
	}
	if _, ok := x.e.origins["agency"]; !ok {
		t.Error("the member join kept no org entry")
	}
}

// TestJoinInviteRefusedFirst (owner item 19, break-verify target): on a
// fresh machine a bad invite (unknown, expired, wrong email, used by
// someone else, revoked) is refused before anyone is registered: no
// actor row, no push, the org entry taken out again; a good one joins.
func TestJoinInviteRefusedFirst(t *testing.T) {
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency") // a fresh machine: no orgs[] entry yet
	count := func() int {
		n := 0
		for _, tb := range []string{schema.TableActors, schema.TableActorContacts, schema.TableActorPeriods, schema.TableProjectMembers} {
			n += len(x.e.All(row.Table(tb)))
		}
		return n
	}
	rows, pushes := count(), x.e.Pushes
	refused := func(name, line string, code errs.Code, emails ...string) {
		t.Helper()
		x.joiner()
		_, err := x.join(line, emails...)
		wantCode(t, name, err, code)
		if got := count(); got != rows || x.e.Pushes != pushes {
			t.Errorf("%s wrote: rows %d -> %d, pushes %d -> %d", name, rows, got, pushes, x.e.Pushes)
		}
		if _, ok := x.e.origins["agency"]; ok {
			t.Errorf("%s left its org entry", name)
		}
	}
	inv, _ := onboard.DecodeInvite(out.Line)
	inv.ID = "inv_01890a5d8a2b7000a000000000000fff"
	unknown, err := onboard.EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	refused("unknown id", unknown, errs.InviteNotFound, "tomas@agency.example")
	x.e.Now = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC) // expires_on 2026-11-20 was yesterday
	refused("expired", out.Line, errs.InviteExpired, "tomas@agency.example")
	x.e.Now = time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)
	// the row says another address than the (edited) line
	inv, _ = onboard.DecodeInvite(out.Line)
	inv.Email = "tomas.alt@agency.example"
	edited, err := onboard.EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	refused("wrong email", edited, errs.InviteEmail, "tomas.alt@agency.example")
	// used by an actor that is not this person
	invRow := x.e.All(schema.TableInvites)[0]
	used := invRow
	used.Fields = maps.Clone(invRow.Fields)
	used.Fields[schema.FieldInviteStatus] = txt(t, schema.InviteStatusAccepted)
	used.Fields[schema.FieldInviteAcceptedBy] = txt(t, testID[ids.Actor](t, ids.PrefixActor).String())
	x.e.Put(used)
	refused("used", out.Line, errs.InviteUsed, "tomas@agency.example")
	x.e.Put(invRow)
	x.e.CallerID = x.admin
	if _, err := x.ops().revoke(op.WithConfirmed(x.ctx, true), setup.TeamInviteRefIn{Ref: out.ID}); err != nil {
		t.Fatal(err)
	}
	rows, pushes = count(), x.e.Pushes
	refused("revoked", out.Line, errs.InviteRevoked, "tomas@agency.example")
	// a good invite still joins on the fresh machine
	x.e.CallerID, x.e.origins["agency"] = x.admin, Origin{Store: storeURL, OrgFolder: "org"}
	good := x.invite(setup.TeamInviteIn{Email: "ana@agency.example", Roles: []string{"dev"}, Projects: []string{"acme-hub"}})
	delete(x.e.origins, "agency")
	ana := x.joiner()
	if res, err := x.join(good.Line, "ana@agency.example"); err != nil || res.Actor != ana.String() || !res.Imported {
		t.Errorf("good invite = %+v %v", res, err)
	}
}

// TestStaffAddPrintsStoreURLJoin (B2-1): member add tells the admin the
// command a new member runs on a fresh machine, which has no org entry,
// so it names the org's store url (credentials stripped), never the org's
// name alone.
func TestStaffAddPrintsStoreURLJoin(t *testing.T) {
	x := newFixture(t)
	out, err := x.ops().staffAdd(op.WithConfirmed(x.ctx, true), StaffAddIn{Email: "ann@agency.example", Name: "Ann"})
	if err != nil {
		t.Fatal(err)
	}
	want := "lore setup join " + storeURL
	if out.JoinWith != want || !strings.Contains(out.Text(), "joins on their machine with: "+want+" ") {
		t.Fatalf("join line = %q\n%s", out.JoinWith, out.Text())
	}
	if strings.Contains(out.Text(), "<org>") {
		t.Fatalf("member add still prints the org placeholder:\n%s", out.Text())
	}
	// a made-up password in the store url never reaches the printed line
	pw := "not-a-real-pw"
	x.e.origins["agency"] = Origin{Store: "https://bob:" + pw + "@git.example/agency/lore-team.git"}
	out, err = x.ops().staffAdd(op.WithConfirmed(x.ctx, true), StaffAddIn{Email: "cat@agency.example", Name: "Cat"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Text(), pw) || !strings.Contains(out.JoinWith, "git.example/agency/lore-team.git") {
		t.Fatalf("join line with a credential url = %q", out.JoinWith)
	}
	delete(x.e.origins, "agency")
	out, err = x.ops().staffAdd(op.WithConfirmed(x.ctx, true), StaffAddIn{Email: "dee@agency.example", Name: "Dee"})
	if err != nil {
		t.Fatal(err)
	}
	if out.JoinWith != joinPlaceholder {
		t.Fatalf("join line without an org entry = %q", out.JoinWith)
	}
}

// TestStaffAddRestoresSameDay (B2-14): a member removed by mistake today
// comes back with member add the same day: the stint that ended today is
// reopened (no new stint overlapping it, which was E_PERIOD_OVERLAP), the
// actor is active again under its roster name, and the output says so.
func TestStaffAddRestoresSameDay(t *testing.T) {
	x := newFixture(t)
	ctx := op.WithConfirmed(x.ctx, true)
	out, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "ann@agency.example", Name: "Ann Lee"})
	if err != nil {
		t.Fatal(err)
	}
	ann, err := ids.Parse[ids.Actor](out.Added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// what offboard writes today: the stint ends today, the actor is left
	ps := x.rowsOf(schema.TableActorPeriods, ann)
	if len(ps) != 1 {
		t.Fatalf("periods after add: %v", ps)
	}
	end, err := value.DateOf("2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	p := ps[0]
	p.Fields[schema.FieldPeriodEndedOn], p.Fields[schema.FieldPeriodEndReason] = end, txt(t, "removed by mistake")
	x.e.Put(p)
	for _, a := range x.e.All(schema.TableActors) {
		if a.ID == ann.Any() {
			a.Fields[schema.FieldActorStatus], a.Fields[schema.FieldActorActive] = txt(t, schema.ActorStatusLeft), value.BoolOf(false)
			x.e.Put(a)
		}
	}
	out, err = x.ops().staffAdd(ctx, StaffAddIn{Email: "ann@agency.example"})
	if err != nil {
		t.Fatalf("same-day restore: %v", err)
	}
	if len(out.Added) != 1 || out.Added[0].Name != "Ann Lee" || !strings.Contains(out.Text(), "was left; back on the roster") {
		t.Fatalf("restore output:\n%s", out.Text())
	}
	ps = x.rowsOf(schema.TableActorPeriods, ann)
	if len(ps) != 1 || fieldText(ps[0], schema.FieldPeriodEndedOn) != "" || fieldText(ps[0], schema.FieldPeriodEndReason) != "" {
		t.Fatalf("the stint was not reopened: %v", ps)
	}
	for _, a := range x.e.All(schema.TableActors) {
		if a.ID == ann.Any() && fieldText(a, schema.FieldActorStatus) != schema.ActorStatusActive {
			t.Errorf("actor after restore: %v", a.Fields)
		}
	}
}
