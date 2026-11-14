package wire

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/actorproject"
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
	if opts.PersonOnly {
		in.Detect = nil // `lore api`: the server is the person, not its starter's tool
	}
	// ONBOARDING 3.4: the active solo setup's identity, after the org entry.
	in.Setup = setupIdentity(ctx, opts.Home, optsProjectDir(opts))
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
	if err := refuseNoPersonalStore(st, target); err != nil {
		return err
	}
	// P3-LOCAL-1: the machine store keeps only machine-local tables, and
	// actors is not one (a machine-local row's actor refs resolve against
	// the stores that hold actor rows), so nothing is written there.
	if target == store.SourceMachine {
		return nil
	}
	st.actorMu.Lock()
	defer st.actorMu.Unlock()
	if st.wroteTo == nil {
		st.wroteTo = map[store.Source]bool{}
	}
	st.wroteTo[target] = true
	if st.who.StableKey == "" {
		return nil
	}
	if st.actorDone[target] {
		return nil
	}
	if w, ok := st.storeWho[target]; ok {
		// C19: an org store's rows are stamped with the person's identity
		// in that org (orgstoreactor.go), so that actor's row goes there.
		if err := a.ensureWriterRow(ctx, st, target, w); err != nil {
			return err
		}
		st.actorDone[target] = true
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
	if _, cur, err := st.eng.Get(ctx, ref); err == nil {
		// devco:K-6: both updates below are lore's own upkeep of a copy
		// that exists, so a live write carries them with its row instead
		// of rewriting the committed actor file (store.WithSupportingRefresh).
		rctx := store.WithSupportingRefresh(ctx)
		if berr := a.backfillKey(rctx, st, ref, cur, who); berr != nil {
			return berr
		}
		// SOURCE_OF_TRUTH actors: a copy whose stamp is not its source's
		// version is rewritten on this first write to its store
		// (actor_source.go; the caller's own actor only).
		a.refreshOwnCopy(rctx, st, target, cur)
		st.actorDone[target] = true
		return nil
	} else if !errs.Has(err, errs.NotFound) {
		return err
	}
	fields := map[string]value.Value{}
	vals := map[string]string{actorFieldKind: kind, actorFieldDisplay: display}
	rebound := who == st.actor && st.rebound()
	if rebound {
		// TEAM_PROFILES 6.3 (TP-4): the open was rebound to the person's one
		// actor (an identity contact), so the row written is that actor's,
		// with its own stable_key and handle, never one keyed by the second
		// email (no second actor, no UniqueOn(stable_key) clash).
		var rerr error
		if vals, rerr = st.reboundFields(ctx, target); rerr != nil {
			return rerr
		}
	}
	if who == st.actor && !rebound && fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle] = st.who.StableKey, st.who.Handle
		// OQ8/OQ9: an agent's own row names the person it acts for and its
		// display label ("tomas via codex").
		if st.who.Owner != nil {
			if oid, oerr := st.who.Owner.ActorID(); oerr == nil {
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
	if who == st.actor {
		// SOURCE_OF_TRUTH actors: a new copy takes its source's values and
		// is stamped with where it came from (never the roster row itself).
		a.stampNewCopy(ctx, st, target, who.Any(), vals)
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
		return errs.Prefix("wire: write the actor row to store "+string(target), err)
	}
	st.actorDone[target] = true
	return nil
}

// ensureWriterRow writes the actor row of w, the person's identity in the
// org that owns target (storeWho), into target unless it is there: the
// same projection ensureActor writes for the open's own identity (kind,
// display_name, and stable_key, handle, owner and label where
// fullActorRow allows them).
func (a *App) ensureWriterRow(ctx context.Context, st *state, target store.Source, w storeWriter) error {
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: w.id.Any()}
	if _, _, err := st.eng.Get(ctx, ref); err == nil {
		return nil
	} else if !errs.Has(err, errs.NotFound) {
		return err
	}
	vals := map[string]string{actorFieldKind: actorKindOf(w.who.Kind), actorFieldDisplay: w.who.DisplayName}
	if fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle], vals["label"] = w.who.StableKey, w.who.Handle, w.who.Label
		if o := w.who.Owner; o != nil {
			if oid, oerr := o.ActorID(); oerr == nil {
				if oerr := a.ensureOwnerRow(ctx, st, target, oid, *o); oerr != nil {
					return oerr
				}
				vals[rosterFieldOwner] = oid.String()
			}
		}
	}
	fields, err := textFields(vals)
	if err != nil {
		return errs.Prefix("wire: the actor row of your identity in store "+string(target), err)
	}
	if _, err := st.eng.CreateWithID(ctx, target, schema.TableActors, w.id.Any(), fields); err != nil && !errs.Has(err, errs.Stale) {
		return errs.Prefix("wire: write the actor row to store "+string(target), err)
	}
	return nil
}

