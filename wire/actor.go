package wire

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
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
	// R11 (#7): agent detection is part of the identity chain (LORE_ACTOR and
	// LORE_HUMAN=1 still win inside it).
	in := identity.Inputs{Home: opts.Home, FlagActor: opts.FlagActor, Config: mc.Config, Dir: opts.RepoDir, Detect: DetectAgent}
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
//
// Q-R2-2: auto-written actor rows keep stable_key and handle in an org's
// own (non-client) repos, because the org's team needs them to resolve
// assignees and created_by refs; only client repos get the minimal row,
// because a client reads those files and must never see a person's email.
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
	// In a client repo an agent's writes are stamped as its owner
	// (clientActorAs), so the actor row written there is the owner's
	// projection, never the agent's (D8).
	who, kind, display := st.actor, actorKindOf(st.who.Kind), st.who.DisplayName
	if !st.agentOwner.IsZero() && st.eng.IsClient(target) {
		who, kind, display = st.agentOwner, schema.ActorKindHuman, st.agentOwnerName
	}
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: who.Any()}
	if _, _, err := st.eng.Get(ctx, ref); err == nil {
		st.actorDone[target] = true
		return nil
	} else if !errs.Has(err, errs.NotFound) {
		return err
	}
	fields := map[string]value.Value{}
	vals := map[string]string{actorFieldKind: kind, actorFieldDisplay: display}
	if who == st.actor && fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle] = st.who.StableKey, st.who.Handle
		// OQ8/OQ9: an agent's own row names the person it acts for and its
		// display label ("tomas via codex").
		if st.who.Owner != nil {
			if oid, oerr := identity.ActorID(st.who.Owner.StableKey); oerr == nil {
				// The owner's row must exist in this store first: actors
				// refs are checked at write time (DOCS O2), so naming an
				// owner the store does not hold would refuse the agent's
				// first write (found under Claude Code, 2026-11-04).
				if oerr := a.ensureOwnerRow(ctx, st, target, oid, *st.who.Owner); oerr != nil {
					return oerr
				}
				vals[rosterFieldOwner] = oid.String()
			}
		}
		if st.who.Label != "" {
			vals["label"] = st.who.Label
		}
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
	_, err := st.eng.CreateWithID(ctx, target, schema.TableActors, who.Any(), fields)
	if err != nil && !errs.Has(err, errs.Stale) { // a concurrent first write by the same person made the same row
		return errs.Wrap(errs.CodeOf(err), "wire: write the actor row to store "+string(target), err)
	}
	st.actorDone[target] = true
	return nil
}

// ensureOwnerRow writes the agent owner's actor row into target when that
// store does not hold it yet, so the agent's row can name its owner
// (OQ8/OQ9). Same shape as any actor row there: stable_key and handle only
// where fullActorRow allows them (Q-R2-2, BATCH2 #14/#15).
func (a *App) ensureOwnerRow(ctx context.Context, st *state, target store.Source, oid ids.ID[ids.Actor], owner identity.Resolved) error {
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: oid.Any()}
	if _, _, err := st.eng.Get(ctx, ref); err == nil {
		return nil
	} else if !errs.Has(err, errs.NotFound) {
		return err
	}
	vals := map[string]string{actorFieldKind: actorKindOf(owner.Kind), actorFieldDisplay: owner.DisplayName}
	if fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle] = owner.StableKey, owner.Handle
	}
	fields := map[string]value.Value{}
	for k, v := range vals {
		if v == "" {
			continue
		}
		t, err := value.TextOf(v)
		if err != nil {
			return errs.Wrap(errs.InvalidInput, "wire: the owner's "+k+" is not valid text", err)
		}
		fields[k] = t
	}
	if _, err := st.eng.CreateWithID(ctx, target, schema.TableActors, oid.Any(), fields); err != nil && !errs.Has(err, errs.Stale) {
		return errs.Wrap(errs.CodeOf(err), "wire: write the owner's actor row to store "+string(target), err)
	}
	return nil
}

