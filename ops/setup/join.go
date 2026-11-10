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
	"github.com/thesatellite-ai/lore/op"
)

// Join is setup.join. The setup name is the global --setup, else the
// invite's org when it is a valid setup name (1.5: setup acme-hub for org
// acme-hub), else "" (the page asks for it in the kind step).
func (e engine) Join(ctx context.Context, in JoinIn) (JoinOut, error) {
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
	text := strings.TrimSpace(in.Invite)
	if i := strings.Index(text, InvitePrefix); i > 0 {
		text = text[i:] // drop a pasted "lore setup join " in front
	}
	return JoinOut{
		Web:    WebStart{Setup: name, NoOpen: in.NoOpen, Invite: text},
		Invite: Invite(inv),
		Plan:   plan,
	}, nil
}
