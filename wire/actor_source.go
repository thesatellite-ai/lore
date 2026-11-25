package wire

// actor_source.go — one source of truth for an actor held in several
// stores (SOURCE_OF_TRUTH rows 2 and 3, owner 2026-11-09); the rules
// themselves are logic/actorsource.
//
//	source  team: the org roster store's actors row (the roster App's
//	              "<org>/<folder>" store, attached here)
//	        solo: the caller's identity as the chain resolved it (st.who:
//	              the active setup's identity, else the block it came from)
//	copies  every other attached store's actors row of the same id, stamped
//	        origin_store (sto_… / setup:<name>) + origin_version (roster
//	        HLC / identity hash)
//
//	ensureActor ──▶ copy there? no ──▶ create it stamped (stampNewCopy)
//	                            yes ──▶ stale or unstamped? ──▶ Update (ownCopyPatch, one write with
//	                                    the key back-fill and agent rename: upkeepOwnRow)
//	                                    team: orgs[].identity.name follows the roster
//	ActorReport ──▶ actor list / show, lore show <act_id>, doctor, Team versions
//	RefreshActorCopies ──▶ `lore actor refresh` (the doctor's fix)
//
// A refresh on a write path is best effort: a copy that cannot be
// rewritten stays as it is (the doctor names it) and never fails the
// person's own write.

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var _ profilestore.ActorSources = (*App)(nil)

// actorSourceAt is a source and, for a roster source, the store holding
// it ("" otherwise): that store's row is the source itself, never a copy.
type actorSourceAt struct {
	src    actorsource.Source
	roster store.Source
}

// actorRosterStore is the org roster store attached in st ("" when this
// App has no org, the org has no roster store, or it is not attached).
func (a *App) actorRosterStore(ctx context.Context, st *state) store.Source {
	if a.opts.Org == nil {
		return ""
	}
	r, err := a.repoRoster(ctx)
	if err != nil || r.src == store.SourceRepo || !slices.Contains(st.eng.Stores(), r.src) {
		return ""
	}
	return r.src
}

// storeOrigin is what a copy's origin_store names for the roster store
// src: its lore-store.yaml sto_ id, else (a store with no id) its name.
func storeOrigin(st *state, src store.Source) string {
	if mb, ok := concrete(st.byName[src]).(markerBackend); ok {
		if id := mb.Marker().ID; !id.IsZero() {
			return id.String()
		}
	}
	return string(src)
}

// identityOrigin is the origin_store and the message label of the
// caller's identity source: the setup it came from, the org entry, or the
// chain step.
func (a *App) identityOrigin(ctx context.Context, st *state) (origin, where string) {
	switch st.who.Step {
	case identity.StepSetup:
		if s := setupIdentity(ctx, a.opts.Home, a.projectDir()); s != nil {
			return actorsource.OriginSetupPrefix + s.Setup, "setup " + s.Setup
		}
	case identity.StepOrg:
		return actorsource.OriginOrgPrefix + st.who.Org, "the identity of org " + st.who.Org + " (config.yaml)"
	case identity.StepTop:
		return actorsource.OriginIdentityPrefix + "top", "identity: (config.yaml)"
	case identity.StepGit:
		return actorsource.OriginIdentityPrefix + "git", "git config user.email"
	}
	return actorsource.OriginIdentityPrefix + "other", string(st.who.Step)
}

// callerIsSource reports whether id is the caller's own actor as the
// identity chain resolved it (not rebound to another actor by an identity
// contact, TP-4: that actor's source is its roster row).
func callerIsSource(st *state, id ids.AnyID) bool {
	return st.who.StableKey != "" && id == st.actor.Any() && !st.rebound()
}

// actorSourceOf is actor id's source: its roster row when the roster store
// holds it, else (the caller only) the caller's identity, else none.
func (a *App) actorSourceOf(ctx context.Context, st *state, id ids.AnyID) actorSourceAt {
	if rs := a.actorRosterStore(ctx, st); rs != "" {
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: rs, Table: schema.TableActors, ID: id})
		switch {
		case err == nil && !r.Deleted:
			return actorSourceAt{roster: rs, src: actorsource.Source{Known: true, Reachable: true, Origin: storeOrigin(st, rs),
				Where: string(rs), Values: valuesOfRow(r), Version: r.HLC.String()}}
		case err != nil && !errs.Has(err, errs.NotFound):
			return actorSourceAt{roster: rs, src: actorsource.Source{Known: true, Origin: storeOrigin(st, rs), Where: string(rs)}}
		}
	}
	if callerIsSource(st, id) {
		v := actorsource.Values{Name: st.who.DisplayName, Kind: actorKindOf(st.who.Kind), StableKey: st.who.StableKey, Handle: st.who.Handle,
			Aliases: actorsource.JoinAliases(st.who.Aliases)}
		origin, where := a.identityOrigin(ctx, st)
		return actorSourceAt{src: actorsource.Source{Known: true, Reachable: true, Origin: origin, Where: where, Values: v,
			Version: actorsource.IdentityVersion(v)}}
	}
	return actorSourceAt{}
}

