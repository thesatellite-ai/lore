package setup

// join.go — setup.join (ONBOARDING_DESIGN 4.5, 7.1): parse the invite
// line and answer the member plan plus a WebStart; cmd/lore opens the
// page on it. Nothing is written here: the roster writes (org.import,
// contacts, period, memberships, invite accepted, setups[] entry) are the
// join step's Apply (ApplyJoin, the wire Applier), after its checks pass.
// The line holds no secret and grants nothing, so parsing it is safe.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// invitePrefixAny starts every invite line, any version (a v2 line is
// still an invite, refused by its decoder, never an org name).
const invitePrefixAny = "lore-invite:"

// OrgFinder is the optional Onboard upgrade that says whether an org
// entry of this name exists on the machine (wire.App, orgs[] in
// ~/.lore/config.yaml).
type OrgFinder interface {
	HasOrg(ctx context.Context, org string) bool
}

// knownOrg reports whether org names an org entry here.
func (e engine) knownOrg(ctx context.Context, org string) bool {
	f, ok := upgrade.As[OrgFinder](e.env)
	return ok && onboard.ValidSetupName(org) && f.HasOrg(ctx, org)
}

// storeURLish reports whether s reads as a git store url (scp form
// git@host:path, or scheme://…).
func storeURLish(s string) bool {
	return strings.Contains(s, "://") || (strings.Contains(s, "@") && strings.Contains(s, ":") && !strings.ContainsAny(s, " \t"))
}

// quoteShort is s quoted, cut to 60 bytes.
func quoteShort(s string) string {
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return "\"" + s + "\""
}

// UnknownOrgErr is the refusal of `lore setup join <org>` when this machine
// has no org entry of that name (E_JOIN_ORG_UNKNOWN). A member an admin
// added joins from a fresh machine, where only the org's store url can be
// resolved; setup.join and the join step's apply both answer with it, so
// the page and the command say the same thing.
func UnknownOrgErr(org string) error {
	return errs.New(errs.JoinOrgUnknown, "setup join: no org named "+quoteShort(org)+" on this machine yet; join with the org's store url the admin gave you").
		WithHint("lore setup join <the org's store url> (lore member add printed it for your admin); an outsider: paste the whole line starting with " + InvitePrefix)
}

// Join is setup.join. The setup name is the global --setup, else the
// invite's org when it is a valid setup name (1.5: setup acme-hub for org
// acme-hub), else "" (the page asks for it in the kind step).
func (e engine) Join(ctx context.Context, in JoinIn) (JoinOut, error) {
	text := strings.TrimSpace(in.Invite)
	if text != "" && !strings.Contains(text, invitePrefixAny) {
		// a member an admin added (`lore member add`) joins by the org's
		// name or store url, no invite line: the join step finds them on
		// the roster by their email (team.ApplyJoinMember)
		if !storeURLish(text) && !e.knownOrg(ctx, text) {
			if onboard.ValidSetupName(text) {
				// an org name this machine has no entry for: the common case
				// of a new member on a fresh laptop, never an invite problem
				return JoinOut{}, UnknownOrgErr(text)
			}
			return JoinOut{}, errs.New(errs.InviteInvalid, "setup join: "+quoteShort(text)+" is not an invite line, an org on this machine, or a store url").
				WithHint("an admin added you (lore member add): lore setup join <the org's store url>; an outsider: paste the whole line starting with " + InvitePrefix)
		}
		return e.joinMember(ctx, text, in.NoOpen)
	}
	inv, err := onboard.DecodeInvite(in.Invite)
	if err != nil {
		return JoinOut{}, err
	}
	name := op.SetupOf(ctx)
	if name == "" && onboard.ValidSetupName(inv.Org) {
		name = inv.Org
	}
	plan, err := e.Plan(ctx, PlanIn{Kind: string(onboard.KindMember), Setup: name})
	if err != nil {
		return JoinOut{}, err
	}
	if i := strings.Index(text, InvitePrefix); i > 0 {
		text = text[i:] // drop a pasted "lore setup join " in front
	}
	return JoinOut{
		Web:    WebStart{Setup: name, NoOpen: in.NoOpen, Invite: text},
		Invite: Invite(inv),
		Plan:   plan,
	}, nil
}

// joinMember is setup.join for a member: the page opens on the join step
// with the org (or store url) as its answer.
func (e engine) joinMember(ctx context.Context, org string, noOpen bool) (JoinOut, error) {
	name := op.SetupOf(ctx)
	if name == "" && onboard.ValidSetupName(org) {
		name = org
	}
	plan, err := e.Plan(ctx, PlanIn{Kind: string(onboard.KindMember), Setup: name})
	if err != nil {
		return JoinOut{}, err
	}
	inv := Invite{Store: org}
	if !storeURLish(org) {
		inv = Invite{Org: org}
	}
	return JoinOut{Web: WebStart{Setup: name, NoOpen: noOpen, Invite: org}, Invite: inv, Plan: plan}, nil
}
