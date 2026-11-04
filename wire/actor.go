package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// actors field names written on a first write (schema.Builtin actors).
const (
	actorFieldKind    = "kind"
	actorFieldDisplay = "display_name"
	actorFieldKey     = "stable_key"
	actorFieldHandle  = "handle"
)

// resolveIdentity runs the identity chain (adapters/identity) with the
// machine config and the optional org seam.
func resolveIdentity(ctx context.Context, opts Options) (identity.Resolved, error) {
	mc, err := readMachineConfig(opts.Home)
	if err != nil {
		return identity.Resolved{}, err
	}
	in := identity.Inputs{Home: opts.Home, FlagActor: opts.FlagActor, Config: mc.Config, Dir: opts.RepoDir}
	if opts.Org != nil {
		org, err := opts.Org(ctx)
		if err != nil {
			return identity.Resolved{}, err
		}
		in.Org = org
	}
	return identity.Resolve(ctx, in)
}

// EnsureActor writes the caller's actor row into target unless it is
// there (the old binary's lore.db actor writes use it: actors are
// core-owned in the checkout, so a lore.db-only row would never reach a
// file). The row is the same projection every core write makes.
func (a *App) EnsureActor(ctx context.Context, target store.Source) error {
	st, err := a.writeState(ctx)
	if err != nil {
		return err
	}
	return a.ensureActor(ctx, st, target)
}

// fullActorRow reports whether the caller's actor row written into target
// may carry the identity's stable key (an email) and handle. A client
// repo (the repo store when Options.RepoIsClient says so, fails or is
// unset) never may. The repo store otherwise may only in an org's OWN
// repo (the identity resolved inside an org, Resolved.Org): a repo with
// no org, or a client's developer checkout with no org config, gets
// v0.1.17's F18 projection (id, display_name, kind), as v0.1.17 wrote it
// (spec-deltas: "in a repo that is not an org own repo the file is the
// F18 projection"), because the repo's files are committed and an email
// there is published with the code. Every other store that is not
// client-class (org stores, me) keeps the full row.
func fullActorRow(st *state, target store.Source) bool {
	if st.eng.IsClient(target) {
		return false
	}
	if target == store.SourceRepo {
		return st.who.Org != ""
	}
	return true
}

// ensureActor writes the caller's actor row into target on its first write
// there (through the engine, so it is scanned, stamped and audited like any
// row), the repo store included: an identity that only runs core commands
// would otherwise have no actor row there (task --assignee, created_by
// refs). The id is v0.1.17's deterministic actor id, so a v0.1.17 export
// of the same actor writes the same file (it may rewrite it as `_v: 1`
// until the actors noun migrates; both read the same).
func (a *App) ensureActor(ctx context.Context, st *state, target store.Source) error {
	if target == "" {
		target = st.defaultTarget
	}
	if st.who.StableKey == "" {
		return nil
	}
	st.actorMu.Lock()
	defer st.actorMu.Unlock()
	if st.actorDone[target] {
		return nil
	}
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: st.actor.Any()}
	if _, _, err := st.eng.Get(ctx, ref); err == nil {
		st.actorDone[target] = true
		return nil
	} else if !errs.Has(err, errs.NotFound) {
		return err
	}
	fields := map[string]value.Value{}
	vals := map[string]string{actorFieldKind: st.who.Kind, actorFieldDisplay: st.who.DisplayName}
	if fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle] = st.who.StableKey, st.who.Handle
	}
	for k, v := range vals {
		if v == "" {
			continue
		}
		t, err := value.TextOf(v)
		if err != nil {
			return errs.Wrap(errs.InvalidInput, "wire: the actor's "+k+" is not valid text", err)
		}
		fields[k] = t
	}
	_, err := st.eng.CreateWithID(ctx, target, schema.TableActors, st.actor.Any(), fields)
	if err != nil && !errs.Has(err, errs.Stale) { // a concurrent first write by the same person made the same row
		return errs.Wrap(errs.CodeOf(err), "wire: write the actor row to store "+string(target), err)
	}
	st.actorDone[target] = true
	return nil
}
