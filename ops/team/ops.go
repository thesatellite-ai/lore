// Package team is the invite nouns of guided setup (ONBOARDING_DESIGN 1.6,
// 4.5, 4.6, 7.1): team.invite, team.invite.list, team.invite.revoke and
// team.invite.resend, plus the join and admin writes the setup Applier
// calls (rosterapply.go).
//
// Every invite lives as an `invites` row in the org roster store (4.5,
// NeverClient, Synced), read and written through profilestore.Store, the
// same seam the team profile ops use (TEAM_PROFILES 2.0), so the writes go
// through the roster App's engine (scanned, stamped, audited) and are
// pushed at once. The line handed to the person (logic/onboard
// EncodeInvite) holds no secret and grants nothing; roles, projects and
// the period type are read from the row on accept.
//
//	team.invite  ──▶ admin setup? ──▶ email, type, roles, projects, expiry
//	             ──▶ --grant? confirmed? (outward, E_NEEDS_CONFIRM)
//	             ──▶ invites row (pending, expires_on today+14d) ──▶ Push
//	             ──▶ line (EncodeInvite) + invite.github_access (gh probe / grant)
//	team.invite.list   ──▶ rows, status as read today (pending past its day = expired)
//	team.invite.revoke ──▶ confirm ──▶ status revoked ──▶ Push
//	team.invite.resend ──▶ the same line again; writes nothing
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
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
)

// Env is what the team ops need; *wire.App implements it. It may also
// implement member.ProjectFinder (an optional upgrade, used to resolve a
// --project name across every store the App reads).
type Env interface {
	// Store opens the org roster store (TS-1); invites rows live there.
	profilestore.Store
	// SetupStore gives the active setup (ONB 3.3): the team ops run for
	// admin setups only (7.1 page allowlist column).
	SetupStore(ctx context.Context) (setup.Store, error)
	// TeamOrigin is the store url and org folder of the orgs[] entry org,
	// for the invite line (4.5). No entry is E_NOT_FOUND.
	TeamOrigin(ctx context.Context, org string) (Origin, error)
	// TeamGitHub is the gh runner of invite.github_access and --grant
	// (4.6); E_UNSUPPORTED when the binary has none.
	TeamGitHub(ctx context.Context) (GitHubRunner, error)
}

// Origin is where an org's roster lives: the "store" and "org_folder" of
// the invite line.
type Origin struct {
	Store     string
	OrgFolder string
}

// Ops returns the team invite operations bound to env (ONB 7.1).
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	base := []errs.Code{errs.Refused, errs.LeakBlocked, errs.SetupNotFound, errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Internal}
	writeErrs := append([]errs.Code{errs.RoleUnknown, errs.ProjectNameCollision, errs.NeedsConfirm, errs.PolicyDenied}, base...)
	refErrs := append([]errs.Code{errs.InviteNotFound, errs.NeedsConfirm, errs.PolicyDenied}, base...)
	resendErrs := append([]errs.Code{errs.InviteNotFound, errs.InviteExpired, errs.InviteRevoked, errs.InviteUsed}, base...)
	asking := func(s op.Spec) op.Spec {
		// MayAsk: --grant and revoke answer E_NEEDS_CONFIRM until the
		// caller confirms (ONB 4.6 "only after an explicit confirm",
		// 7.1 "revoke … (confirm)").
		s.Confirm.MayAsk = true
		return s
	}
	return append([]op.Entry{
		op.Define(asking(op.Spec{Name: setup.OpTeamInvite, Summary: "Invite an outsider (a contractor on a personal email) with a one-time line to paste; for your own staff use lore member add",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "invite a developer to one project", CLI: "lore team invite tomas@agency.example --github tomas-dev --role dev --project acme-hub", Want: "lore setup join lore-invite:v1:"}}}),
			func(ctx context.Context, in setup.TeamInviteIn) (setup.TeamInviteOut, error) {
				return o.invite(ctx, in)
			}),
		op.Define(op.Spec{Name: setup.OpTeamInviteList, Summary: "List the org's invites (a pending invite past its last day shows as expired)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: base,
			Examples: []op.Example{{Title: "pending invites", CLI: "lore team invite list --status pending", Want: "inv_"}}},
			func(ctx context.Context, in setup.TeamInviteListIn) (setup.TeamInviteListOut, error) {
				return o.list(ctx, in)
			}),
		op.Define(asking(op.Spec{Name: setup.OpTeamInviteRevoke, Summary: "Revoke a pending invite (asks first)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: refErrs,
			Examples: []op.Example{{Title: "revoke by email", CLI: "lore team invite revoke tomas@agency.example --yes", Want: "revoked"}}}),
			func(ctx context.Context, in setup.TeamInviteRefIn) (setup.TeamInviteRevokeOut, error) {
				return o.revoke(ctx, in)
			}),
		op.Define(op.Spec{Name: setup.OpTeamInviteResend, Summary: "Print a pending invite's line again (changes nothing)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: resendErrs,
			Examples: []op.Example{{Title: "resend by email", CLI: "lore team invite resend tomas@agency.example", Want: "lore-invite:v1:"}}},
			func(ctx context.Context, in setup.TeamInviteRefIn) (setup.TeamInviteResendOut, error) {
				return o.resend(ctx, in)
			}),
	}, o.staffOps(base, writeErrs)...)
}

