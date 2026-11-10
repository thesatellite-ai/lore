package profilestore

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/model/ids"
)

// ActorSources is an optional upgrade of an op's Env (wire.App
// implements it): one actor's source of truth and copies
// (logic/actorsource, SOURCE_OF_TRUTH actors), and the refresh that
// rewrites stale copies now.
//
// Why a seam: which value is the truth depends on what only the host
// knows (the caller's identity chain and setup, the org roster store and
// its sto_ id), while actor list, actor show and the refresh op live in
// ops. An Env without it shows each store's copy as before.
type ActorSources interface {
	// ActorReport is actor id's source and every attached store's copy.
	// A store that cannot be read is left out (the doctor reports it).
	ActorReport(ctx context.Context, id ids.AnyID) (actorsource.Report, error)
	// RefreshActorCopies rewrites every stale or unstamped copy of the
	// actors ids names (nil: every actor with a copy here) from its source
	// and returns what it wrote. An actor whose source cannot be read is
	// skipped (nothing to copy from).
	RefreshActorCopies(ctx context.Context, actors []ids.AnyID) ([]RefreshedCopy, error)
}

// RefreshedCopy is one copy RefreshActorCopies rewrote.
type RefreshedCopy struct {
	Actor string `json:"actor"`
	Store string `json:"store"`
	// From and To are the copy's display_name before and after (equal when
	// only the stamp or another field changed).
	From string `json:"from"`
	To   string `json:"to"`
}

// IdentityRenamer is an optional upgrade of an op's Env: actor edit
// --name in a repo with no org changes the source of a solo actor, the
// caller's own identity (the active setup's identity, else the identity
// block the chain answered from). Only the caller's own actor can be
// renamed this way.
type IdentityRenamer interface {
	RenameIdentity(ctx context.Context, ref, name string) (Renamed, error)
}

// Renamed is what RenameIdentity changed.
type Renamed struct {
	ID string `json:"id"`
	// Where names the identity written: "setup solo1", "identity: in
	// ~/.lore/config.yaml".
	Where string `json:"where"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// PersonEditGate is an optional upgrade of a Roster: whether the caller
// may change person id's roster row (SOURCE_OF_TRUTH actors: the person
// themself or an admin of the org). nil allows; a refusal is
// E_POLICY_DENIED naming who may.
type PersonEditGate interface {
	MayEditPerson(ctx context.Context, id ids.AnyID) error
}