// valuesOfRow is the identity fields of an actors row.
func valuesOfRow(r row.Row) actorsource.Values {
	return actorsource.Values{Name: fieldText(r, actorFieldDisplay), Kind: fieldText(r, actorFieldKind),
		StableKey: fieldText(r, actorFieldKey), Handle: fieldText(r, actorFieldHandle), Aliases: fieldText(r, schema.FieldActorKeyAliases)}
}

// copyOfRow is store src's actors row r as a copy; a copy where lore
// writes only the F18 projection (a client store, or the repo store of a
// repo with no org: fullActorRow) is Copy.Projected, never stamped.
func copyOfRow(st *state, src store.Source, r row.Row, isSource bool) actorsource.Copy {
	return actorsource.Copy{Store: string(src), Values: valuesOfRow(r), OriginStore: fieldText(r, schema.FieldActorOriginStore),
		OriginVersion: fieldText(r, schema.FieldActorOriginVersion), HLC: r.HLC, IsSource: isSource, Projected: !fullActorRow(st, src)}
}

// actorReport is id's source and the live copy of every attached store
// read across (machine store and shadowed names left out; a store that
// fails to read is left out too: the store checks report it).
func (a *App) actorReport(ctx context.Context, st *state, id ids.AnyID) actorsource.Report {
	at := a.actorSourceOf(ctx, st, id)
	shadow := shadowedStores(st.eng)
	rep := actorsource.Report{ID: id.String(), Source: at.src}
	for _, name := range st.eng.Stores() {
		if name == store.SourceMachine || shadow[name] {
			continue
		}
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: name, Table: schema.TableActors, ID: id})
		if err != nil || r.Deleted {
			continue
		}
		rep.Copies = append(rep.Copies, copyOfRow(st, name, r, name == at.roster && at.src.Reachable))
	}
	rep.Source = actorsource.Settle(rep.Source, rep.Copies)
	return rep
}

// ActorReport implements profilestore.ActorSources.
func (a *App) ActorReport(ctx context.Context, id ids.AnyID) (actorsource.Report, error) {
	st, err := a.state(ctx)
	if err != nil {
		return actorsource.Report{}, err
	}
	return a.actorReport(ctx, st, id), nil
}

// refreshCopy rewrites copy c (in c.Store) from src: the fields Patch
// names, in one engine update (a new version of the file, scanned and
// audited like any write). wrote is false when nothing differed.
func refreshCopy(ctx context.Context, st *state, id ids.AnyID, src actorsource.Source, c actorsource.Copy) (bool, error) {
	patch := actorsource.Patch(src, c)
	if len(patch) == 0 {
		return false, nil
	}
	if k, ok := patch[actorsource.FieldKind]; ok {
		patch[actorsource.FieldKind] = actorKindOf(k)
	}
	fields, err := textFields(patch)
	if err != nil {
		return false, err
	}
	if _, err = st.eng.Update(ctx, engine.Ref{Store: store.Source(c.Store), Table: schema.TableActors, ID: id}, fields); err != nil {
		return false, errs.Prefix("refresh the actor copy "+id.String()+" in store "+c.Store, err)
	}
	return true, nil
}