// ops binds the op bodies to env.
type ops struct{ env Env }

// adminSetup is the active setup, which must be an admin setup with an org
// (ONB 7.1: team ops are for admin setups). No active setup or another
// kind is E_REFUSED naming the next step.
func adminSetup(ctx context.Context, env Env) (onboard.SetupEntry, error) {
	st, err := env.SetupStore(ctx)
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	act, err := st.Active(ctx, op.SetupOf(ctx))
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	if act.Name == "" || act.Why == setup.WhyNone {
		return onboard.SetupEntry{}, errs.New(errs.Refused, "team invites are written from an admin setup, and no setup is active here").
			WithHint("lore setup list, then run with --setup <an admin setup>, or start a team with lore setup")
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return onboard.SetupEntry{}, err
	}
	e, ok := cfg.Setup(act.Name)
	if !ok {
		return onboard.SetupEntry{}, errs.New(errs.SetupNotFound, "no setup named "+act.Name+"; lore setup list shows the setups on this machine")
	}
	if e.Kind != onboard.KindAdmin || e.Org == "" {
		return onboard.SetupEntry{}, errs.New(errs.Refused, "setup "+e.Name+" is a "+string(e.Kind)+" setup; team invites are written from an admin setup of the org").
			WithHint("run with --setup <an admin setup>, or ask the org admin to invite")
	}
	return e, nil
}

// Field values and limits of the invite ops.
const (
	// daySuffix ends an --expires lifetime ("14d").
	daySuffix = "d"
	// maxInviteDays bounds --expires: an invite is a short-lived pointer
	// (Q8); a longer wait is a new invite.
	maxInviteDays = 90
	// listSep separates several values in one --role / --project.
	listSep = ","
	// humanKeyPrefix is the stable key form of a person (human:<email>).
	humanKeyPrefix = "human:"
	// fieldStableKey and fieldProjectName are columns schema keeps as
	// literals.
	fieldStableKey   = "stable_key"
	fieldProjectName = "name"
)

// GitHub access states of TeamInviteOut.GitHubAccess (contract).
const (
	// GitHubGranted: the user can already push, or --grant just gave access.
	GitHubGranted = "granted"
	// GitHubPending: no access yet (or it could not be checked); the
	// output names the gh command.
	GitHubPending = "pending"
)

// splitList splits repeated, comma-joined values, trimmed, empty and
// duplicate entries dropped, first-seen order kept.
func splitList(in []string) []string {
	var out []string
	for _, v := range in {
		for _, p := range strings.Split(v, listSep) {
			p = strings.TrimSpace(p)
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseExpires reads --expires: "<n>d" (1..maxInviteDays), default
// schema.InviteDefaultDays. It returns the last day the invite accepts.
func parseExpires(s string, today time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	days := schema.InviteDefaultDays
	if s != "" {
		n, ok := dayCount(s)
		if !ok || n < 1 || n > maxInviteDays {
			return time.Time{}, errs.New(errs.InvalidInput, "--expires "+work.Quote(s)+" is not a lifetime of 1 to 90 days").WithHint("--expires 14d")
		}
		days = n
	}
	return teamprofile.Day(today).AddDate(0, 0, days), nil
}

// dayCount parses "<digits>d".
func dayCount(s string) (int, bool) {
	num, ok := strings.CutSuffix(s, daySuffix)
	if !ok || num == "" || len(num) > 3 {
		return 0, false
	}
	n := 0
	for _, c := range num {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// fieldText is a field's text, "" when absent or null.
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// textValue is s as a text value.
func textValue(field, s string) (value.Text, error) {
	t, err := value.TextOf(s)
	if err != nil {
		return value.Text{}, errs.Wrap(errs.InvalidInput, field+" "+work.Quote(s)+" is not valid text", err)
	}
	return t, nil
}

// textList is items as a list-of-text value.
func textList(field string, items []string) (value.List, error) {
	vs := make([]value.Value, 0, len(items))
	for _, s := range items {
		t, err := textValue(field, s)
		if err != nil {
			return value.List{}, err
		}
		vs = append(vs, t)
	}
	l, err := value.ListOf(value.KindText, vs...)
	if err != nil {
		return value.List{}, errs.Wrap(errs.InvalidInput, field, err)
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

// refValue is id as a ref value.
func refValue(id ids.AnyID) (value.Ref, error) {
	r, err := value.RefOf(id)
	if err != nil {
		return value.Ref{}, errs.Wrap(errs.Internal, "id "+id.String()+" is not a valid ref", err)
	}
	return r, nil
}

// actorEmail is the email of actor's stable key (human:<email>), "" for
// another form.
func actorEmail(a row.Row) string {
	email, ok := strings.CutPrefix(fieldText(a, fieldStableKey), humanKeyPrefix)
	if !ok {
		return ""
	}
	return email
}
