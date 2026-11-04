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
	for k, v := range map[string]string{actorFieldKind: st.who.Kind, actorFieldDisplay: st.who.DisplayName,
		actorFieldKey: st.who.StableKey, actorFieldHandle: st.who.Handle} {
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