// actorKindOf maps an identity kind to actors.kind, a closed set
// (schema.ActorKinds) that refuses identity.KindAuto: a machine or
// session-salt identity, and any unknown kind, is written as human, as
// v0.1.17's actorKind (saas/cmd/cli/init.go) did.
func actorKindOf(k string) string {
	if slices.Contains(schema.ActorKinds(), k) {
		return k
	}
	return schema.ActorKindHuman
}

// clientActorAs is the repo store's engine.Attached.ActorAs: in a client
// repo, a write by this App's agent identity is stamped as the agent's
// owning person (state.agentOwner, read from the org roster at open), as
// v0.1.17 recorded it (D8, Q11: ClientActorProjection), so neither the
// agent's id nor its label reaches the client's committed files. Any other
// actor, an internal repo, or an agent with no known owner is stamped as
// is (v0.1.17 kept the id of an agent without an owner).
func (s *state) clientActorAs(client bool) func(ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	if !client {
		return nil
	}
	return func(a ids.ID[ids.Actor]) ids.ID[ids.Actor] {
		if a == s.actor && !s.agentOwner.IsZero() {
			return s.agentOwner
		}
		return a
	}
}

// agentOwnerOf finds the owning person of this App's agent actor in the
// rosters of the attached stores (an actors row with this id and an
// owner_actor_id; the first store in precedence order that has one wins),
// with the owner's display_name from the same roster ("" when the owner
// row is absent). None found is the zero id, not an error.
func agentOwnerOf(ctx context.Context, st *state) (ids.ID[ids.Actor], string, error) {
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		rows, err := st.eng.List(ctx, name, schema.TableActors)
		if err != nil {
			return ids.ID[ids.Actor]{}, "", err
		}
		for _, r := range rows {
			if r.Deleted || r.ID != st.actor.Any() {
				continue
			}
			id, perr := ids.Parse[ids.Actor](fieldText(r, rosterFieldOwner))
			if perr != nil {
				continue
			}
			display := ""
			for _, o := range rows {
				if !o.Deleted && o.ID == id.Any() {
					display = fieldText(o, actorFieldDisplay)
				}
			}
			return id, display, nil
		}
	}
	return ids.ID[ids.Actor]{}, "", nil
}

// ensureNamedActors writes, into a client repo store, the minimal actor row
// of every actor a row being written there names: each actor-ref field of
// table's schema (a ref to actors, an any-table ref holding an act_ id, or
// a list of either), not only the caller (ensureActor).
//
// FLAG6 (owner A): when a client-visible row names an actor (assignee,
// done_by, claimed_by, reviewer, ...), lore writes that actor's minimal row
// {id, display_name, kind} — never stable_key, handle or email — into the
// client repo with the row, so every clone of the client repo can resolve
// the name. The engine write lands in the same backend write set as the
// row: in a live repo the actor row is held and rides in the row's live
// commit (saas/cmd/cli live_core.go holdActorCompanions), otherwise both
// files sit in .lore/data for the same code commit. It is the projection
// v0.1.17 recorded as ClientActorProjection (saas/pkg/aicoder/stores, which
// this module cannot import), rebuilt here from the actor's row in the
// first attached store that has it:
//   - an actor already in the client repo with the same display_name and
//     kind is skipped; a different one is updated to the projection;
//   - the caller and the agent owner are ensureActor's (skipped here);
//   - an agent while agent_details is internal is skipped: its projection
//     would be its owner under another id, and the row names the agent;
//   - an actor no attached store knows is skipped (nothing to project).
//
// Non-client stores are untouched (fullActorRow keeps its behaviour).
func (a *App) ensureNamedActors(ctx context.Context, st *state, target store.Source, table row.Table, fields map[string]value.Value) error {
	if target == "" {
		target = st.defaultTarget
	}
	if table == schema.TableActors || !st.eng.IsClient(target) {
		return nil
	}
	def, ok := st.eng.Registry().Table(table)
	if !ok {
		return nil // the write itself reports the unknown table
	}
	named := namedActors(def, fields)
	if len(named) == 0 {
		return nil
	}
	details := ""
	for _, id := range named {
		if id == st.actor || (!st.agentOwner.IsZero() && id == st.agentOwner) {
			continue
		}
		src, ok, err := actorRowElsewhere(ctx, st, target, id)
		if err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		kind, display := fieldText(src, actorFieldKind), fieldText(src, actorFieldDisplay)
		if kind == schema.ActorKindAgent {
			if details == "" {
				if details, err = agentDetailsOf(a.opts.Home, st.who.Org); err != nil {
					return err
				}
			}
			if details != AgentDetailsClient {
				continue
			}
		}
		if err := writeClientActor(ctx, st, target, id, actorKindOf(kind), display); err != nil {
			return err
		}
	}
	return nil
}

