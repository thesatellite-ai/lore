// roster_op.go — the old binary's opsinfra.Roster (wire.Options.Roster):
// `lore actor register|deactivate` are core operations (ops/infra
// roster.go, P3-AUDIT-5); this is the host half that still needs the old
// modules: org resolution (gatherOrgSituation), the org's 0.1.18 roster
// through the core (roster_core.go, store-only outside a repo, BUG-T3),
// the repo's own roster with no org (Q4, written live, Q12) and a v0.1.17
// org store through ent (roster_cmd.go). Output lines are rendered by the
// operation from the returned values.
package main

import (
	"context"

	entActor "dbent/gen/ent/actor"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/rowroute"

	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// cliRoster implements opsinfra.Roster for this binary.
type cliRoster struct{}

var _ opsinfra.Roster = cliRoster{}

// ActorRegister implements opsinfra.Roster. An agent identity never
// registers (F18: its owner runs `lore agent start`).
func (cliRoster) ActorRegister(ctx context.Context, in opsinfra.ActorRegisterIn) (out opsinfra.ActorRegisterOut, err error) {
	defer func() { err = rosterErr(err) }()
	id, err := resolveIdentityHere(ctx)
	if err != nil {
		return out, err
	}
	if id.Kind == identityKindAgent {
		return out, errcodes.New(errcodes.InvalidInput, "an agent cannot register actors; its owner runs `lore agent start --label <l> --owner @<owner>`")
	}
	if in.Kind == opsinfra.RegisterKindAgent {
		aid, where, rerr := rosterRegisterAgent(ctx, in.Label, in.Owner)
		if rerr != nil {
			return out, rerr
		}
		return opsinfra.ActorRegisterOut{ID: aid, Kind: opsinfra.RegisterKindAgent, Name: in.Label, Label: in.Label, Created: true, Where: redactIfQuiet(where)}, nil
	}
	cr, err := openRosterOnCore(ctx)
	if err != nil {
		return out, err
	}
	if cr != nil {
		return cr.registerHuman(ctx, id.StableKey, id.DisplayName, id.Handle)
	}
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return out, err
	}
	defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
	if r.org == nil {
		// No org: the repo's own actors table is the roster (Q4), written
		// LIVE so every clone resolves the handle after its next fetch (Q12).
		liveCmdState.table, liveCmdState.choice = tableActors, rowroute.ChoiceLive
	}
	a, created, err := r.register(ctx, actorSpec{Kind: entActor.KindHuman, StableKey: id.StableKey, Name: id.DisplayName, Handle: defaultHandle(id.Handle, id.StableKey)})
	if err != nil {
		return out, err
	}
	out = opsinfra.ActorRegisterOut{ID: a.ID, Kind: opsinfra.RegisterKindHuman, Name: a.DisplayName, Created: created, Where: redactIfQuiet(r.where)}
	if a.Handle != nil {
		out.Handle = *a.Handle
	}
	return out, nil
}

// ActorDeactivate implements opsinfra.Roster.
func (cliRoster) ActorDeactivate(ctx context.Context, handle string) (out opsinfra.ActorDeactivateOut, err error) {
	defer func() { err = rosterErr(err) }()
	cr, err := openRosterOnCore(ctx)
	if err != nil {
		return out, err
	}
	if cr != nil {
		return cr.deactivate(ctx, handle)
	}
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return out, err
	}
	defer func() { _ = r.client.Close() }() // writes have saved by return; a close error is not actionable
	a, err := r.resolveHandle(ctx, handle)
	if err != nil {
		return out, err
	}
	if _, err := a.Update().SetActive(false).SetLeftAt(cliClock().Now().UTC()).Save(ctx); err != nil {
		return out, errcodes.New(errcodes.Internal, "update the roster").WithCause(err)
	}
	return opsinfra.ActorDeactivateOut{ID: a.ID, Name: a.DisplayName}, nil
}

// rosterErr turns a v0.1.17 CLIError into the core error the surface
// prints (same E_ code, message and hint); nil stays nil.
func rosterErr(err error) error {
	if err == nil {
		return nil
	}
	return asCoreError(err)
}