// ownCopyPatch is ensureActor's refresh of a copy that already exists in
// target (cur): when it is the caller's own actor and stale or unstamped
// against its source, the fields that make it current; in a team, also
// bring the config's orgs[].identity.name in line with the roster. The
// caller writes it together with its other upkeep of the row
// (upkeepOwnRow); pending holds those fields, already decided, so the
// refresh is judged on the row as it will be. Best effort (file comment):
// nil when the copy needs nothing or the fields are not valid text.
func (a *App) ownCopyPatch(ctx context.Context, st *state, target store.Source, cur row.Row, pending map[string]value.Value) map[string]value.Value {
	if cur.Deleted || cur.ID != st.actor.Any() {
		return nil
	}
	at := a.actorSourceOf(ctx, st, cur.ID)
	if at.roster != "" && at.src.Reachable {
		a.followRosterName(ctx, st, at.src)
	}
	next := cur
	next.Fields = maps.Clone(cur.Fields)
	maps.Copy(next.Fields, pending)
	c := copyOfRow(st, target, next, target == at.roster && at.src.Reachable)
	if !actorsource.NeedsRefresh(at.src, c) {
		return nil
	}
	patch := actorsource.Patch(at.src, c)
	if k, ok := patch[actorsource.FieldKind]; ok {
		patch[actorsource.FieldKind] = actorKindOf(k)
	}
	fields, err := textFields(patch)
	if err != nil || len(fields) == 0 {
		return nil
	}
	return fields
}

// followRosterName rewrites orgs[<org>].identity.name in config.yaml to
// the roster row's display_name when the caller's identity came from that
// org entry, names the same key and differs (the config entry is a copy of
// the roster in a team). Best effort.
func (a *App) followRosterName(ctx context.Context, st *state, src actorsource.Source) {
	if st.who.Step != identity.StepOrg || st.who.Org == "" || src.Values.Name == "" || src.Values.Name == st.who.DisplayName {
		return
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return
	}
	for _, o := range mc.Orgs {
		if o.Name != st.who.Org || o.Identity.Key != st.who.StableKey || o.Identity.Name == src.Values.Name {
			continue
		}
		e := o.Identity
		e.Name = src.Values.Name
		if _, _, err := a.SetOrgIdentity(ctx, o.Name, opsidentity.Entry{Key: e.Key, Name: e.Name, Handle: e.Handle}); err == nil {
			st.who.DisplayName = src.Values.Name
		}
		return
	}
}

// stampNewCopy fills the fields of the caller's first actor row in target
// from its source: the source's values where the row carries that field
// and the origin stamp, unless target holds the source itself (the roster
// store: never stamped) or the source cannot be read.
func (a *App) stampNewCopy(ctx context.Context, st *state, target store.Source, id ids.AnyID, vals map[string]string) {
	at := a.actorSourceOf(ctx, st, id)
	if !at.src.Reachable || target == at.roster {
		return
	}
	held := actorsource.Copy{Values: actorsource.Values{Name: vals[actorFieldDisplay], Kind: vals[actorFieldKind],
		StableKey: vals[actorFieldKey], Handle: vals[actorFieldHandle], Aliases: vals[schema.FieldActorKeyAliases]}, Projected: !fullActorRow(st, target)}
	for k, v := range actorsource.Patch(at.src, held) {
		if k == actorsource.FieldKind {
			v = actorKindOf(v)
		}
		vals[k] = v
	}
}

// RefreshActorCopies implements profilestore.ActorSources.
func (a *App) RefreshActorCopies(ctx context.Context, actors []ids.AnyID) ([]profilestore.RefreshedCopy, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return nil, err
	}
	if actors == nil {
		if actors, err = a.actorIDs(ctx, st); err != nil {
			return nil, err
		}
	}
	out := []profilestore.RefreshedCopy{}
	for _, id := range actors {
		rep := a.actorReport(ctx, st, id)
		if at := a.actorSourceOf(ctx, st, id); at.roster != "" && at.src.Reachable && callerIsSource(st, id) {
			a.followRosterName(ctx, st, at.src)
		}
		for _, c := range rep.Stale() {
			wrote, rerr := refreshCopy(ctx, st, id, rep.Source, c)
			a.wroteTo(store.Source(c.Store), rerr)
			if rerr != nil {
				return out, rerr
			}
			if wrote {
				out = append(out, profilestore.RefreshedCopy{Actor: id.String(), Store: c.Store, From: c.Values.Name, To: rep.Source.Values.Name})
			}
		}
	}
	return out, nil
}

// actorIDs is every live actor id of the attached stores read across,
// once each, sorted.
func (a *App) actorIDs(ctx context.Context, st *state) ([]ids.AnyID, error) {
	shadow := shadowedStores(st.eng)
	seen := map[ids.AnyID]bool{}
	var out []ids.AnyID
	for _, name := range st.eng.Stores() {
		if name == store.SourceMachine || shadow[name] {
			continue
		}
		rows, err := st.eng.List(ctx, name, schema.TableActors)
		if err != nil {
			continue // an unreadable store: the store checks report it
		}
		for _, r := range rows {
			if !r.Deleted && !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r.ID)
			}
		}
	}
	slices.SortFunc(out, func(x, y ids.AnyID) int { return strings.Compare(x.String(), y.String()) })
	return out, nil
}