// namedActors lists, once each in field order, the actor ids fields holds
// in def's actor-ref fields.
func namedActors(def schema.TableDef, fields map[string]value.Value) []ids.ID[ids.Actor] {
	var out []ids.ID[ids.Actor]
	seen := map[ids.ID[ids.Actor]]bool{}
	add := func(v value.Value) {
		if v == nil || v.Kind() != value.KindRef {
			return
		}
		id, err := ids.Parse[ids.Actor](v.String())
		if err != nil || id.IsZero() || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, f := range def.Fields {
		if f.RefTable != "" && f.RefTable != schema.TableActors {
			continue
		}
		v, ok := fields[f.Name]
		if !ok || v == nil {
			continue
		}
		switch {
		case f.Kind == value.KindRef:
			add(v)
		case f.Kind == value.KindList && f.Elem == value.KindRef:
			if l, isList := v.(value.List); isList {
				for _, it := range l.Items() {
					add(it)
				}
			}
		}
	}
	return out
}

// actorRowElsewhere is the live actors row id in the first attached store,
// in precedence order, other than target (ok=false when none has one).
func actorRowElsewhere(ctx context.Context, st *state, target store.Source, id ids.ID[ids.Actor]) (row.Row, bool, error) {
	for _, name := range st.eng.Stores() {
		if name == target {
			continue
		}
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: name, Table: schema.TableActors, ID: id.Any()})
		if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
			continue
		}
		if err != nil {
			return row.Row{}, false, err
		}
		return r, true, nil
	}
	return row.Row{}, false, nil
}

// writeClientActor makes target's actors row id the projection {kind,
// display_name}: created when absent, updated when it differs, left when
// it matches.
func writeClientActor(ctx context.Context, st *state, target store.Source, id ids.ID[ids.Actor], kind, display string) error {
	want := map[string]value.Value{}
	for k, v := range map[string]string{actorFieldKind: kind, actorFieldDisplay: display} {
		if v == "" {
			continue
		}
		t, err := value.TextOf(v)
		if err != nil {
			return errs.Wrap(errs.InvalidInput, "wire: actor "+id.String()+" field "+k, err)
		}
		want[k] = t
	}
	if _, ok := want[actorFieldDisplay]; !ok {
		return nil // display_name is required: an actor with none cannot be projected
	}
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: id.Any()}
	_, cur, err := st.eng.Get(ctx, ref)
	switch {
	case errs.Has(err, errs.NotFound) || (err == nil && cur.Deleted):
		_, err = st.eng.CreateWithID(ctx, target, schema.TableActors, id.Any(), want)
		if err != nil && !errs.Has(err, errs.Stale) {
			return errs.Wrap(errs.CodeOf(err), "wire: write the actor row "+id.String()+" to store "+string(target), err)
		}
		return nil
	case err != nil:
		return err
	}
	patch := map[string]value.Value{}
	for k, v := range want {
		if fieldText(cur, k) != v.String() {
			patch[k] = v
		}
	}
	if len(patch) == 0 {
		return nil
	}
	_, err = st.eng.Update(ctx, ref, patch)
	return err
}