// refuseNoPersonalStore answers a write to `me` (--to me, --scope global)
// on a machine with no personal store attached with a refusal that says
// what is missing and how to make it (INTEG-RENDER-3), before the first
// row (the caller's actor row) would fail with the engine's internal
// "no store named me" / "write the actor row to store me". The code stays
// E_NOT_FOUND: the store is not there.
func refuseNoPersonalStore(st *state, target store.Source) error {
	if target != store.Source(attach.ReservedMe) || slices.Contains(st.eng.Stores(), target) {
		return nil
	}
	return errs.New(errs.NotFound, "no personal store is attached on this machine: --scope global and --to me write to your personal store (me)").
		WithHint("your personal store is not set up: run `lore store init --name me` (a local folder) or `lore store init --name me --url <url>`, then run the command again")
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
		return errs.Prefix("wire: write the owner's actor row to store "+string(target), err)
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
// v0.1.17 recorded as ClientActorProjection, built from the actor's row in
// the first attached store that has it:
//   - an actor already in the client repo with the same display_name and
//     kind is skipped; a different one is updated to the projection;
//   - the caller and the agent owner are ensureActor's (skipped here);
//   - an agent while agent_details is internal is skipped: its projection
//     would be its owner under another id, and the row names the agent;
//   - an actor no attached store knows is skipped (nothing to project).
//
// The projection itself is logic/actorproject.ClientProjection (later 385:
// one definition below wire). Non-client stores are untouched (fullActorRow keeps its behaviour).
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
		kind := fieldText(src, actorFieldKind)
		agentClient := false
		if kind == schema.ActorKindAgent { // agent_details is read only when an agent is named
			if details == "" {
				if details, err = agentDetailsOf(a.opts.Home, st.who.Org); err != nil {
					return err
				}
			}
			agentClient = details == AgentDetailsClient
		}
		proj, write := actorproject.ClientProjection(actorKindOf(kind), fieldText(src, actorFieldDisplay), agentClient)
		if !write {
			continue
		}
		if err := writeClientActor(ctx, st, target, id, proj); err != nil {
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

// writeClientActor makes target's actors row id the FLAG6 projection
// (logic/actorproject): created when absent, updated when it differs, left
// when it matches.
func writeClientActor(ctx context.Context, st *state, target store.Source, id ids.ID[ids.Actor], proj actorproject.ClientActor) error {
	ref := engine.Ref{Store: target, Table: schema.TableActors, ID: id.Any()}
	_, cur, err := st.eng.Get(ctx, ref)
	create := errs.Has(err, errs.NotFound) || (err == nil && cur.Deleted)
	if err != nil && !create {
		return err
	}
	cols := proj.Fields()
	if !create {
		if cols = proj.Patch(func(f string) string { return fieldText(cur, f) }); len(cols) == 0 {
			return nil
		}
	}
	want, err := textFields(cols)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "wire: actor "+id.String(), err)
	}
	if !create {
		_, err = st.eng.Update(ctx, ref, want)
		return err
	}
	_, err = st.eng.CreateWithID(ctx, target, schema.TableActors, id.Any(), want)
	if err != nil && !errs.Has(err, errs.Stale) {
		return errs.Prefix("wire: write the actor row "+id.String()+" to store "+string(target), err)
	}
	return nil
}

// settleAgentKey applies the 6.1 collision rule (OQ9) to a detected
// agent's key before anything is stamped with it: the key carries the
// first 6 hex of the owner's act_ id, and the second owner whose agent
// would get the same key uses 10 instead, for good.
//
//	my 10-hex row exists in any store       → 10 (the key never changes back)
//	6-hex row exists with another owner     → 10
//	otherwise                               → 6
//
// Stores that fail to read are skipped: refusing every command over an
// unreadable store would be worse, and the 10-hex row, once written, wins
// from then on. A non-detected identity (person, LORE_ACTOR, CI) is never
// touched.
func (s *state) settleAgentKey(ctx context.Context, backends map[store.Source]store.Backend) error {
	wide, ok, err := identity.WidenAgentKey(s.who)
	if err != nil || !ok {
		return err
	}
	ownerID, err := s.who.Owner.ActorID()
	if err != nil {
		return err
	}
	wideID, err := identity.ActorID(wide.StableKey)
	if err != nil {
		return err
	}
	widen := false
	for _, b := range backends {
		if _, gerr := b.Get(ctx, schema.TableActors, wideID.Any()); gerr == nil {
			widen = true
			break
		}
		got, gerr := b.Get(ctx, schema.TableActors, s.actor.Any())
		if gerr != nil || got.Deleted {
			continue
		}
		r, derr := got.Decode()
		if derr != nil {
			continue
		}
		if o := fieldText(r, rosterFieldOwner); o != "" && o != ownerID.String() {
			widen = true
			break
		}
	}
	if widen {
		s.who, s.actor = wide, wideID
	}
	return nil
}

// backfillKey writes the caller's stable key (and handle) into its own
// actor row in ref's store when the row there has none but the store may
// hold it (fullActorRow): a row written before the identity had a key, or
// by an older build, gets the key on the person's next write there
// (FB-ACTOR-KEY), so `lore actor list --json` and the roster resolve the
// person by key. A store that may not hold the key (a client repo, a repo
// that is not an org's own) is never written; a rebound open writes its
// own fields, never these.
func (a *App) backfillKey(ctx context.Context, st *state, ref engine.Ref, cur row.Row, who ids.ID[ids.Actor]) error {
	if who != st.actor || st.rebound() || !fullActorRow(st, ref.Store) {
		return nil
	}
	patch := map[string]value.Value{}
	for k, v := range map[string]string{actorFieldKey: st.who.StableKey, actorFieldHandle: st.who.Handle} {
		if v == "" || actorField(cur, k) != "" {
			continue
		}
		t, err := value.TextOf(v)
		if err != nil {
			return errs.Wrap(errs.InvalidInput, "wire: the actor's "+k+" is not valid text", err)
		}
		patch[k] = t
	}
	if len(patch) == 0 {
		return nil
	}
	if _, err := st.eng.Update(ctx, ref, patch); err != nil && !errs.Has(err, errs.Stale) {
		return errs.Prefix("wire: add the stable key to the actor row in store "+string(ref.Store), err)
	}
	return nil
}

// actorField is the text of an actor row field ("" when absent or null).
func actorField(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}