// actorSourceGet answers an unqualified Get of an actor id that several
// stores hold (one actor written once per store, not two rows; the engine
// alone answers the newest copy): the source's row when the roster holds it, else the
// first current copy, else the first copy, with display_name set to the
// value a merged read shows. An empty source (no error) means id has no
// live copy here after all: the caller keeps the engine's answer.
func (a *App) actorSourceGet(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", row.Row{}, err
	}
	rep := a.actorReport(ctx, st, ref.ID)
	if len(rep.Copies) < 2 {
		return "", row.Row{}, nil // one copy: the engine's answer is it
	}
	pick := rep.Copies[0]
	for _, c := range rep.Copies {
		if s := actorsource.StateOf(rep.Source, c); s == actorsource.StateSource {
			pick = c
			break
		} else if s == actorsource.StateCurrent && actorsource.StateOf(rep.Source, pick) != actorsource.StateCurrent {
			pick = c
		}
	}
	_, r, err := st.eng.Get(ctx, engine.Ref{Store: store.Source(pick.Store), Table: schema.TableActors, ID: ref.ID})
	if err != nil {
		return "", row.Row{}, err
	}
	if n := rep.Shown().Values.Name; n != "" && n != fieldText(r, actorFieldDisplay) {
		if t, terr := textFields(map[string]string{actorFieldDisplay: n}); terr == nil {
			r.Fields = maps.Clone(r.Fields)
			r.Fields[actorFieldDisplay] = t[actorFieldDisplay]
		}
	}
	return store.Source(pick.Store), r, nil
}

// ActorNamer is the name function human output uses for actor ids
// (surface/cli WithActorNamer): the actor's @handle, else its name, from
// its source store, else from an actors row in a folder of the org store
// clones on this machine (cloneActorName); the id itself when none names it.
func (a *App) ActorNamer(ctx context.Context) func(string) string {
	n := a.sourceActorNamer(ctx)
	return func(actor string) string {
		if actor == "" {
			return ""
		}
		if name, ok := auditfill.NameOf(actor); ok {
			return name
		}
		if name := n(actor); name != actor {
			return name
		}
		if name := a.cloneActorName(ctx, actor); name != "" {
			return name
		}
		return actor
	}
}

// cloneActorName is the @handle, else the name, of actor's live actors row
// in any folder of the org store clones on this machine; "" when none
// holds one. devco:D-611: an answer the home folder gives (E_IN_TRASH of a
// row read straight from a store clone's file, goneRowAnswer) printed
// "deleted by act_…": that App attaches no store, so its namer named
// nobody, while trash list in the folder named the person.
func (a *App) cloneActorName(ctx context.Context, actor string) string {
	if _, err := ids.Parse[ids.Actor](actor); err != nil {
		return ""
	}
	for _, f := range a.everyOrgFolders(ctx).Folders {
		if f.Dir == "" {
			continue
		}
		path := filepath.Join(f.Dir, filepath.FromSlash(f.Folder), schema.TableActors, actor+".json")
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			continue
		}
		r, err := row.Decode(b)
		if err != nil || r.Deleted {
			continue
		}
		if name := actorDisplayName(r, ""); name != "" {
			return name
		}
	}
	return ""
}

// sourceActorNamer is the writer-name function of doctor's Team versions
// notes, from each actor's source (handle, else name) rather than
// whichever store's copy comes first; an actor no store holds falls back
// to teamActorNamer's answer (the id).
func (a *App) sourceActorNamer(ctx context.Context) func(string) string {
	st, err := a.state(ctx)
	fallback := a.teamActorNamer(ctx)
	seen := map[string]string{}
	return func(actor string) string {
		if n, ok := seen[actor]; ok {
			return n
		}
		n := ""
		if id, perr := ids.Parse[ids.Actor](actor); err == nil && perr == nil {
			v := a.actorReport(ctx, st, id.Any()).Shown().Values
			n = v.Handle
			if n == "" {
				n = v.Name
			}
		}
		if n == "" {
			n = fallback(actor)
		}
		seen[actor] = n
		return n
	}
}
